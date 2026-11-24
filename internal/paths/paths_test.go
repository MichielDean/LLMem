package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBlockedPath_SystemDirs(t *testing.T) {
	blocked := []string{
		"/etc", "/etc/passwd", "/etc/llmem/config.yaml",
		"/var", "/var/lib/data",
		"/sys", "/sys/kernel",
		"/proc", "/proc/self",
		"/dev", "/dev/null",
		"/boot", "/boot/vmlinuz",
		"/root", "/root/.bashrc",
		"/sbin", "/sbin/init",
		"/bin", "/bin/bash",
		"/usr/sbin", "/usr/sbin/sshd",
		"/usr/bin", "/usr/bin/python3",
	}
	for _, p := range blocked {
		if !IsBlockedPath(p) {
			t.Errorf("expected %q to be blocked", p)
		}
	}
}

func TestIsBlockedPath_UserDirs(t *testing.T) {
	safe := []string{
		"/home/user/.config/llmem",
		"/tmp/llmem",
		"/binary_search/data.db",
		"/usr/local/llmem",
	}
	for _, p := range safe {
		if IsBlockedPath(p) {
			t.Errorf("expected %q to be safe, but it was blocked", p)
		}
	}
}

func TestIsBlockedPath_ExactMatch(t *testing.T) {
	// "/bin" should be blocked as exact match
	if !IsBlockedPath("/bin") {
		t.Error("expected /bin to be blocked")
	}
	// "/binary_search" should NOT be blocked (no trailing /)
	if IsBlockedPath("/binary_search") {
		t.Error("expected /binary_search to NOT be blocked")
	}
}

func TestValidateHomePath_TraversalReject(t *testing.T) {
	_, err := ValidateHomePath("/foo/../etc/passwd", "test")
	if err == nil {
		t.Error("expected error for path with .. traversal")
	}
}

func TestValidateHomePath_BlockedDir(t *testing.T) {
	_, err := ValidateHomePath("/etc/llmem", "test")
	if err == nil {
		t.Error("expected error for blocked system directory")
	}
}

func TestValidateHomePath_ValidPath(t *testing.T) {
	dir := t.TempDir()
	resolved, err := ValidateHomePath(dir, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestValidateWritePath_TraversalReject(t *testing.T) {
	_, err := ValidateWritePath("/foo/../etc/passwd", "test")
	if err == nil {
		t.Error("expected error for path with .. traversal")
	}
}

func TestValidateWritePath_BlockedDir(t *testing.T) {
	_, err := ValidateWritePath("/var/log/llmem", "test")
	if err == nil {
		t.Error("expected error for blocked system directory")
	}
}

func TestValidateWritePath_ValidPath(t *testing.T) {
	dir := t.TempDir()
	resolved, err := ValidateWritePath(dir, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestValidateSessionID_Empty(t *testing.T) {
	_, err := ValidateSessionID("")
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

func TestValidateSessionID_Slash(t *testing.T) {
	_, err := ValidateSessionID("abc/def")
	if err == nil {
		t.Error("expected error for session ID with /")
	}
}

func TestValidateSessionID_Backslash(t *testing.T) {
	_, err := ValidateSessionID("abc\\def")
	if err == nil {
		t.Error("expected error for session ID with backslash")
	}
}

func TestValidateSessionID_Traversal(t *testing.T) {
	_, err := ValidateSessionID("abc..def")
	if err == nil {
		t.Error("expected error for session ID with ..")
	}
}

func TestValidateSessionID_Valid(t *testing.T) {
	id, err := ValidateSessionID("session-123-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "session-123-abc" {
		t.Errorf("expected 'session-123-abc', got %q", id)
	}
}

func TestGetHomeDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LMEM_HOME", dir)
	home := GetHomeDir()
	if home != dir {
		t.Errorf("expected %q, got %q", dir, home)
	}
}

func TestGetDBPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LMEM_HOME", dir)
	dbPath := GetDBPath()
	expected := filepath.Join(dir, "memory.db")
	if dbPath != expected {
		t.Errorf("expected %q, got %q", expected, dbPath)
	}
}

func TestGetConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LMEM_HOME", dir)
	configPath := GetConfigPath()
	expected := filepath.Join(dir, "config.yaml")
	if configPath != expected {
		t.Errorf("expected %q, got %q", expected, configPath)
	}
}

func TestGetSkillDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LMEM_HOME", dir)
	path := GetSkillDir()
	expected := filepath.Join(dir, "skills")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestMigrateFromLobsterdog_NoOldDir(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	migrated, err := MigrateFromLobsterdog()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if migrated {
		t.Error("expected no migration when old dir doesn't exist")
	}
}

func TestMigrateFromLobsterdom_Success(t *testing.T) {
	homeDir := t.TempDir()

	oldDir := filepath.Join(homeDir, ".lobsterdog")
	newDir := filepath.Join(homeDir, ".config", "llmem")

	// Create old directory with a file
	os.MkdirAll(oldDir, 0700)
	os.WriteFile(filepath.Join(oldDir, "config.yaml"), []byte("memory:\n  db: test.db\n"), 0600)

	t.Setenv("HOME", homeDir)
	migrated, err := MigrateFromLobsterdog()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migration to occur")
	}

	// Check the file was copied
	data, err := os.ReadFile(filepath.Join(newDir, "config.yaml"))
	if err != nil {
		t.Fatalf("expected config.yaml in new dir: %v", err)
	}
	if string(data) != "memory:\n  db: test.db\n" {
		t.Errorf("config.yaml content mismatch: %q", string(data))
	}
}

func TestMigrateFromLobsterdog_Idempotent(t *testing.T) {
	homeDir := t.TempDir()

	oldDir := filepath.Join(homeDir, ".lobsterdog")
	newDir := filepath.Join(homeDir, ".config", "llmem")

	os.MkdirAll(oldDir, 0700)
	os.WriteFile(filepath.Join(oldDir, "config.yaml"), []byte("test\n"), 0600)

	t.Setenv("HOME", homeDir)

	// First migration
	migrated, _ := MigrateFromLobsterdog()
	if !migrated {
		t.Error("expected first migration to succeed")
	}

	// Second migration — should be no-op because new dir exists
	migrated, _ = MigrateFromLobsterdog()
	if migrated {
		t.Error("expected second migration to be no-op")
	}

	// Verify the file still exists
	if _, err := os.Stat(filepath.Join(newDir, "config.yaml")); err != nil {
		t.Errorf("expected config.yaml to persist: %v", err)
	}
}