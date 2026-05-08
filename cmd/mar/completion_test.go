package main

import (
	"strings"
	"testing"
)

// Sanity check on the generated scripts: all three should mention each
// top-level subcommand by name AND list the build targets after
// `--target`. If we add a new subcommand or build target later and
// forget to update completion.go, these tests catch it.

var topLevelCommands = []string{
	"dev", "build", "init", "check", "format",
	"config", "migrate", "fly",
	"repl", "lsp", "completion", "version",
}

// flySubcommands and migrateSubcommands are the inner sets that
// `mar fly <tab>` / `mar migrate <tab>` should expand to. Driving
// the completion through these guards regression on either side
// (forgetting to register a new subcommand, or breaking the zsh
// _describe array indirection that previously leaked __tmp_*
// helper functions into the candidate list).
var flySubcommands = []string{
	"init", "provision", "deploy", "logs", "status", "destroy",
}

var migrateSubcommands = []string{
	"plan", "status",
}

var buildTargetList = []string{
	"darwin-amd64", "darwin-arm64",
	"linux-amd64", "linux-arm64",
	"windows-amd64", "ios",
}

func TestZshCompletion_listsAllCommands(t *testing.T) {
	out := zshCompletion()
	for _, cmd := range topLevelCommands {
		if !strings.Contains(out, "'"+cmd+":") {
			t.Errorf("zsh script missing command %q", cmd)
		}
	}
}

func TestBashCompletion_listsAllCommands(t *testing.T) {
	out := bashCompletion()
	for _, cmd := range topLevelCommands {
		if !strings.Contains(out, cmd) {
			t.Errorf("bash script missing command %q", cmd)
		}
	}
}

func TestFishCompletion_listsAllCommands(t *testing.T) {
	out := fishCompletion()
	for _, cmd := range topLevelCommands {
		if !strings.Contains(out, "-a "+cmd+" ") {
			t.Errorf("fish script missing command %q", cmd)
		}
	}
}

// TestZshFlyAndMigrateUseArrayIndirection regression-tests the
// specific shape `_describe` requires. A previous version passed
// loose strings as args, which zsh interpreted as completion-helper
// names and leaked candidates like `_a_11`, `_tmpd`, `_tmpm` into
// the menu. The fix is to declare a `local -a <name>` array and
// hand it to `_describe '...' name`.
//
// We assert the array variable is declared AND that every documented
// subcommand label appears inside it.
func TestZshFlyAndMigrateUseArrayIndirection(t *testing.T) {
	out := zshCompletion()
	if !strings.Contains(out, "local -a fly_subs") {
		t.Error("zsh: fly subcommand block missing `local -a fly_subs` declaration")
	}
	if !strings.Contains(out, "_describe 'subcommand' fly_subs") {
		t.Error("zsh: fly subcommand block must call _describe with the array name (not loose args)")
	}
	for _, sub := range flySubcommands {
		if !strings.Contains(out, "'"+sub+":") {
			t.Errorf("zsh: fly_subs missing %q label", sub)
		}
	}

	if !strings.Contains(out, "local -a migrate_subs") {
		t.Error("zsh: migrate subcommand block missing `local -a migrate_subs` declaration")
	}
	if !strings.Contains(out, "_describe 'subcommand' migrate_subs") {
		t.Error("zsh: migrate subcommand block must call _describe with the array name")
	}
	for _, sub := range migrateSubcommands {
		if !strings.Contains(out, "'"+sub+":") {
			t.Errorf("zsh: migrate_subs missing %q label", sub)
		}
	}
}

// TestBashFlyAndMigrateSubs ensures the bash equivalent suggests
// the right subcommands when the user types `mar fly <tab>` or
// `mar migrate <tab>`.
func TestBashFlyAndMigrateSubs(t *testing.T) {
	out := bashCompletion()
	flyLine := "compgen -W \"" + strings.Join(flySubcommands, " ") + "\""
	if !strings.Contains(out, flyLine) {
		t.Errorf("bash: missing fly subcommand line\n  want: %q", flyLine)
	}
	migLine := "compgen -W \"" + strings.Join(migrateSubcommands, " ") + "\""
	if !strings.Contains(out, migLine) {
		t.Errorf("bash: missing migrate subcommand line\n  want: %q", migLine)
	}
}

// TestFishFlyAndMigrateSubs ensures fish gets the same subcommand
// list. fish format is `complete ... -a 'a b c'`.
func TestFishFlyAndMigrateSubs(t *testing.T) {
	out := fishCompletion()
	flyLine := "-a '" + strings.Join(flySubcommands, " ") + "'"
	if !strings.Contains(out, flyLine) {
		t.Errorf("fish: missing fly subcommand line\n  want: %q", flyLine)
	}
	migLine := "-a '" + strings.Join(migrateSubcommands, " ") + "'"
	if !strings.Contains(out, migLine) {
		t.Errorf("fish: missing migrate subcommand line\n  want: %q", migLine)
	}
}

func TestAllShells_includeBuildTargets(t *testing.T) {
	scripts := map[string]string{
		"zsh":  zshCompletion(),
		"bash": bashCompletion(),
		"fish": fishCompletion(),
	}
	for shell, out := range scripts {
		for _, target := range buildTargetList {
			if !strings.Contains(out, target) {
				t.Errorf("%s script missing build target %q", shell, target)
			}
		}
	}
}

func TestAllShells_mentionFormatCheckFlag(t *testing.T) {
	for shell, out := range map[string]string{
		"zsh":  zshCompletion(),
		"bash": bashCompletion(),
		"fish": fishCompletion(),
	} {
		if !strings.Contains(out, "--check") {
			t.Errorf("%s script missing --check flag for `mar format`", shell)
		}
	}
}

func TestRunCompletion_unknownShell(t *testing.T) {
	if got := runCompletion([]string{"powershell"}); got != 2 {
		t.Errorf("expected exit 2 for unknown shell, got %d", got)
	}
}

func TestRunCompletion_missingArg(t *testing.T) {
	if got := runCompletion(nil); got != 2 {
		t.Errorf("expected exit 2 for missing arg, got %d", got)
	}
}

func TestRunCompletion_validShells(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish"} {
		if got := runCompletion([]string{sh}); got != 0 {
			t.Errorf("%s: expected exit 0, got %d", sh, got)
		}
	}
}
