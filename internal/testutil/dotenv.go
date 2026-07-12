package testutil

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnvAtPath reads a single .env file and sets env vars. Returns true if file existed.
func loadDotEnvAtPath(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)

		// Don't override already-set env vars (CLI/shell takes precedence)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return true
}

// LoadDotEnv reads a .env file and sets environment variables.
// If the file isn't found at path, walks up parent directories until found.
// Missing file is a no-op (not an error).
func LoadDotEnv(path string) {
	if loadDotEnvAtPath(path) {
		return
	}
	dir, _ := os.Getwd()
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // filesystem root
		}
		dir = parent
		if loadDotEnvAtPath(filepath.Join(dir, ".env")) {
			return
		}
	}
}
