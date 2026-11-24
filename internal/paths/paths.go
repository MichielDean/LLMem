// Package paths provides path resolution, validation, and migration for LLMem.
// All hardcoded paths use ~/.config/llmem/ (with LMEM_HOME override).
// Backward compatibility: ~/.lobsterdog/ is checked first for migration.
package paths

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxPathDepth limits path component count to prevent traversal attacks.
const maxPathDepth = 10

// blockedSystemPrefixes lists system directories that should never be used
// as LLMem data locations. Shared between ValidateHomePath and
// ValidateWritePath to prevent DRY violations.
var blockedSystemPrefixes = []string{
	"/etc",
	"/var",
	"/sys",
	"/proc",
	"/dev",
	"/boot",
	"/root",
	"/sbin",
	"/bin",
	"/usr/sbin",
	"/usr/bin",
}

// fmtErr wraps an error with the "llmem: paths:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: paths: "+format, args...)
}

// GetHomeDir returns the base directory for all LLMem data.
// Resolution order:
// 1. LMEM_HOME env var (if set and non-empty after stripping)
// 2. ~/.config/llmem/ (if it exists)
// 3. ~/.lobsterdog/ (backward compat — if it exists and ~/.config/llmem/ doesn't)
// 4. ~/.config/llmem/ (default, even if it doesn't exist yet)
//
// Creates the directory with 0700 permissions if it doesn't exist.
func GetHomeDir() string {
	envVal := ""
	if v, ok := os.LookupEnv("LMEM_HOME"); ok {
		envVal = strings.TrimSpace(v)
	}
	if envVal != "" {
		resolved, err := ValidateHomePath(filepath.Clean(envVal), "LMEM_HOME")
		if err != nil {
			slog.Error("llmem: paths: invalid LMEM_HOME", "error", err)
			// Fall through to default
		} else {
			os.MkdirAll(resolved, 0700)
			return resolved
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Last resort
		return "/tmp/llmem"
	}

	newPath := filepath.Join(homeDir, ".config", "llmem")
	oldPath := filepath.Join(homeDir, ".lobsterdog")

	// If new path exists, use it
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	// If old path exists, use it (backward compat)
	if _, err := os.Stat(oldPath); err == nil {
		slog.Info("llmem: paths: using legacy ~/.lobsterdog/ directory; run MigrateFromLobsterdog() to move to ~/.config/llmem/")
		return oldPath
	}

	return newPath
}

// GetDBPath returns the path to the memory database (~/.config/llmem/memory.db).
func GetDBPath() string {
	return filepath.Join(GetHomeDir(), "memory.db")
}

// GetConfigPath returns the path to config.yaml (~/.config/llmem/config.yaml).
func GetConfigPath() string {
	return filepath.Join(GetHomeDir(), "config.yaml")
}

// GetSkillDir returns the path to the skills directory (~/.config/llmem/skills/).
// Checks LMEM_HOME env var override, same as GetHomeDir().
func GetSkillDir() string {
	return filepath.Join(GetHomeDir(), "skills")
}

// GetDirFromPath returns the parent directory of a file path.
// Returns empty string if the path has no directory component.
func GetDirFromPath(path string) string {
	return filepath.Dir(path)
}

// IsBlockedPath returns true if the resolved path targets a protected system directory.
// Uses prefix + '/' matching to avoid false positives — e.g. '/bin'
// should not match '/binary_search/data.db', but '/bin/' should.
func IsBlockedPath(resolved string) bool {
	for _, prefix := range blockedSystemPrefixes {
		if resolved == prefix || strings.HasPrefix(resolved, prefix+"/") {
			return true
		}
	}
	return false
}

// ValidateHomePath validates that a home path is safe to use.
// Checks (in order):
// - Must not contain '..' traversal components
// - Must not target sensitive system directories
// - Must not be a symlink itself
// - Must not exceed reasonable path depth
//
// Returns the resolved, validated path.
func ValidateHomePath(path, source string) (string, error) {
	// Check traversal BEFORE resolving (resolve eliminates ..)
	if strings.Contains(filepath.Clean(path), "..") {
		return "", fmtErr("%s contains '..' traversal: %s", source, path)
	}

	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmtErr("%s: resolve path: %w", source, err)
	}

	// Block system directories
	if IsBlockedPath(resolved) {
		return "", fmtErr("%s targets a system directory: %s", source, resolved)
	}

	// Must not be a symlink itself (prevents symlink escalation)
	// Use Lstat to not follow symlinks
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmtErr("%s is a symlink (not allowed): %s", source, path)
		}
	} else if !os.IsNotExist(err) {
		// Permission denied or other stat error
		return "", fmtErr("%s cannot be accessed: %w", source, err)
	}

	// Must not exceed a reasonable path depth
	parts := strings.Split(resolved, string(filepath.Separator))
	// Filter empty parts (leading /)
	nonEmpty := 0
	for _, p := range parts {
		if p != "" {
			nonEmpty++
		}
	}
	if nonEmpty > maxPathDepth {
		return "", fmtErr("%s path too deep (%d components): %s", source, nonEmpty, resolved)
	}

	return resolved, nil
}

// ValidateWritePath validates that a write target path is safe.
// Same checks as ValidateHomePath but for write targets.
// Does NOT require the path to be within the LLMem home directory —
// users may configure custom output paths.
func ValidateWritePath(path, source string) (string, error) {
	// Check traversal BEFORE resolving
	if strings.Contains(filepath.Clean(path), "..") {
		return "", fmtErr("%s path contains '..' traversal: %s", source, path)
	}

	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmtErr("%s: resolve path: %w", source, err)
	}

	// Block system directories
	if IsBlockedPath(resolved) {
		return "", fmtErr("%s path targets a protected directory: %s", source, resolved)
	}

	// Must not be a symlink itself
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmtErr("%s path is a symlink (not allowed for write targets): %s", source, path)
		}
	} else if !os.IsNotExist(err) {
		return "", fmtErr("%s path cannot be accessed: %w", source, err)
	}

	return resolved, nil
}

// ValidateSessionID validates that a session ID is safe to use in filesystem paths.
// Rejects session IDs that contain path separators or traversal sequences.
func ValidateSessionID(sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmtErr("session_id must not be empty")
	}
	if strings.Contains(sessionID, "/") {
		return "", fmtErr("session_id contains '/' (path traversal risk): %q", sessionID)
	}
	if strings.Contains(sessionID, "\\") {
		return "", fmtErr("session_id contains '\\' (path traversal risk): %q", sessionID)
	}
	if strings.Contains(sessionID, "..") {
		return "", fmtErr("session_id contains '..' (path traversal risk): %q", sessionID)
	}
	return sessionID, nil
}

// MigrateFromLobsterdog migrates data from ~/.lobsterdog/ to ~/.config/llmem/.
// Copies config.yaml, memory.db,
// and context/ directory from ~/.lobsterdog/ to ~/.config/llmem/
// only if the source exists and the destination doesn't.
// Never deletes the source directory.
//
// Returns (true, nil) if any migration was performed, (false, nil) if no migration needed.
func MigrateFromLobsterdog() (bool, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, fmtErr("get home directory: %w", err)
	}

	oldHome := filepath.Join(homeDir, ".lobsterdog")
	newHome := filepath.Join(homeDir, ".config", "llmem")

	// Resolve to avoid symlink attacks
	oldHome, err = filepath.Abs(oldHome)
	if err != nil {
		return false, fmtErr("resolve old home: %w", err)
	}
	newHome, err = filepath.Abs(newHome)
	if err != nil {
		return false, fmtErr("resolve new home: %w", err)
	}

	if _, err := os.Stat(oldHome); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := os.Stat(newHome); err == nil {
		return false, nil
	}

	migrated := false
	if err := os.MkdirAll(newHome, 0700); err != nil {
		return false, fmtErr("create new home directory: %w", err)
	}

	filesToCopy := []string{
		"config.yaml",
		"memory.db",
	}

	for _, filename := range filesToCopy {
		src := filepath.Join(oldHome, filename)
		dst := filepath.Join(newHome, filename)

		// Skip symlinks — only copy regular files
		if info, err := os.Lstat(src); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				slog.Warn("llmem: paths: skipping symlink during migration", "path", src)
				continue
			}
		}

		if _, err := os.Stat(src); err != nil {
			continue // Source doesn't exist
		}
		if _, err := os.Stat(dst); err == nil {
			continue // Destination already exists
		}

		if err := copyFile(src, dst); err != nil {
			slog.Warn("llmem: paths: failed to migrate file", "file", filename, "error", err)
			continue
		}
		slog.Info("llmem: paths: migrated file from ~/.lobsterdog/ to ~/.config/llmem/", "file", filename)
		migrated = true
	}

	// Copy context/ directory — skip symlinks
	srcContext := filepath.Join(oldHome, "context")
	dstContext := filepath.Join(newHome, "context")

	if info, err := os.Lstat(srcContext); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			slog.Warn("llmem: paths: skipping symlink directory during migration", "path", srcContext)
		} else if info.IsDir() {
			if _, err := os.Stat(dstContext); os.IsNotExist(err) {
				if err := copyDir(srcContext, dstContext); err != nil {
					slog.Warn("llmem: paths: failed to migrate context/ directory", "error", err)
				} else {
					slog.Info("llmem: paths: migrated context/ directory from ~/.lobsterdog/ to ~/.config/llmem/")
					migrated = true
				}
			}
		}
	}

	return migrated, nil
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmtErr("read source file: %w", err)
	}
	return os.WriteFile(dst, data, 0600)
}

// copyDir copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, 0700)
		}

		return copyFile(path, targetPath)
	})
}