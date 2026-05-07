// `mar fly backup` — pull a restorable bundle from the running Fly
// machine into ./backups/<app>-<timestamp>.tar.gz.
//
// Three steps over fly's SSH:
//
//   1. fly ssh console -C "mar-runtime backup /tmp/mar-backup.tar.gz"
//        - Runs the backup subcommand on the live machine (VACUUM
//          INTO + tarball with metadata.json + mar.json + mar.db).
//   2. fly ssh sftp get /tmp/mar-backup.tar.gz <local>
//        - Pulls the bundle down.
//   3. fly ssh console -C "rm -f /tmp/mar-backup.tar.gz"
//        - Best-effort cleanup so the file doesn't linger on the
//          machine. Failure is non-fatal — local copy already
//          succeeded.
//
// The bundle path on the remote is fixed (/tmp/mar-backup.tar.gz)
// because (a) /tmp is reliably writable on Debian-slim, (b) we
// always overwrite, and (c) the caller doesn't need the path.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	flyRemoteBackupPath = "/tmp/mar-backup.tar.gz"
)

// runFlyBackup orchestrates the SSH dance and writes the bundle to
// ./backups/<app>-YYYY-MM-DD-HHMMSS.tar.gz. Returns 0 on success.
func runFlyBackup(path string) int {
	cfg, err := loadFlyConfig(path)
	if err != nil {
		fprintError("mar fly backup: %v", err)
		return 1
	}
	if _, err := requireFlyCLI(); err != nil {
		fprintError("%v", err)
		return 1
	}
	if err := ensureFlyAppExists(cfg.AppName); err != nil {
		fprintError("mar fly backup: %v", err)
		return 1
	}

	// Local destination: ./backups/<app>-<timestamp>.tar.gz.
	// Timestamp uses the operator's local time for human-friendly
	// filenames; the bundle's metadata.json carries UTC for parsing.
	localDir := "backups"
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		fprintError("mar fly backup: create %s: %v", colorMagenta(localDir), err)
		return 1
	}
	timestamp := time.Now().Format("2006-01-02-150405")
	localPath := filepath.Join(localDir, fmt.Sprintf("%s-%s.tar.gz", cfg.AppName, timestamp))

	fmt.Println()
	fmt.Println("mar fly backup: creating consistent snapshot…")
	fmt.Println()

	// Step 1: run mar-runtime backup on the machine.
	fmt.Printf("  → %s\n", colorMagenta("fly ssh console: mar-runtime backup"))
	produceCmd := exec.Command("fly", "ssh", "console",
		"--app", cfg.AppName,
		"-C", fmt.Sprintf("mar-runtime backup %s", flyRemoteBackupPath),
	)
	if out, err := produceCmd.CombinedOutput(); err != nil {
		fprintError("mar fly backup: producing snapshot on machine failed:\n%s", out)
		return 1
	}

	// Step 2: pull it down via sftp.
	fmt.Printf("  → %s\n", colorMagenta("fly ssh sftp get"))
	getCmd := exec.Command("fly", "ssh", "sftp", "get",
		"--app", cfg.AppName,
		flyRemoteBackupPath, localPath,
	)
	if out, err := getCmd.CombinedOutput(); err != nil {
		fprintError("mar fly backup: download failed:\n%s", out)
		// Still try cleanup before giving up.
		_ = exec.Command("fly", "ssh", "console", "--app", cfg.AppName,
			"-C", "rm -f "+flyRemoteBackupPath).Run()
		return 1
	}

	// Step 3: best-effort cleanup. Don't fail the command if this
	// errors — the local copy is already saved.
	fmt.Printf("  → %s\n", colorMagenta("fly ssh console: rm /tmp snapshot"))
	_ = exec.Command("fly", "ssh", "console", "--app", cfg.AppName,
		"-C", "rm -f "+flyRemoteBackupPath).Run()

	// Report what landed.
	fmt.Println()
	fi, _ := os.Stat(localPath)
	var size string
	if fi != nil {
		size = fmt.Sprintf(" (%s)", humanBytes(fi.Size()))
	}
	fmt.Printf("backup saved: %s%s\n", colorGreen(localPath), size)

	// Surface metadata so the operator sees what was captured + which
	// secrets they'll need on restore. Best-effort — bundle parse
	// failure shouldn't fail the command.
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

// bundleMetadata mirrors backupMetadata from cmd/mar-runtime.
// Defined locally so cmd/mar doesn't depend on cmd/mar-runtime.
type bundleMetadata struct {
	CreatedAtUnixMs  int64    `json:"createdAtUnixMs"`
	CreatedAtRFC3339 string   `json:"createdAtRFC3339"`
	MarVersion       string   `json:"marVersion"`
	BuildTarget      string   `json:"buildTarget"`
	AppName          string   `json:"appName"`
	EnvRefs          []string `json:"envRefs"`
}

// readBundleMetadata extracts metadata.json from the tarball
// without touching the rest. tar -O streams the file content
// to stdout, gunzip handles the .gz wrapper. Reuses the system
// tar binary (always present on macOS / Linux) instead of pulling
// in archive/tar handling here — this is a one-shot informational
// read, not a hot path.
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

// humanBytes formats a byte count as a human-readable string with
// units. Mirrors the formatBytes used in admin.js but for Go output.
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
