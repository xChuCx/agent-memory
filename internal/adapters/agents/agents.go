// Package agents is the AGENTS.md adapter for agent-memory. AGENTS.md
// is a project-root Markdown file that an industry-broad set of AI
// agent runtimes (OpenAI Codex CLI, Cursor's agent mode, Sourcegraph
// Cody, and others) reads as persistent project context.
//
// Unlike Claude / Cursor (which use hidden dirs), AGENTS.md sits at
// the repo root. There is no widely-agreed user-global location for
// it; this adapter is project-local only and rejects UserGlobal: true
// with a clear error.
package agents

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentfs "github.com/agent-memory/agent-memory/internal/fs"
)

// AdapterName is the stable identifier used by `agent-memory install <name>`.
const AdapterName = "agents"

// agentsFile is the canonical filename at the repo root.
const agentsFile = "AGENTS.md"

//go:embed AGENTS.md
var agentsFS embed.FS

// Options mirrors claude/cursor for shape uniformity. UserGlobal must
// be false; Install rejects true with a clear message.
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

// Install writes the embedded AGENTS.md to <Root>/AGENTS.md. Returns
// an error for UserGlobal: true because there is no convention this
// adapter respects — open an issue if you have a use case.
func Install(opts Options) (*Result, error) {
	if opts.UserGlobal {
		return nil, errors.New("agents install: --user-global is not supported (AGENTS.md has no agreed home-dir location); install per-project")
	}
	if opts.Root == "" {
		return nil, errors.New("agents install: Root is required")
	}
	target := filepath.Join(opts.Root, agentsFile)
	if !opts.Force {
		if _, statErr := os.Stat(target); statErr == nil {
			return &Result{Adapter: AdapterName, Skipped: []string{target}}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("agents install: stat %s: %w", target, statErr)
		}
	}
	body, err := agentsFS.ReadFile(agentsFile)
	if err != nil {
		return nil, fmt.Errorf("agents install: read embedded AGENTS.md: %w", err)
	}
	if err := os.MkdirAll(opts.Root, 0755); err != nil {
		return nil, fmt.Errorf("agents install: mkdir %s: %w", opts.Root, err)
	}
	if err := agentfs.WriteAtomic(target, body, 0644); err != nil {
		return nil, fmt.Errorf("agents install: write %s: %w", target, err)
	}
	return &Result{Adapter: AdapterName, Files: []string{target}}, nil
}

// AgentsContent returns the embedded AGENTS.md bytes verbatim. Used
// by tests and by future `install agents --print`.
func AgentsContent() []byte {
	b, _ := agentsFS.ReadFile(agentsFile)
	return b
}
