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
	if err := runEmbeddedOrArg(); err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		os.Exit(1)
	}
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
// We stamp MAR_DEV_LAUNCH_CWD with the user's startup cwd BEFORE
// extracting, so relative paths in mar.json (e.g. `./notes.db`) still
// resolve against the directory the user launched the binary from, not
// against the temp extraction dir. Without that, the SQLite file would
// land inside `/tmp/mar-runtime-xxx/` and disappear at the next OS
// cleanup — silently destroying production data on every restart.
func runEmbedded(b *appbundle.Bundle) error {
	if _, set := os.LookupEnv("MAR_DEV_LAUNCH_CWD"); !set {
		if cwd, err := os.Getwd(); err == nil {
			os.Setenv("MAR_DEV_LAUNCH_CWD", cwd)
		}
	}
	tmp, err := os.MkdirTemp("", "mar-runtime-*")
	if err != nil {
		return err
	}
	// Best-effort cleanup. We don't defer if exec.Run blocks the process
	// inside ListenAndServe — but ListenAndServe never returns cleanly
	// in the success path, so cleanup happens via the OS on exit.
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

	// Admin panel boot — schema + sync from mar.json["admins"]. Same
	// DB the user-auth uses; the _mar_admin_* tables coexist with
	// user entities under the reserved framework prefix.
	if dbPath != "" {
		db, dbErr := runtime.OpenDB()
		if dbErr != nil {
			return fmt.Errorf("admin panel: %w", dbErr)
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
	}

	lp := &jsserve.LiveProgram{}
	if manifest != nil && manifest.Name != "" {
		lp.SetAppName(manifest.Name)
	}
	// devMode stays false so the JS bundle skips dev affordances and
	// ServeLive (called with hub=nil below) doesn't register /_mar/reload.

	rEnv, _, _, err := project.LoadIntoEnvWithModulesAndHook(entryFile,
		func(env *runtime.Env, mods []*ast.Module) {
			apphost.Install(env, mods, port, lp)
		})
	if err != nil {
		return err
	}

	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, ok = rEnv.Lookup("main")
	}
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
	return jsserve.ServeLive(port, lp, nil)
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
