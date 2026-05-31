// Package cursor is the Cursor IDE adapter for agent-memory. It bundles
// a worked Cursor-rule (.mdc) file teaching the editor's agent when and
// how to call memory.fetch_context / memory.propose_update, plus an
// Install function that drops the file at the documented location.
//
// Cursor reads .mdc files from .cursor/rules/ (project-local) and from
// ~/.cursor/rules/ (user-global). Both are supported here.
package cursor

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// AdapterName is the stable identifier used by `agent-memory install <name>`.
const AdapterName = "cursor"

// rulesDirRel is the path inside the install base where the rule file lives.
const rulesDirRel = ".cursor/rules"

// ruleFile is the rule file Cursor expects to find.
const ruleFile = "agent-memory.mdc"

//go:embed agent-memory.mdc
var ruleFS embed.FS

// Options mirrors the same shape used by every adapter package.
type Options struct {
	Root       string
	UserGlobal bool
	Force      bool
	HomeDir    string
}

// Result reports what Install did. Same shape as claude/agents/gemini
// so the CLI dispatch layer can render results uniformly.
type Result struct {
	Adapter string   `json:"adapter"`
	Files   []string `json:"files,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// Install writes the embedded agent-memory.mdc to the chosen rules
// directory, creating intermediate directories as needed. Existing
// files are preserved unless Force is set.
//
// Symmetric with claude's Install: same return-result contract,
// "skipped because already present" reported in Result.Skipped (not
// as an error).
func Install(opts Options) (*Result, error) {
	base, err := resolveBase(opts)
	if err != nil {
		return nil, err
	}
	rulesDir := filepath.Join(base, filepath.FromSlash(rulesDirRel))
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return nil, fmt.Errorf("cursor install: mkdir %s: %w", rulesDir, err)
	}
	target := filepath.Join(rulesDir, ruleFile)
	if !opts.Force {
		if _, statErr := os.Stat(target); statErr == nil {
			return &Result{Adapter: AdapterName, Skipped: []string{target}}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("cursor install: stat %s: %w", target, statErr)
		}
	}
	body, err := ruleFS.ReadFile(ruleFile)
	if err != nil {
		return nil, fmt.Errorf("cursor install: read embedded rule: %w", err)
	}
	if err := agentfs.WriteAtomic(target, body, 0644); err != nil {
		return nil, fmt.Errorf("cursor install: write %s: %w", target, err)
	}
	return &Result{Adapter: AdapterName, Files: []string{target}}, nil
}

// RuleContent returns the embedded .mdc bytes verbatim. Used by tests
// and by future `install cursor --print`.
func RuleContent() []byte {
	b, _ := ruleFS.ReadFile(ruleFile)
	return b
}

func resolveBase(opts Options) (string, error) {
	if opts.UserGlobal {
		if opts.HomeDir != "" {
			return opts.HomeDir, nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cursor install: resolve home: %w", err)
		}
		return h, nil
	}
	if opts.Root == "" {
		return "", errors.New("cursor install: Root is required when UserGlobal is false")
	}
	return opts.Root, nil
}
