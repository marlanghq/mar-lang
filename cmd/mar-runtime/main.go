// Command mar-runtime is the lean production stub. `mar build` produces
// a self-contained executable by concatenating mar-runtime + an
// app-bundle payload with a footer marker; on startup the binary reads
// its own file, extracts the payload, evaluates the user's main, and
// serves HTTP.
//
// Two modes:
//
//   - Stamped (default in built binaries): the executable carries an
//     embedded payload at its tail. We extract it to a temp dir and
//     run from there.
//   - Bare (development / debugging the stub itself): no payload found.
//     Falls back to taking a project directory or Main.mar path on the
//     command line — useful for testing the runtime stub directly
//     before plumbing the bundle pipeline.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/appbundle"
	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/ratelimit"
	"mar/internal/runtime"
)

// manifestAdmins is a tiny shim so canonicalAdmins's caller doesn't
// have to nil-check the manifest.
func manifestAdmins(m *project.Manifest) []string {
	if m == nil {
		return nil
	}
	return m.Admins
}

// canonicalAdmins canonicalizes manifest.Admins (lowercase + trim +
// dedupe + sort) for boot-time sync. Mirrors the cmd/mar
// LoadAdminsFromManifest helper but kept inline so this binary
// doesn't depend on the cmd/mar package.
func canonicalAdmins(emails []string) []string {
	out := make([]string, 0, len(emails))
	seen := make(map[string]bool, len(emails))
	for _, e := range emails {
		canon := strings.ToLower(strings.TrimSpace(e))
		if canon == "" || seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	sort.Strings(out)
	return out
}

func main() {
	// Subcommand dispatch happens BEFORE the embedded-bundle path,
	// so `mar-runtime admin list` (invoked over SSH from `mar fly
	// admin list`) doesn't try to extract + run the user's app.
	// Subcommands operate against the project's mar.db on the
	// running machine's filesystem; they exit when done rather than
	// transitioning into ServeLive.
	if len(os.Args) >= 2 && os.Args[1] == "admin" {
		os.Exit(runRuntimeAdmin(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "backup" {
		os.Exit(runRuntimeBackup(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "restore-db" {
		os.Exit(runRuntimeRestore(os.Args[2:]))
	}
	if err := runEmbeddedOrArg(); err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		os.Exit(1)
	}
}

// runRuntimeAdmin handles `mar-runtime admin <sub>`. The user-
// facing entry point on the host is `mar fly admin list`, which
// invokes this over SSH. Operates on the local mar.db without
// going through HTTP.
//
// v1 implements `list` only — the spec deliberately excludes
// runtime add/remove (see admin-panel.md §6.2). Adding admins
// happens via mar.json + redeploy.
func runRuntimeAdmin(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: mar-runtime admin list")
		return 2
	}
	switch args[0] {
	case "list":
		return runRuntimeAdminList()
	default:
		fmt.Fprintf(os.Stderr, "mar-runtime admin: unknown subcommand %q\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: mar-runtime admin list")
		return 2
	}
}

// runRuntimeAdminList opens the project's DB (extracting the bundle
// to a temp dir if necessary), reports both the desired list (from
// the bundled mar.json) and the post-sync runtime state.
func runRuntimeAdminList() int {
	manifest, projectDir, err := loadRuntimeManifest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime admin list: %v\n", err)
		return 1
	}
	dbPath, _ := project.ResolveDatabasePath(manifest, projectDir)
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "mar-runtime admin list: no database configured for this project")
		return 1
	}
	runtime.SetDBPath(dbPath)
	db, err := runtime.OpenDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime admin list: open db: %v\n", err)
		return 1
	}

	desired := canonicalAdmins(manifestAdmins(manifest))
	fmt.Println("admins (from mar.json on disk in prod):")
	if len(desired) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, e := range desired {
			fmt.Printf("  %s\n", e)
		}
	}

	live, err := admin.ListAdmins(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar-runtime admin list: read DB: %v\n", err)
		return 1
	}
	fmt.Println()
	fmt.Println("admins (from runtime DB, post-sync):")
	if len(live) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, a := range live {
			fmt.Printf("  %s\n", a.Email)
		}
	}

	fmt.Println()
	fmt.Println("last login:")
	anyLogin := false
	for _, a := range live {
		anyLogin = true
		if a.LastLoginAtMs == 0 {
			fmt.Printf("  %s  never\n", a.Email)
		} else {
			ago := time.Since(time.UnixMilli(a.LastLoginAtMs))
			fmt.Printf("  %s  %s ago\n", a.Email, formatDurationApprox(ago))
		}
	}
	if !anyLogin {
		fmt.Println("  (no admins configured)")
	}
	return 0
}

// formatDurationApprox renders durations like "2 hours" or
// "3 days" for human-readable last-login output.
func formatDurationApprox(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// loadRuntimeManifest mirrors the path discovery runEmbedded uses:
// if this binary is stamped, extract the bundle to a temp dir and
// load mar.json from there; otherwise treat os.Args[2] (the path
// after `admin list`) as the project dir.
//
// Tests run against bare-stub builds, so the os.Args path is the
// development scenario. Production always goes through the
// extract-bundle branch.
func loadRuntimeManifest() (*project.Manifest, string, error) {
	return loadRuntimeManifestWith(true /*resolveEnv*/)
}

// loadRuntimeManifestWith mirrors loadRuntimeManifest but lets the
// caller skip env:VAR resolution. Used by `mar-runtime backup` —
// backup just snapshots the DB + copies mar.json verbatim, so the
// env vars don't need to be in scope. Admin list keeps the default
// resolve=true (production runtime where vars are set anyway).
func loadRuntimeManifestWith(resolveEnv bool) (*project.Manifest, string, error) {
	load := project.LoadManifest
	if !resolveEnv {
		load = project.LoadManifestStructure
	}
	exePath, err := os.Executable()
	if err == nil {
		bundle, loadErr := appbundle.LoadExecutable(exePath)
		if loadErr == nil {
			tmp, err := os.MkdirTemp("", "mar-runtime-admin-*")
			if err != nil {
				return nil, "", err
			}
			if err := appbundle.ExtractToDir(bundle, tmp); err != nil {
				return nil, "", err
			}
			m, err := load(tmp)
			if err != nil {
				return nil, "", err
			}
			return m, tmp, nil
		}
	}
	// Bare mode: take the project dir from the next arg (or ".").
	dir := "."
	if len(os.Args) >= 4 {
		// args layout: mar-runtime admin list [dir]
		dir = os.Args[3]
	}
	m, err := load(dir)
	if err != nil {
		return nil, "", err
	}
	return m, dir, nil
}

// runEmbeddedOrArg first checks whether this binary was stamped with an
// app-bundle payload (the normal case for `mar build` output). If so we
// extract and run that. Otherwise we expect a CLI argument pointing at
// a project directory or Main.mar — the dev-time path.
func runEmbeddedOrArg() error {
	exePath, err := os.Executable()
	if err == nil {
		bundle, loadErr := appbundle.LoadExecutable(exePath)
		switch {
		case loadErr == nil:
			return runEmbedded(bundle)
		case errors.Is(loadErr, appbundle.ErrNoBundle):
			// fall through to CLI mode
		default:
			return loadErr
		}
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mar-runtime <project-dir-or-Main.mar>")
		os.Exit(2)
	}
	return runFromPath(os.Args[1])
}

// runEmbedded materializes the stamped payload onto disk under a temp
// dir (so the existing project loader, which reads from filesystem, can
// take over without changes) and then runs from there.
//
// We stamp MAR_LAUNCH_CWD with the user's startup cwd BEFORE
// extracting, so relative paths in mar.json (e.g. `./notes.db`) still
// resolve against the directory the user launched the binary from, not
// against the temp extraction dir. Without that, the SQLite file would
// land inside `/tmp/mar-runtime-xxx/` and disappear at the next OS
// cleanup — silently destroying production data on every restart.
func runEmbedded(b *appbundle.Bundle) error {
	if _, set := os.LookupEnv("MAR_LAUNCH_CWD"); !set {
		if cwd, err := os.Getwd(); err == nil {
			os.Setenv("MAR_LAUNCH_CWD", cwd)
		}
	}
	tmp, err := os.MkdirTemp("", "mar-runtime-*")
	if err != nil {
		return err
	}
	// Best-effort cleanup. We don't defer the rmdir because the server
	// loop (jsserve.ServeLive → http.Serve) never returns cleanly in
	// the success path — the process blocks there until SIGTERM. So
	// the temp dir lives until the OS reaps it on exit.
	if err := appbundle.ExtractToDir(b, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return runFromPath(tmp)
}

// runFromPath loads the project at `path` (file or directory) and serves
// it in production mode: no hot-reload, no dev banner, no SSE channel,
// no time-travel panel in the browser. Configuration (port, database
// path) comes from mar.json in the project directory.
func runFromPath(path string) error {
	entryFile, projectDir, err := resolveEntry(path)
	if err != nil {
		return err
	}

	port := 3000
	manifest, err := project.LoadManifest(projectDir)
	if err != nil {
		return err
	}
	if manifest != nil && manifest.Server != nil && manifest.Server.Port != 0 {
		port = manifest.Server.Port
	}
	dbPath, dbSource := project.ResolveDatabasePath(manifest, projectDir)
	if dbPath != "" {
		fmt.Printf("[mar] database: %s (from %s)\n", dbPath, dbSource)
		runtime.SetDBPath(dbPath)
	}

	// Wire auth runtime from the manifest. Without this, the
	// production binary boots with an empty session secret and
	// every /_auth/* request returns 503 — same handlers `mar dev`
	// uses, just never told what secret/SMTP to use. Resolving
	// happens here and only here for prod.
	secret, _, err := project.ResolveSessionSecret(manifest, projectDir)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if secret != "" {
		jsserve.SetAuthRuntime(secret, project.ToSMTPConfig(manifest))
		if manifest != nil && manifest.Mail != nil {
			jsserve.SetAdminMailFrom(manifest.Mail.From)
		}
	}
	jsserve.SetAdminBuildInfo("dev") // production fills this via ldflags later
	jsserve.SetAdminRequestBufferSize(project.ResolvedRecentRequestsSize(manifest))

	// Gateway rate limiter — always on, per-IP, configured via
	// mar.json["rateLimit"]. validateRateLimit already ran during
	// LoadManifest above; here we just resolve the policy. Rate is
	// converted from per-minute (operator-friendly) to per-second
	// (token-bucket internal unit).
	var rateLimitCfg *project.RateLimitConfig
	if manifest != nil {
		rateLimitCfg = manifest.RateLimit
	}
	jsserve.SetRateLimit(ratelimit.New(ratelimit.Policy{
		Rate:  float64(rateLimitCfg.ResolvedRequestsPerMinute()) / 60.0,
		Burst: rateLimitCfg.ResolvedBurst(),
	}))

	// Per-request body cap (see cmd/mar/main.go for the rationale).
	var serverCfg *project.ServerConfig
	if manifest != nil {
		serverCfg = manifest.Server
	}
	jsserve.SetMaxBodyBytes(serverCfg.ResolvedMaxBodyBytes())

	// Admin panel boot — schema + sync from mar.json["admins"]. Same
	// DB the user-auth uses; the _mar_admin_* tables coexist with
	// user entities under the reserved framework prefix.
	if dbPath != "" {
		db, dbErr := runtime.OpenDB()
		if dbErr != nil {
			return fmt.Errorf("admin panel: %w", dbErr)
		}
		// Acquire the exclusive advisory lock on the DB file, held
		// for the process lifetime. Blocks a second instance of the
		// runtime against the same DB, and signals the restore CLI
		// that the server is in use. Kernel releases on process exit
		// regardless of how — clean shutdown, SIGKILL, panic, etc.
		if err := runtime.HoldDBLock(dbPath); err != nil {
			if errors.Is(err, runtime.ErrDBLocked) {
				return fmt.Errorf("database %s is locked by another process (another mar-runtime instance, or restore-db in progress)", dbPath)
			}
			return fmt.Errorf("database lock: %w", err)
		}
		desired := canonicalAdmins(manifestAdmins(manifest))
		added, removed, err := admin.Boot(db, desired, time.Now().UnixMilli())
		if err != nil {
			return fmt.Errorf("admin panel: %w", err)
		}
		if added != 0 || removed != 0 {
			fmt.Printf("[mar] admin panel: synced %d admins (+%d -%d)\n", len(desired), added, removed)
		}
		if len(desired) == 0 {
			fmt.Fprintln(os.Stderr, "mar: admin panel locked (no admins in mar.json) — /_mar/admin will reject all logins.")
		}

		// Auto-backup scheduler — runs in the background for the
		// process lifetime. No-op when disabled or no DB. The
		// goroutine inherits this binary's stderr for status logs.
		//
		// Gets its OWN connection (not the app's single-connection
		// pool) so VACUUM INTO runs concurrently under WAL without
		// stalling request handlers.
		if backupDB, bErr := runtime.OpenSnapshotDB(dbPath); bErr == nil {
			admin.MaybeStartAutoBackup(
				context.Background(),
				backupDB, manifest, projectDir, dbPath, "dev",
			)
		}
	}

	lp := &jsserve.LiveProgram{}
	if manifest != nil && manifest.Name != "" {
		lp.SetAppName(manifest.Name)
	}
	// devMode stays false so the JS bundle skips dev affordances and
	// ServeLive (called with hub=nil below) doesn't register /_mar/reload.

	// Parse-only loader (no typecheck pass) — the embedded payload
	// was already type-checked at `mar build` time, so re-checking
	// every boot wastes startup AND pulls `internal/typecheck`
	// (~100 KB + crypto/fips140 init data) into the link graph for
	// no benefit. See LoadIntoEnvForRuntime's doc for the full
	// rationale.
	rEnv, mods, err := project.LoadIntoEnvForRuntime(entryFile,
		func(env *runtime.Env, mods []*ast.Module) {
			apphost.Install(env, mods, port, lp)
		})
	if err != nil {
		return err
	}

	mainVal, ok := project.LookupMain(rEnv, mods)
	if !ok {
		return fmt.Errorf("%s: no `main` exported", entryFile)
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return fmt.Errorf("main is not an Effect (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return err
	}
	if lp.Port() == 0 {
		return fmt.Errorf("main returned without invoking App.frontend / App.backend / App.fullstack")
	}

	// hub=nil: production server, no SSE / hot-reload endpoint.
	return jsserve.ServeLive(port, lp, nil, nil)
}

func resolveEntry(path string) (entryFile string, projectDir string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", "", statErr
	}
	if info.IsDir() {
		entry := filepath.Join(path, "Main.mar")
		if _, err := os.Stat(entry); err != nil {
			return "", "", fmt.Errorf("%s: no Main.mar found in directory", path)
		}
		return entry, path, nil
	}
	return path, filepath.Dir(path), nil
}
