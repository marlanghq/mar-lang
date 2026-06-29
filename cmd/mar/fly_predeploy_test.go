package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mar/internal/runtime"
	"mar/internal/scaffold"
)

// flyPreDeployValidate gates `mar fly deploy`: a server (backend/fullstack)
// app whose mar.json is missing the production config its Auth.config needs
// must be rejected as a pre-flight — before any Fly resource is created —
// so a misconfigured project never leaves an orphaned app/volume/secrets.
// Frontend deploys carry no server-side config, so validation is skipped.
//
// Uses the same fake-Auth trick as scaffold/build_test.go: the validator
// only checks CurrentAuth() != nil, which RegisterAuth simulates without
// running a full program (the real deploy gets here after Topology runs main).
func TestFlyPreDeployValidate(t *testing.T) {
	runtime.ResetAuthForTesting()
	t.Cleanup(runtime.ResetAuthForTesting)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mar.json"), []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.RegisterAuth(runtime.VAuth{}) // simulate Auth.config running in main

	// Backend: Auth in use + no mail config → rejected up front, with the
	// typed error the CLI renders as a missing-config block.
	err := flyPreDeployValidate(dir, flyTopologyBackend)
	if err == nil {
		t.Fatal("backend deploy with Auth but no mail config: want a pre-flight error, got nil")
	}
	var pcErr *scaffold.ProductionConfigError
	if !errors.As(err, &pcErr) {
		t.Fatalf("want *scaffold.ProductionConfigError, got %T: %v", err, err)
	}

	// Fullstack: same gate as backend.
	if err := flyPreDeployValidate(dir, flyTopologyFullstack); err == nil {
		t.Fatal("fullstack deploy with Auth but no mail config: want a pre-flight error, got nil")
	}

	// Frontend: no server-side config to validate → skipped, even though
	// Auth happens to be registered in this test's global state.
	if err := flyPreDeployValidate(dir, flyTopologyFrontend); err != nil {
		t.Fatalf("frontend deploy should skip production-config validation; got %v", err)
	}
}
