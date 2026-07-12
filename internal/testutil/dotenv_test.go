package testutil

import (
	"os"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	// Write a temp .env
	path := t.TempDir() + "/.env"
	os.WriteFile(path, []byte(`# comment
DJI_TEST_KEY=hello
DJI_TEST_QUOTED="world"
DJI_TEST_NUM=42
`), 0644)

	os.Unsetenv("DJI_TEST_KEY")
	os.Unsetenv("DJI_TEST_QUOTED")
	os.Unsetenv("DJI_TEST_NUM")

	LoadDotEnv(path)

	if v := os.Getenv("DJI_TEST_KEY"); v != "hello" {
		t.Errorf("DJI_TEST_KEY=%q, want hello", v)
	}
	if v := os.Getenv("DJI_TEST_QUOTED"); v != "world" {
		t.Errorf("DJI_TEST_QUOTED=%q, want world", v)
	}
	if v := os.Getenv("DJI_TEST_NUM"); v != "42" {
		t.Errorf("DJI_TEST_NUM=%q, want 42", v)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	// Should not error on missing file
	LoadDotEnv("/nonexistent/.env")
}

func TestLoadDotEnvNoOverride(t *testing.T) {
	path := t.TempDir() + "/.env"
	os.WriteFile(path, []byte("DJI_TEST_EXISTING=from_file\n"), 0644)

	os.Setenv("DJI_TEST_EXISTING", "from_shell")
	LoadDotEnv(path)

	if v := os.Getenv("DJI_TEST_EXISTING"); v != "from_shell" {
		t.Errorf("expected shell override, got %q", v)
	}
}
