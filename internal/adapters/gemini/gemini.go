// Package gemini is the Gemini CLI adapter for agent-memory. Gemini CLI
// reads GEMINI.md at the project root as long-term project context;
// this adapter drops a worked file at that location.
//
// As with the agents adapter, there is no widely-agreed user-global
// location for GEMINI.md, so this adapter is project-local only and
// rejects UserGlobal: true with a clear error.
package gemini

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentfs "github.com/agent-memory/agent-memory/internal/fs"
)

// AdapterName is the stable identifier used by `agent-memory install <name>`.
const AdapterName = "gemini"

// geminiFile is the canonical filename at the repo root.
const geminiFile = "GEMINI.md"

//go:embed GEMINI.md
var geminiFS embed.FS

// Options keeps the same shape as every other adapter for uniform CLI
// dispatch. UserGlobal: true is rejected with a clear error.
type Options struct {
	Root       string
	UserGlobal bool
	Force      bool
	HomeDir    string // unused for this adapter
}

// Result is the standard adapter Result.
type Result struct {
	Adapter string   `json:"adapter"`
	Files   []string `json:"files,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// Install writes the embedded GEMINI.md to <Root>/GEMINI.md.
func Install(opts Options) (*Result, error) {
	if opts.UserGlobal {
		return nil, errors.New("gemini install: --user-global is not supported (GEMINI.md has no agreed home-dir location); install per-project")
	}
	if opts.Root == "" {
		return nil, errors.New("gemini install: Root is required")
	}
	target := filepath.Join(opts.Root, geminiFile)
	if !opts.Force {
		if _, statErr := os.Stat(target); statErr == nil {
			return &Result{Adapter: AdapterName, Skipped: []string{target}}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("gemini install: stat %s: %w", target, statErr)
		}
	}
	body, err := geminiFS.ReadFile(geminiFile)
	if err != nil {
		return nil, fmt.Errorf("gemini install: read embedded GEMINI.md: %w", err)
	}
	if err := os.MkdirAll(opts.Root, 0755); err != nil {
		return nil, fmt.Errorf("gemini install: mkdir %s: %w", opts.Root, err)
	}
	if err := agentfs.WriteAtomic(target, body, 0644); err != nil {
		return nil, fmt.Errorf("gemini install: write %s: %w", target, err)
	}
	return &Result{Adapter: AdapterName, Files: []string{target}}, nil
}

// GeminiContent returns the embedded GEMINI.md bytes verbatim.
func GeminiContent() []byte {
	b, _ := geminiFS.ReadFile(geminiFile)
	return b
}
