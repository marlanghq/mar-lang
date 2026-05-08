// `mar fly database` — operations against the production database.
//
// Three verbs:
//
//   mar fly database backup
//       Force a snapshot now. Lands in the on-volume catalog at
//       /data/backups/<timestamp>.tar.gz. Same shape as automatic
//       backups produced by the runtime's scheduler.
//
//   mar fly database backups
//       List the catalog (newest first).
//
//   mar fly database backup download <id>
//       Pull a snapshot from the catalog to ./backups/<id>.tar.gz
//       locally. Useful for cold storage / local archive.
//
// Restore lives in the admin panel UI (/_mar/admin) — it requires
// a server-side schema match check + machine restart, which is
// hard to express tersely in CLI output. The CLI complement is
// kept narrow on purpose.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const flyDatabaseUsage = `Usage: mar fly database <command> [args]

Commands:
  backup                   Force a snapshot now. Lands in the
                           production catalog (same place as
                           auto-backups).
  backups                  List the catalog (newest first).
  backup download <id>     Pull a backup from the catalog to
                           ./backups/<id>.tar.gz locally.

Restore is in the admin panel UI: /_mar/admin → Database backups.`

// runFlyDatabase dispatches `mar fly database <sub>`.
func runFlyDatabase(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, flyDatabaseUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "backup":
		// `mar fly database backup` (snapshot)
		// `mar fly database backup download <id>` (pull)
		if len(args) >= 2 && args[1] == "download" {
			if len(args) < 3 {
				fprintError("mar fly database backup download: missing <id>")
				return 2
			}
			return runFlyDatabaseDownload(args[2], "")
		}
		return runFlyDatabaseBackup(".")
	case "backups":
		return runFlyDatabaseList(".")
	default:
		fprintError("mar fly database: unknown subcommand %q", args[0])
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, flyDatabaseUsage)
		return 2
	}
}

// flyRemoteCatalogDir is where the catalog lives on the Fly volume.
// Derived from the convention `<dir of mar.db>/backups/`. We hard-
// code `/data/backups` because the Dockerfile mounts the Fly volume
// at /data, and `database.path` defaults to `/data/mar.db`. Users
// who pin `database.path` elsewhere will hit a different catalog
// location; this CLI assumes the default. (Restore via the panel
// derives the path dynamically and is correct for any setup.)
const flyRemoteCatalogDir = "/data/backups"

// runFlyDatabaseBackup forces a snapshot on the running machine.
// The snapshot lands in the production catalog (alongside auto-
// backups) — no local download. Use `backup download <id>` if you
// want a local copy.
func runFlyDatabaseBackup(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly database backup: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly database backup: %v", err)
		return 1
	}

	// Compute the ID locally — the SSH subshell doesn't have a
	// reliably-portable way to format UTC.
	id := time.Now().UTC().Format("2006-01-02-150405")
	remotePath := flyRemoteCatalogDir + "/" + id + ".tar.gz"

	fmt.Println()
	fmt.Println("mar fly database backup: forcing a snapshot…")
	fmt.Println()

	cmd := exec.Command("fly", "ssh", "console",
		"--app", cfg.AppName,
		"-C", fmt.Sprintf("mkdir -p %s && mar-runtime backup %s",
			flyRemoteCatalogDir, remotePath),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fprintError("mar fly database backup: snapshot failed:\n%s", out)
		return 1
	}

	fmt.Printf("snapshot created in catalog: %s\n", colorCyan(id))
	fmt.Println()
	fmt.Printf("Pull a local copy with: %s\n",
		colorGreen(fmt.Sprintf("mar fly database backup download %s", id)))
	fmt.Println()
	return 0
}

// runFlyDatabaseList shells `ls` on the remote catalog dir, parses
// the output, and prints a tabular view.
func runFlyDatabaseList(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly database backups: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly database backups: %v", err)
		return 1
	}

	cmd := exec.Command("fly", "ssh", "console",
		"--app", cfg.AppName,
		"-C", fmt.Sprintf(
			"if [ -d %s ]; then ls -1 %s | sort -r; else echo '__NO_CATALOG__'; fi",
			flyRemoteCatalogDir, flyRemoteCatalogDir),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fprintError("mar fly database backups: %v\n%s", err, out)
		return 1
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "__NO_CATALOG__" {
		fmt.Println()
		fmt.Printf("No backup catalog yet on %s.\n", colorCyan(cfg.AppName))
		fmt.Println()
		fmt.Printf("Run %s to create the first backup, or wait for the auto-backup goroutine.\n",
			colorGreen("mar fly database backup"))
		fmt.Println()
		return 0
	}

	fmt.Println()
	fmt.Printf("backup catalog on %s (newest first):\n", colorCyan(cfg.AppName))
	fmt.Println()
	any := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, ".tar.gz") {
			continue
		}
		id := strings.TrimSuffix(line, ".tar.gz")
		fmt.Printf("  %s\n", colorCyan(id))
		any = true
	}
	if !any {
		fmt.Printf("  %s\n", colorYellow("(empty)"))
	}
	fmt.Println()
	return 0
}

// runFlyDatabaseDownload pulls a single backup from the catalog to
// ./backups/<id>.tar.gz locally.
func runFlyDatabaseDownload(id, projectPath string) int {
	if projectPath == "" {
		projectPath = "."
	}
	cfg, err := loadFlyConfig(projectPath)
	if err != nil {
		fprintError("mar fly database backup download: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly database backup download: %v", err)
		return 1
	}

	localDir := "backups"
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		fprintError("mar fly database backup download: create %s: %v", colorMagenta(localDir), err)
		return 1
	}
	localPath := filepath.Join(localDir, id+".tar.gz")
	remotePath := flyRemoteCatalogDir + "/" + id + ".tar.gz"

	fmt.Println()
	fmt.Printf("mar fly database backup download: pulling %s…\n", colorCyan(id))
	fmt.Println()

	get := exec.Command("fly", "ssh", "sftp", "get",
		"--app", cfg.AppName,
		remotePath, localPath,
	)
	if out, err := get.CombinedOutput(); err != nil {
		fprintError("mar fly database backup download: %v\n%s", err, out)
		return 1
	}

	fi, _ := os.Stat(localPath)
	var size string
	if fi != nil {
		size = fmt.Sprintf(" (%s)", humanBytes(fi.Size()))
	}
	fmt.Printf("saved: %s%s\n", colorGreen(localPath), size)

	if meta, err := readBundleMetadata(localPath); err == nil {
		fmt.Println()
		fmt.Printf("  app:           %s\n", colorCyan(meta.AppName))
		fmt.Printf("  built with:    mar %s (%s)\n",
			colorCyan(meta.MarVersion), meta.BuildTarget)
		fmt.Printf("  created at:    %s\n", meta.CreatedAtRFC3339)
		if len(meta.EnvRefs) > 0 {
			fmt.Println()
			fmt.Println("  required secrets to restore (set via `fly secrets set`):")
			for _, name := range meta.EnvRefs {
				fmt.Printf("    %s\n", colorMagenta(name))
			}
		}
	}
	fmt.Println()
	return 0
}

// bundleMetadata mirrors admin.SnapshotMetadata. Defined locally
// so cmd/mar doesn't pull internal/admin into its build.
type bundleMetadata struct {
	CreatedAtUnixMs   int64    `json:"createdAtUnixMs"`
	CreatedAtRFC3339  string   `json:"createdAtRFC3339"`
	MarVersion        string   `json:"marVersion"`
	BuildTarget       string   `json:"buildTarget"`
	AppName           string   `json:"appName"`
	EnvRefs           []string `json:"envRefs"`
	SchemaFingerprint string   `json:"schemaFingerprint"`
}

// readBundleMetadata extracts metadata.json from a downloaded
// tarball. Uses the system tar binary — present on macOS / Linux
// without extra deps. Best-effort; failures are non-fatal.
func readBundleMetadata(path string) (*bundleMetadata, error) {
	out, err := exec.Command("tar", "-xzOf", path, "metadata.json").Output()
	if err != nil {
		return nil, err
	}
	var m bundleMetadata
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// humanBytes formats a byte count for the success line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
