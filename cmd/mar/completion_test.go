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
	"config", "migrate", "admin", "fly", "cloudflare-pages",
	"repl", "lsp", "completion", "version",
}

// cloudflarePagesSubcommands — the only sub right now is `deploy`.
// Listed separately from flySubcommands so the per-shell assertions
// can confirm both top-level commands offer their own subs.
var cloudflarePagesSubcommands = []string{"deploy"}

// flySubcommands, migrateSubcommands, adminSubcommands, and the
// fly third-level subs are the inner sets that `mar <cmd> <tab>`
// (or `mar <cmd> <sub> <tab>`) should expand to. Driving the
// completion through these guards against two regression modes:
// forgetting to register a new subcommand, or feeding `_describe`
// loose args (zsh interprets them as helper-function names and
// leaks `__tmp_*` into the candidate list — the array-indirection
// pattern in zshCompletion prevents this).
var flySubcommands = []string{
	"preview", "deploy", "logs", "status",
	"admin", "database", "db", "secrets", "destroy",
}

var migrateSubcommands = []string{
	"plan", "status",
}

var adminSubcommands = []string{
	"add", "remove", "list",
}

var flySecretsSubcommands = []string{
	"set", "list", "unset",
}

var flyDatabaseSubcommands = []string{
	"backup", "backups",
}

var flyAdminSubcommands = []string{
	"list",
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

// TestZshFlyAndMigrateUseArrayIndirection pins the specific shape
// `_describe` requires. Passing loose strings as args makes zsh
// interpret them as completion-helper names and leaks candidates
// like `_a_11`, `_tmpd`, `_tmpm` into the menu. The correct shape
// is to declare a `local -a <name>` array and hand it to
// `_describe '...' name`.
//
// We assert the array variable is declared AND that every documented
// subcommand label appears inside it.
func TestZshFlyAndMigrateUseArrayIndirection(t *testing.T) {
	out := zshCompletion()
	if !strings.Contains(out, "local -a fly_subs") {
		t.Error("zsh: fly subcommand block missing `local -a fly_subs` declaration")
	}
	// fly subcommands use `-V <groupname>` so zsh keeps them in
	// natural lifecycle order (preview→deploy→…) instead of sorting
	// alphabetically. The group name is arbitrary, the flag matters.
	if !strings.Contains(out, "_describe -V") || !strings.Contains(out, "fly_subs") {
		t.Error("zsh: fly subcommand block must call _describe with -V <group> for natural order")
	}
	for _, sub := range flySubcommands {
		if !strings.Contains(out, "'"+sub+":") {
			t.Errorf("zsh: fly_subs missing %q label", sub)
		}
	}

	if !strings.Contains(out, "local -a migrate_subs") {
		t.Error("zsh: migrate subcommand block missing `local -a migrate_subs` declaration")
	}
	// Same -V <group> requirement as fly_subs above: keep migrate's
	// plan-then-status order, don't let zsh re-sort.
	if !strings.Contains(out, "_describe -V") || !strings.Contains(out, "migrate_subs") {
		t.Error("zsh: migrate subcommand block must call _describe with -V <group> for natural order")
	}
	for _, sub := range migrateSubcommands {
		if !strings.Contains(out, "'"+sub+":") {
			t.Errorf("zsh: migrate_subs missing %q label", sub)
		}
	}
}

// TestBashFlyAndMigrateSubs ensures the bash equivalent suggests
// the right subcommands when the user types `mar fly <tab>` or
// `mar migrate <tab>`. Now also covers admin (top-level) + the
// fly third-level subs (admin / database / secrets).
func TestBashFlyAndMigrateSubs(t *testing.T) {
	out := bashCompletion()
	// Top-level fly subs — string-literal embedded as flySubcommandList.
	for _, sub := range flySubcommands {
		// Match the word boundary inside the compgen -W "..." list.
		if !strings.Contains(out, " "+sub+" ") && !strings.Contains(out, " "+sub+"\"") && !strings.Contains(out, "\""+sub+" ") {
			t.Errorf("bash: missing fly subcommand %q", sub)
		}
	}
	migLine := "compgen -W \"" + strings.Join(migrateSubcommands, " ") + "\""
	if !strings.Contains(out, migLine) {
		t.Errorf("bash: missing migrate subcommand line\n  want: %q", migLine)
	}
	// fly third-level subs.
	for _, sub := range flySecretsSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("bash: missing fly secrets sub %q", sub)
		}
	}
	for _, sub := range flyDatabaseSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("bash: missing fly database sub %q", sub)
		}
	}
	for _, sub := range flyAdminSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("bash: missing fly admin sub %q", sub)
		}
	}
}

// TestFishFlyAndMigrateSubs ensures fish gets the same subcommand
// list. fish format is `complete ... -a 'a b c'`.
func TestFishFlyAndMigrateSubs(t *testing.T) {
	out := fishCompletion()
	for _, sub := range flySubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("fish: missing fly subcommand %q", sub)
		}
	}
	migLine := "-a 'plan status'"
	if !strings.Contains(out, migLine) {
		t.Errorf("fish: missing migrate subcommand line\n  want: %q", migLine)
	}
	for _, sub := range flySecretsSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("fish: missing fly secrets sub %q", sub)
		}
	}
	for _, sub := range flyDatabaseSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("fish: missing fly database sub %q", sub)
		}
	}
	for _, sub := range flyAdminSubcommands {
		if !strings.Contains(out, sub) {
			t.Errorf("fish: missing fly admin sub %q", sub)
		}
	}
}

// TestAllShells_listAdminSubs checks the new top-level `mar admin`
// subcommands (add / remove / list) appear in each shell's script.
func TestAllShells_listAdminSubs(t *testing.T) {
	for shell, out := range map[string]string{
		"zsh":  zshCompletion(),
		"bash": bashCompletion(),
		"fish": fishCompletion(),
	} {
		for _, sub := range adminSubcommands {
			if !strings.Contains(out, sub) {
				t.Errorf("%s: missing admin subcommand %q", shell, sub)
			}
		}
	}
}

// TestAllShells_listCloudflarePagesSubs — `mar cloudflare-pages` sits
// next to `mar fly` as a second deploy target. Each shell should
// offer its sub(s) so users discover the path via tab. Today the only
// sub is `deploy`; this test catches us forgetting to add new subs
// (or new top-level deploy targets) when they land.
func TestAllShells_listCloudflarePagesSubs(t *testing.T) {
	for shell, out := range map[string]string{
		"zsh":  zshCompletion(),
		"bash": bashCompletion(),
		"fish": fishCompletion(),
	} {
		if !strings.Contains(out, "cloudflare-pages") {
			t.Errorf("%s: missing top-level cloudflare-pages command", shell)
		}
		for _, sub := range cloudflarePagesSubcommands {
			if !strings.Contains(out, sub) {
				t.Errorf("%s: missing cloudflare-pages sub %q", shell, sub)
			}
		}
	}
}

// TestAllShells_haveNoOpenFlag — the --no-open flag was added to
// `mar dev` and `mar fly deploy` after MAR_NO_OPEN was removed.
// Each shell should advertise it so users discover it via tab.
func TestAllShells_haveNoOpenFlag(t *testing.T) {
	for shell, out := range map[string]string{
		"zsh":  zshCompletion(),
		"bash": bashCompletion(),
		"fish": fishCompletion(),
	} {
		if !strings.Contains(out, "--no-open") {
			t.Errorf("%s: missing --no-open flag", shell)
		}
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
