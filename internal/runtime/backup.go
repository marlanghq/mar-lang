package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mar/internal/sqlitecli"
)

// BackupResult describes the generated backup file and rotated files.
type BackupResult struct {
	Path       string
	BackupDir  string
	Removed    []string
	KeptLast   int
	Database   string
	OccurredAt int64
}

// BackupFile describes an existing backup file on disk.
type BackupFile struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"sizeBytes"`
	CreatedAtMs int64  `json:"createdAtMs"`
	CreatedAt   string `json:"createdAt"`
}

// CreateSQLiteBackup creates a timestamped SQLite snapshot and rotates old backups.
func CreateSQLiteBackup(databasePath string, cfg sqlitecli.Config, keepLast int) (BackupResult, error) {
	if keepLast <= 0 {
		keepLast = 20
	}

	resolvedDatabasePath := resolveAbsolutePath(databasePath)
	baseName := filepath.Base(resolvedDatabasePath)
	prefix := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	if prefix == "" {
		prefix = "database"
	}

	backupDir := backupDirectory(databasePath)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return BackupResult{}, err
	}

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	backupPath := filepath.Join(backupDir, prefix+"-"+timestamp+".db")
	quotedPath := strings.ReplaceAll(backupPath, "'", "''")
	db := sqlitecli.OpenWithConfig(databasePath, cfg)
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO '" + quotedPath + "'"); err != nil {
		return BackupResult{}, fmt.Errorf("backup failed: %w", err)
	}

	removed, err := rotateBackups(backupDir, prefix, keepLast)
	if err != nil {
		return BackupResult{}, err
	}

	return BackupResult{
		Path:       backupPath,
		BackupDir:  backupDir,
		Removed:    removed,
		KeptLast:   keepLast,
		Database:   resolvedDatabasePath,
		OccurredAt: time.Now().UnixMilli(),
	}, nil
}

// ListSQLiteBackups lists existing backup files for a SQLite database, newest first.
func ListSQLiteBackups(databasePath string, limit int) ([]BackupFile, error) {
	resolvedDatabasePath := resolveAbsolutePath(databasePath)
	baseName := filepath.Base(resolvedDatabasePath)
	prefix := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	if prefix == "" {
		prefix = "database"
	}
	backupDir := backupDirectory(databasePath)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BackupFile{}, nil
		}
		return nil, err
	}

	pattern := prefix + "-"
	files := make([]BackupFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, pattern) || !strings.HasSuffix(name, ".db") {
			continue
		}

		fullPath := filepath.Join(backupDir, name)
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}

		timestamp := backupFileTimestamp(name, info.ModTime())
		files = append(files, BackupFile{
			Path:        fullPath,
			Name:        name,
			SizeBytes:   info.Size(),
			CreatedAtMs: timestamp.UnixMilli(),
			CreatedAt:   timestamp.Format("2006-01-02 15:04:05"),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].CreatedAtMs > files[j].CreatedAtMs
	})
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return files, nil
}

// FindSQLiteBackupByName finds a known backup for the given database by exact file name.
func FindSQLiteBackupByName(databasePath, fileName string) (BackupFile, bool, error) {
	cleanedName := strings.TrimSpace(fileName)
	if cleanedName == "" {
		return BackupFile{}, false, nil
	}
	if cleanedName != filepath.Base(cleanedName) {
		return BackupFile{}, false, nil
	}

	backups, err := ListSQLiteBackups(databasePath, 0)
	if err != nil {
		return BackupFile{}, false, err
	}
	for _, backup := range backups {
		if backup.Name == cleanedName {
			return backup, true, nil
		}
	}
	return BackupFile{}, false, nil
}

func backupDirectory(databasePath string) string {
	resolvedDatabasePath := resolveAbsolutePath(databasePath)
	return filepath.Join(filepath.Dir(resolvedDatabasePath), "backups")
}

func resolveAbsolutePath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return cwd
	}
	if filepath.IsAbs(cleaned) {
		return filepath.Clean(cleaned)
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return filepath.Clean(cleaned)
	}
	return filepath.Clean(abs)
}

func backupFileTimestamp(fileName string, fallback time.Time) time.Time {
	trimmed := strings.TrimSuffix(fileName, ".db")
	parts := strings.Split(trimmed, "-")
	if len(parts) == 0 {
		return fallback
	}
	raw := parts[len(parts)-1]
	if ts, err := time.Parse("20060102T150405Z", raw); err == nil {
		return ts.Local()
	}
	return fallback
}

func rotateBackups(backupDir, prefix string, keepLast int) ([]string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, err
	}

	pattern := prefix + "-"
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, pattern) && strings.HasSuffix(name, ".db") {
			files = append(files, name)
		}
	}
	if len(files) <= keepLast {
		return []string{}, nil
	}

	sort.Strings(files)
	removeCount := len(files) - keepLast
	removed := make([]string, 0, removeCount)
	for i := 0; i < removeCount; i++ {
		path := filepath.Join(backupDir, files[i])
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	return removed, nil
}
