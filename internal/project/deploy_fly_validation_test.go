package project

import (
	"errors"
	"strings"
	"testing"
)

// `mar deploy` writes a fly.toml from this block, and a field it does not
// check becomes a deploy that fails at Fly rather than at the manifest. The
// validation existed with no test.
func TestFlyDeployConfigIsCheckedBeforeTheDeploy(t *testing.T) {
	manifest := func(app, region, memory string) *Manifest {
		return &Manifest{Name: "x", Locale: "en", Deploy: &DeployConfig{
			Fly: &FlyDeployConfig{App: app, Region: region, Memory: memory},
		}}
	}

	refused := []struct {
		name string
		m    *Manifest
		kind string
	}{
		{"no deploy block at all", &Manifest{Name: "x", Locale: "en"}, "missing-block"},
		{"no app name", manifest("", "gru", "512mb"), "missing-app"},
		{"no region", manifest("a", "", "512mb"), "missing-region"},
		{"no memory", manifest("a", "gru", ""), "missing-memory"},
		{"a memory size Fly does not sell", manifest("a", "gru", "300mb"), "invalid-memory"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeployFly(tc.m)
			if err == nil {
				t.Fatal("this would have reached Fly and failed there")
			}
			var flyErr *DeployFlyError
			if !errors.As(err, &flyErr) {
				t.Fatalf("the CLI renders a hint from the typed error, got %T", err)
			}
			if flyErr.Kind != tc.kind {
				t.Errorf("kind %q, want %q", flyErr.Kind, tc.kind)
			}
		})
	}

	// A complete block passes, and the message for a bad size names the sizes
	// that work — the whole reason the list is a constant.
	t.Run("a complete block", func(t *testing.T) {
		if err := ValidateDeployFly(manifest("a", "gru", AllowedFlyMemorySizes[0])); err != nil {
			t.Fatalf("this is a deployable block: %v", err)
		}
	})
	t.Run("the bad-size message lists the good ones", func(t *testing.T) {
		err := ValidateDeployFly(manifest("a", "gru", "300mb"))
		if err == nil {
			t.Fatal("300mb is not a size Fly sells")
		}
		if !strings.Contains(err.Error(), AllowedFlyMemorySizes[0]) {
			t.Errorf("the message should say what to write instead: %v", err)
		}
	})
}
