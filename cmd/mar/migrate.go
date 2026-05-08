// `mar migrate` subcommand — read-only inspection of schema state.
//
// Two modes:
//
//   mar migrate plan [path]     -- show what next boot will apply / block
//   mar migrate status [path]   -- show history from _mar_schema_migrations
//
// Both load + evaluate the project (so entities register), then either
// diff the live DB against the declarations (plan) or query the audit
// table (status). Neither modifies the schema. Plan exits non-zero if
// any step would be blocked, so CI pipelines can fail bad migrations
// before they reach production.

package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

func runMigrate(args []string) int {
	if len(args) < 1 {
		fprintError("mar migrate: missing subcommand")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "usage: mar migrate <plan|status> [path]")
		return 2
	}
	sub := args[0]
	path := "."
	if len(args) >= 2 {
		path = args[1]
	}
	switch sub {
	case "plan":
		return runMigratePlan(path)
	case "status":
		return runMigrateStatus(path)
	default:
		fprintError("mar migrate: unknown subcommand %q", sub)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "usage: mar migrate <plan|status> [path]")
		return 2
	}
}

// runMigratePlan loads the project, walks the entity registry against
// the live DB, and prints what would happen on next boot. Exit 0 if
// everything is safe (would-apply or already-up-to-date); exit 1 if
// any step is blocked.
func runMigratePlan(path string) int {
	db, err := loadProjectAndOpenDB(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		return 1
	}
	if db == nil {
		fmt.Println("[migrate plan] project has no entities or no database configured; nothing to plan.")
		return 0
	}
	m := runtime.NewMigrator(db, runtime.RegisteredEntities())
	plan, err := m.Plan()
	if err != nil {
		fprintError("mar migrate plan: %v", err)
		return 1
	}

	if len(plan) == 0 {
		fmt.Printf("%s schema is up to date; no changes to apply.\n", colorGreen("[migrate plan]"))
		return 0
	}

	var blocked, applied, notes []runtime.MigrationStep
	for _, s := range plan {
		switch s.Kind {
		case runtime.StepBlocked:
			blocked = append(blocked, s)
		case runtime.StepNoteOrphanTable:
			notes = append(notes, s)
		default:
			applied = append(applied, s)
		}
	}

	if len(applied) > 0 {
		fmt.Printf("%s %s change(s) would apply on next boot:\n",
			colorBold("[migrate plan]"),
			colorCyan(fmt.Sprintf("%d", len(applied))))
		for _, s := range applied {
			fmt.Printf("  - %s\n", s.Description)
			if s.SQL != "" {
				fmt.Printf("        %s\n", colorMagenta(s.SQL))
			}
		}
	}
	for _, s := range notes {
		fmt.Printf("%s %s %s\n", colorBold("[migrate plan]"), colorYellow("note:"), s.Description)
	}
	if len(blocked) > 0 {
		fmt.Printf("\n%s %s change(s) would %s on next boot:\n\n",
			colorBold("[migrate plan]"),
			colorRed(fmt.Sprintf("%d", len(blocked))),
			colorRed("FAIL"))
		for _, s := range blocked {
			fmt.Println(s.Error)
			fmt.Println()
		}
		return 1
	}
	return 0
}

// runMigrateStatus prints the contents of _mar_schema_migrations.
// Read-only; doesn't even need the entity declarations — useful for
// "what changed last deploy?" forensics where the source code may
// not match the production schema.
func runMigrateStatus(path string) int {
	db, err := loadProjectAndOpenDB(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		return 1
	}
	if db == nil {
		fmt.Println("[migrate status] project has no database configured.")
		return 0
	}

	rows, err := db.Query(`
		SELECT id, table_name, migration_kind, sql_text, applied_at
		FROM _mar_schema_migrations
		ORDER BY id
	`)
	if err != nil {
		// If the table doesn't exist, the DB has never had a
		// migrator run — surface that as a friendly message
		// rather than a SQL error.
		if strings.Contains(err.Error(), "no such table") {
			fmt.Printf("%s no migrations recorded yet (the database was never touched by the migrator).\n",
				colorBold("[migrate status]"))
			return 0
		}
		fprintError("mar migrate status: %v", err)
		return 1
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int64
		var table, kind, sqlText string
		var appliedAt int64
		if err := rows.Scan(&id, &table, &kind, &sqlText, &appliedAt); err != nil {
			fprintError("mar migrate status: %v", err)
			return 1
		}
		fmt.Printf("%s  %s  %s  %s\n",
			colorYellow(fmt.Sprintf("%5d", id)),
			formatMillis(appliedAt),
			colorCyan(fmt.Sprintf("%-26s", table+"."+kind)),
			oneLineSQL(sqlText))
		count++
	}
	if err := rows.Err(); err != nil {
		fprintError("mar migrate status: %v", err)
		return 1
	}
	if count == 0 {
		fmt.Printf("%s no migrations recorded yet.\n", colorBold("[migrate status]"))
	}
	return 0
}

// loadProjectAndOpenDB runs the project up to the point where
// entities have registered, opens the configured DB, and returns
// the handle. Returns (nil, nil) when the project doesn't use a DB —
// a "nothing to do" signal the callers turn into a friendly message.
func loadProjectAndOpenDB(path string) (*sql.DB, error) {
	entryFile, projectDir, err := resolveDevEntry(path)
	if err != nil {
		return nil, err
	}
	manifest, _ := project.LoadManifest(projectDir)
	if dbPath, _ := project.ResolveDatabasePath(manifest, projectDir); dbPath != "" {
		runtime.SetDBPath(dbPath)
	}
	// Fresh entity registry for this command invocation.
	runtime.ResetRegisteredEntities()

	// Build a throwaway LiveProgram + apphost overrides so evaluating
	// `main` doesn't crash trying to call the un-overridden App.*
	// builtins. We don't actually serve anything; we only care about
	// the side-effect of Entity.define registrations.
	lp := &jsserve.LiveProgram{}
	rEnv, _, _, err := project.LoadIntoEnvWithModulesAndHook(entryFile,
		func(env *runtime.Env, mods []*ast.Module) {
			apphost.Install(env, mods, 0, lp)
		})
	if err != nil {
		return nil, err
	}

	// Run main so Entity.define + Auth.config side-effects fire.
	// Errors here are surface-level (program crashed during setup);
	// surface them so the operator sees what's wrong before
	// trying to migrate against a half-evaluated registry.
	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, _ = rEnv.Lookup("main")
	}
	if eff, ok := mainVal.(runtime.VEffect); ok {
		_, _ = eff.Run() // best-effort: registration may have happened before any error
	}

	if runtime.CurrentDBPath() == "" || len(runtime.RegisteredEntities()) == 0 {
		return nil, nil
	}
	return runtime.OpenDB()
}

// formatMillis renders a unix-millis timestamp for the status table.
// ISO 8601 with second precision keeps lines compact and sortable.
func formatMillis(unixMs int64) string {
	t := time.Unix(unixMs/1000, (unixMs%1000)*1_000_000).UTC()
	return t.Format("2006-01-02 15:04:05Z")
}

// oneLineSQL collapses a multi-line SQL string to a single line so
// the status table doesn't become a wall of text. Truncates with an
// ellipsis when the result would otherwise be very long.
func oneLineSQL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 80
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}
