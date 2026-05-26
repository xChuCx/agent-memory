package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// MemoryDirName is the directory created inside a repo root that holds
// the agent-memory state. Centralised here so init/status/doctor agree.
const MemoryDirName = ".agent-memory"

// resolveRoot returns the absolute path of the chosen repo root:
// the --root flag value if non-empty, otherwise the current working
// directory.
func resolveRoot(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolveRoot: getwd: %w", err)
	}
	return wd, nil
}

// memoryDir returns the path to the .agent-memory/ directory under root.
func memoryDir(root string) string {
	return filepath.Join(root, MemoryDirName)
}

// pathExists reports whether p exists (any kind: file, dir, symlink).
// Wraps os.Stat so callers don't have to do the errors.Is dance.
func pathExists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
