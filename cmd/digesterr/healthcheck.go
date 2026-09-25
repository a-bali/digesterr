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

// resolveProbePort determines which port the probe should ask.
//
// Order: an explicit -port flag (the operator is naming the port directly, so it
// must win), then the port file the server wrote (authoritative for CLI and YAML
// ports the probe cannot otherwise see), then the full config resolution
// (defaults + YAML + env, exactly as the server does it).
//
// The fallback chain matters when the probe runs before the server has written
// its file (a failing start), or against a data directory that is not writable:
// the probe must still be able to report unhealthy rather than error out.
func resolveProbePort(configPath, portFlag, portFile string) (int, error) {
	if portFlag != "" {
		p, err := strconv.Atoi(portFlag)
		if err != nil {
			return 0, fmt.Errorf("invalid -port value %q", portFlag)
		}
		return p, nil
	}
	if portFile != "" {
		if b, err := os.ReadFile(portFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
				return p, nil
			}
		}
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

	port, err := resolveProbePort(*configPath, *portFlag, *portFile)
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
