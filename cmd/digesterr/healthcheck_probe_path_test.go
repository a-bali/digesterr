package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The container probe is a SEPARATE process with a static command line, so it
// cannot see the server's flags or environment. The server records the port it
// actually bound so the probe does not have to re-derive it from a subset of the
// inputs. These tests pin where the probe looks for that record.
//
// There is an inherent limit worth being explicit about: a probe launched with
// NO arguments can only find a record in a location it can name without help.
// The two such locations are its working directory (the container's WORKDIR) and
// the data directory the server falls back to. When the server's config lives
// somewhere else entirely, the probe must be told that path with -c/-config --
// which is why the flag exists and is honoured by the search.
//
// The failure this guards against is not "probe reports unhealthy" but the
// opposite: missing the record, silently falling back to a derived port, and
// reporting HEALTHY while probing a port this server does not serve.

// The reported CLI-override bug: `digesterr -p 7004 -c /abs/conf.yaml` binds and
// records 7004 while the config still says 7003. A probe pointed at the same
// config must read the record, not the stale config port.
func TestProbeFindsRecordBesideTheConfigItIsGiven(t *testing.T) {
	confDir := t.TempDir()
	absCfg := filepath.Join(confDir, "conf.yaml")
	if err := os.WriteFile(absCfg, []byte("server:\n  port: 7003\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The server bound 7004 (CLI override) and recorded that beside its config.
	written := portFilePath(absCfg)
	if err := os.WriteFile(written, []byte("7004\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProbePort(absCfg, "", candidatePortFiles(absCfg, DefaultPortFile))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 7004 {
		t.Errorf("probe resolved port %d, want 7004 (the port actually bound): the record at %q "+
			"must win over the config's stale port", got, written)
	}
}

// The container case with no arguments: the record is in the probe's working
// directory (WORKDIR /data, where the server writes when its config path is
// relative). t.Chdir makes "working directory" testable.
func TestProbeFindsRecordInItsWorkingDirectory(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, DefaultPortFile), []byte("7005\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	got, err := resolveProbePort("", "", candidatePortFiles("", DefaultPortFile))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 7005 {
		t.Errorf("probe resolved port %d, want 7005: a record in the working directory must be "+
			"found without arguments", got)
	}
}

// The ko-image case: HOME is unset, so DefaultPath() degrades to a relative
// name and the config-adjacent write lands in cwd == /. The server then falls
// back to the data directory. The probe must search that location too, or it
// finds no record at all in exactly the deployment we ship.
func TestProbeAlwaysSearchesTheDataDirectory(t *testing.T) {
	want := "/data/" + DefaultPortFile
	for _, c := range candidatePortFiles("", DefaultPortFile) {
		if c == want {
			return
		}
	}
	t.Errorf("candidatePortFiles = %v, want it to include %q: the container fallback record "+
		"lives there", candidatePortFiles("", DefaultPortFile), want)
}

// An explicit -port-file wins outright, so an operator can always point the
// probe at a specific record.
func TestExplicitPortFileWinsOverDiscovery(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "custom-port")
	if err := os.WriteFile(explicit, []byte("7006\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := candidatePortFiles("/somewhere/else.yaml", explicit)
	if len(got) != 1 || got[0] != explicit {
		t.Errorf("candidatePortFiles = %v, want exactly [%q] when the file is named explicitly", got, explicit)
	}
}

// The server must record the port at EVERY location the probe searches. Writing
// to a single preferred location was the bug: with an absolute -c path the record
// sat somewhere a no-argument probe never looks, so the probe derived a different
// port and could report healthy while this server was dead.
func TestServerRecordsAtEveryProbeLocation(t *testing.T) {
	confDir := t.TempDir()
	dataDir := t.TempDir()
	cfgPath := filepath.Join(confDir, "conf.yaml")
	work := t.TempDir()
	t.Chdir(work)

	locs := portFileWriteLocations(cfgPath, filepath.Join(dataDir, "digesterr.db"))

	// The three distinct locations, and no duplicates.
	want := []string{portFilePath(cfgPath), portFileInDataDir(filepath.Join(dataDir, "digesterr.db")), filepath.Join(work, DefaultPortFile)}
	if len(locs) != len(want) {
		t.Fatalf("portFileWriteLocations = %v, want %d distinct locations", locs, len(want))
	}
	for i, w := range want {
		if locs[i] != w {
			t.Errorf("location %d = %q, want %q", i, locs[i], w)
		}
	}

	// Writing to all of them makes the port discoverable with no arguments at
	// all -- the container case, where the probe knows only its WORKDIR.
	for _, l := range locs {
		if err := writePortFile(l, 7011); err != nil {
			t.Fatalf("write %q: %v", l, err)
		}
	}
	got, err := resolveProbePort("", "", candidatePortFiles("", DefaultPortFile))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 7011 {
		t.Errorf("probe resolved %d, want 7011 with every location recorded", got)
	}

	// And every candidate the probe searches for THIS config must be a location
	// the server records, since a candidate nobody writes is dead weight. Both
	// sides must use the same config path: with no -c the server (and the probe)
	// use $HOME/.digesterr.yaml, which is a different location entirely.
	candidates := candidatePortFiles(cfgPath, DefaultPortFile)
	written := make(map[string]bool, len(locs))
	for _, l := range locs {
		written[l] = true
	}
	written[filepath.Join(work, DefaultPortFile)] = true
	for _, c := range candidates {
		// Resolve relative candidates the way the OS would before comparing:
		// ".digesterr-port" IS work/.digesterr-port, not a separate location.
		abs := c
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(work, abs)
		}
		if abs == "/data/"+DefaultPortFile {
			continue // container-only; equals the data-dir location there
		}
		if !written[abs] {
			t.Errorf("probe searches %q (%q resolved) but the server never writes it (writes: %v)",
				c, abs, locs)
		}
	}

	// The no-argument server and probe agree on the default config location too.
	defLocs := portFileWriteLocations("", filepath.Join(dataDir, "digesterr.db"))
	defWritten := make(map[string]bool, len(defLocs))
	for _, l := range defLocs {
		if abs, err := filepath.Abs(l); err == nil {
			defWritten[abs] = true
		}
	}
	for _, c := range candidatePortFiles("", DefaultPortFile) {
		if c == "/data/"+DefaultPortFile {
			continue
		}
		abs := c
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(work, abs)
		}
		if !defWritten[abs] {
			t.Errorf("with no config, probe searches %q (%q resolved) but the server writes %v",
				c, abs, defLocs)
		}
	}
}

// A -port flag beats every recorded location: the operator naming a port
// directly must not be second-guessed by a stale record from a previous start.
func TestPortFlagBeatsRecordedPort(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale-port")
	if err := os.WriteFile(stale, []byte("7007\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProbePort("", "7008", []string{stale})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 7008 {
		t.Errorf("resolveProbePort = %d, want 7008 (the -port flag wins)", got)
	}
}

// A garbage or unreadable record must not abort the search: the probe has to
// stay able to answer, not error out because one candidate is malformed.
func TestGarbageRecordFallsThroughDiscoveredCandidates(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage-port")
	if err := os.WriteFile(garbage, []byte("not-a-port\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good-port")
	if err := os.WriteFile(good, []byte("7009\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, found := readRecordedPort([]string{garbage, filepath.Join(dir, "absent"), good})
	if !found || got != 7009 {
		t.Errorf("readRecordedPort = (%d,%v), want (7009,true): a garbage record must be "+
			"skipped rather than aborting the search", got, found)
	}
}

// With no record anywhere, the probe falls back to full config resolution and
// still answers -- degraded detection must not become a hard error.
func TestMissingRecordFallsBackToConfigResolution(t *testing.T) {
	confDir := t.TempDir()
	cfgPath := filepath.Join(confDir, "conf.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7010\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProbePort(cfgPath, "", []string{filepath.Join(confDir, "absent")})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 7010 {
		t.Errorf("resolveProbePort = %d, want 7010 from the config", got)
	}
}
