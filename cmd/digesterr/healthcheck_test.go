package main

import (
	"os"
	"path/filepath"
	"testing"

	"digesterr/internal/config"
)

// The container probe used to read the port straight from SERVER_PORT/PORT. A
// port given in the YAML (`server.port`) or as a CLI flag (`-port`) was
// invisible to it, so a container serving fine on a custom port reported
// unhealthy and was restarted in a loop. The reviewer asked for the probe to
// follow the EFFECTIVE port, whatever the source.
//
// The probe cannot read the server's flags or environment, so the server records
// the port it actually bound and the probe prefers that. These are the unit
// tests for the resolution order; the end-to-end proof runs the real binary.

func TestResolveProbePortPrefersRecordedPortFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	// The config says one thing...
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// ...and the server recorded another, which is authoritative because it is
	// the port that was actually bound (it may have come from a CLI flag the
	// probe cannot see).
	portFile := filepath.Join(dir, DefaultPortFile)
	if err := os.WriteFile(portFile, []byte("9007\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveProbePort(cfgPath, "", portFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != 9007 {
		t.Errorf("port = %d, want 9007: the recorded port is what the server bound, "+
			"and covers CLI flags the probe cannot read", got)
	}
}

// TestResolveProbePortFollowsYAMLViaConfig is the other half: with no recorded
// file (a failing start, or an unwritable data dir) the probe must still follow
// the YAML, not fall back to a hardcoded 8080.
func TestResolveProbePortFollowsYAMLViaConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7002\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No port file.
	if got, err := resolveProbePort(cfgPath, "", filepath.Join(dir, "absent")); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if got != 7002 {
		t.Errorf("port = %d, want 7002 from the YAML", got)
	}
}

func TestResolveProbePortFlagOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7003\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProbePort(cfgPath, "7004", filepath.Join(dir, "absent")); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if got != 7004 {
		t.Errorf("port = %d, want the -port flag value 7004", got)
	}
}

// TestResolveProbePortFlagOverridesRecordedPort is the case the first version of
// this code got WRONG, and that the earlier tests missed because they only ever
// paired a flag with a MISSING port file. A recorded port took precedence over
// an explicit -port, so `healthcheck -p 59998` silently probed the running
// server's port instead and reported healthy -- a probe that cannot be aimed at
// a different port is not a probe. An operator naming a port must win.
func TestResolveProbePortFlagOverridesRecordedPort(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7003\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	portFile := filepath.Join(dir, DefaultPortFile)
	if err := os.WriteFile(portFile, []byte("8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProbePort(cfgPath, "59998", portFile); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if got != 59998 {
		t.Errorf("port = %d, want the explicit -port value 59998: a recorded port must not "+
			"silently override a port the operator named", got)
	}
}

func TestResolveProbePortEnvStillWorks(t *testing.T) {
	// The original mechanism must keep working, or the fix regresses the case
	// that already worked.
	t.Setenv("SERVER_PORT", "7005")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7006\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProbePort(cfgPath, "", filepath.Join(dir, "absent")); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if got != 7005 {
		t.Errorf("port = %d, want 7005 from SERVER_PORT (env beats the file)", got)
	}
}

func TestResolveProbePortFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveProbePort(filepath.Join(dir, "absent.yaml"), "", filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("resolve on a missing config must not error (defaults apply): %v", err)
	}
	if want := config.Default().Server.Port; got != want {
		t.Errorf("port = %d, want the built-in default %d", got, want)
	}
}

// TestResolveProbePortIgnoresGarbagePortFile covers a truncated or corrupt
// record: the probe must fall through to the config rather than trust it.
func TestResolveProbePortIgnoresGarbagePortFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 7008\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	portFile := filepath.Join(dir, DefaultPortFile)
	if err := os.WriteFile(portFile, []byte("not-a-port\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProbePort(cfgPath, "", portFile); err != nil {
		t.Fatalf("resolve: %v", err)
	} else if got != 7008 {
		t.Errorf("port = %d, want 7008: a corrupt port file must fall through to the config", got)
	}
}

// TestPortFilePathSitsBesideTheConfig pins where the record is written, so two
// servers with different configs do not clobber each other.
func TestPortFilePathSitsBesideTheConfig(t *testing.T) {
	got := portFilePath("/etc/digesterr/prod.yaml")
	if want := filepath.Join("/etc/digesterr", DefaultPortFile); got != want {
		t.Errorf("portFilePath = %q, want %q", got, want)
	}
}
