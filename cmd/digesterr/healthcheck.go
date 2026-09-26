package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"digesterr/internal/config"
)

// DefaultPortFile is where the running server records the port it actually
// bound, so the health probe can find it.
//
// A probe is a SEPARATE process with a static command line, so it cannot see
// the server's flags or read its environment: a port given as `-port 9000` or
// `server.port: 9000` in the YAML is invisible to it. Recoding the resolved port
// at startup is the only mechanism that covers every source -- defaults, env,
// YAML and CLI alike -- because it records the OUTCOME of the same resolution
// the server performed rather than re-deriving it from a subset of the inputs.
const DefaultPortFile = ".digesterr-port"

// portFilePath is where the bound port is recorded for the health probe. It
// lives beside the config file so two servers started with different configs do
// not overwrite each other's record.
func portFilePath(configPath string) string {
	path := configPath
	if path == "" {
		path = config.DefaultPath()
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return DefaultPortFile
	}
	return filepath.Join(dir, DefaultPortFile)
}

// portFileInDataDir is the second-best place to record the bound port: the
// directory holding the database, which is a mounted volume in every deployment
// shape we ship. It exists because the config-adjacent path is not always
// writable (the container runs as uid 1000 with cwd / and no config file), and a
// refused write there left the probe with no record at all.
//
// A relative DB path with no directory component resolves to the process's
// working directory, which is what the fallback should use.
func portFileInDataDir(dbPath string) string {
	dir := filepath.Dir(dbPath)
	if dir == "" || dir == "." {
		return DefaultPortFile
	}
	return filepath.Join(dir, DefaultPortFile)
}

// portFileWriteLocations is where the server records its bound port. It records
// at EVERY location the probe searches, not just a preferred one, because the
// probe is a separate process that may know nothing about the server's -c path:
// a single location only works when it happens to coincide with the probe's view
// of the filesystem.
//
// Concretely, `digesterr -c /etc/digesterr/conf.yaml -port 9000` used to record
// only /etc/digesterr/.digesterr-port. A no-argument container probe never looks
// there, so it fell back to deriving a port and probed the wrong one -- reporting
// healthy while this server was dead. Recording a copy in the data directory (a
// mounted volume, and the probe's working directory) makes the record reachable.
//
// Order is preference order: the config-adjacent record keeps two servers with
// different configs from clobbering each other, and is the one the probe tries
// first. Duplicates are dropped so the shared location is not written twice.
func portFileWriteLocations(configPath, dbPath string) []string {
	var out []string
	seen := make(map[string]bool, 3)
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(portFilePath(configPath))
	add(portFileInDataDir(dbPath))
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, DefaultPortFile))
	}
	return out
}

// The port file is written by the server BESIDE ITS CONFIG (main.go passes
// portFilePath(*configPath)), while the container probe is a separate process
// with a static command line and no -config argument, running from WORKDIR
// /data. Those two locations only coincide when the config happens to sit in the
// probe's working directory. When it does not, the probe never sees the record
// and silently falls back to re-deriving the port from the config it can find
// (or the built-in default) -- so with an absolute -c path, or a CLI -port
// override the probe cannot read, it probes the WRONG port. Probing a default
// port that another process happens to serve reports healthy while this server
// is dead: a false healthy, which is worse than a plain false unhealthy.
//
// The record is therefore looked for at every plausible location before falling
// back. Callers may also pass an explicit file, which wins outright.
func candidatePortFiles(configPath, explicit string) []string {
	if explicit != "" && explicit != DefaultPortFile {
		// An operator naming the file is authoritative.
		return []string{explicit}
	}
	var out []string
	// Preferred: beside the config the server was started with.
	if cp := portFilePath(configPath); cp != DefaultPortFile {
		out = append(out, cp)
	}
	// The probe's own working directory, which is where the container writes it
	// when the server records beside a RELATIVE config path.
	if wd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(wd, DefaultPortFile))
	}
	out = append(out, DefaultPortFile)
	// The data directory the server falls back to when it has no config to
	// record beside -- the container case, where a write to / is refused.
	out = append(out, "/data/"+DefaultPortFile)
	return out
}

// readRecordedPort returns the first usable port record, or ok=false when none
// exists. Paths are de-duplicated so a repeated location is not read twice.
func readRecordedPort(paths []string) (int, bool) {
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		b, err := os.ReadFile(p)
		if err != nil {
			// Keep looking: a missing/unreadable candidate is not an error, it
			// is simply not where the server recorded its port.
			continue
		}
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
			return p, true
		}
	}
	return 0, false
}

// resolveProbePort determines which port the probe should ask.
//
// Order: an explicit -port flag (the operator is naming the port directly, so it
// must win), then the port record the server wrote (authoritative for CLI and
// YAML ports the probe cannot otherwise see), then the full config resolution
// (defaults + YAML + env, exactly as the server does it).
//
// The fallback chain matters when the probe runs before the server has written
// its record (a failing start), or against a directory that is not writable:
// the probe must still be able to report unhealthy rather than error out.
func resolveProbePort(configPath, portFlag string, portFiles []string) (int, error) {
	if portFlag != "" {
		p, err := strconv.Atoi(portFlag)
		if err != nil {
			return 0, fmt.Errorf("invalid -port value %q", portFlag)
		}
		return p, nil
	}
	if p, ok := readRecordedPort(portFiles); ok {
		return p, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return 0, err
	}
	return cfg.Server.Port, nil
}

// writePortFile records the bound port for the health probe. A failure is not
// fatal: the probe falls back to resolving the config itself, so losing the file
// degrades the port detection rather than stopping the server.
func writePortFile(path string, port int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(port)+"\n"), 0o644)
}

// runHealthcheck probes the server's own /healthz and returns the process exit
// code. It is invoked as `digesterr healthcheck` from the container's
// HEALTHCHECK, which is why it must resolve the port rather than assume 8080.
func runHealthcheck(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to YAML config file")
	fs.StringVar(configPath, "c", "", "shorthand for -config")
	portFlag := fs.String("port", "", "HTTP port to probe (overrides config file and env)")
	fs.StringVar(portFlag, "p", "", "shorthand for -port")
	portFile := fs.String("port-file", DefaultPortFile, "file where the server records its bound port")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 2
	}

	port, err := resolveProbePort(*configPath, *portFlag, candidatePortFiles(*configPath, *portFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: resolve port:", err)
		return 1
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: probe", url+":", err)
		return 1
	}
	defer resp.Body.Close()
	// Drain a little so the connection can be reused and the body is not left
	// half-read; the payload itself is irrelevant, only the status is.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: probe %s: status %d\n", url, resp.StatusCode)
		return 1
	}
	return 0
}
