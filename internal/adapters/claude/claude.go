// Package claude is the Claude Code adapter for agent-memory: it bundles a
// worked SKILL.md (the file teaches Claude Code when and how to call
// memory.fetch_context / memory.propose_update) and an Install function
// that materialises that asset under .claude/skills/agent-memory/ either
// in the current repo or in the user's global Claude Code config.
//
// Future adapters live as sibling packages (internal/adapters/cursor,
// internal/adapters/codex, ...). Each owns its own embedded assets and
// exposes the same shape — see docs/patterns/adapter-installation.md.
package claude

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// AdapterName is the stable identifier used by `agent-memory install <name>`.
const AdapterName = "claude"

// skillDirRel is the path inside the install base where the skill lives.
// Forward-slash form is fine on Windows because filepath.Join normalises.
const skillDirRel = ".claude/skills/agent-memory"

// skillFile is the canonical SKILL.md file name Claude Code looks for.
const skillFile = "SKILL.md"

//go:embed SKILL.md
var skillFS embed.FS

// Options configures Install.
type Options struct {
	// Root is the repository root that hosts .claude/skills/. Required
	// when UserGlobal is false; ignored otherwise.
	Root string

	// UserGlobal redirects the install to ~/.claude/skills/ (or the
	// HomeDir override below). The skill is then visible to Claude Code
	// across every project on this machine.
	UserGlobal bool

	// Force overwrites an existing SKILL.md. Without it, Install returns
	// AlreadyInstalled in Result.Skipped and writes nothing.
	Force bool

	// HomeDir overrides os.UserHomeDir() — tests set it so the install
	// lands in t.TempDir() instead of the real home directory.
	HomeDir string
}

// Result reports what Install did.
type Result struct {
	// Adapter is always AdapterName ("claude"). Included so the CLI layer
	// can render uniform results across multiple adapters one day.
	Adapter string `json:"adapter"`

	// Files lists absolute paths Install wrote. Empty when nothing was
	// written (refused overwrite without Force).
	Files []string `json:"files,omitempty"`

	// Skipped lists absolute paths Install would have written but didn't
	// because the destination already existed and Force was false.
	Skipped []string `json:"skipped,omitempty"`
}

// Install writes the embedded SKILL.md to the chosen skill directory and —
// for a project (non-user-global) install — registers the agent-memory MCP
// server in the repo's .mcp.json so the agent's writes land in THIS repo's
// .agent-memory/. Intermediate directories are created as needed; existing
// files are preserved unless Force is set.
//
// Returns a Go error only for filesystem / configuration failures. The
// "skipped because already present" case is reported through Result, not
// through error — symmetric with the staging Apply contract so the CLI
// can render it the same way.
func Install(opts Options) (*Result, error) {
	base, err := resolveBase(opts)
	if err != nil {
		return nil, err
	}
	res := &Result{Adapter: AdapterName}

	// 1. The skill asset.
	skillDir := filepath.Join(base, filepath.FromSlash(skillDirRel))
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, fmt.Errorf("claude install: mkdir %s: %w", skillDir, err)
	}
	target := filepath.Join(skillDir, skillFile)
	skillExists := false
	if _, statErr := os.Stat(target); statErr == nil {
		skillExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("claude install: stat %s: %w", target, statErr)
	}
	if skillExists && !opts.Force {
		res.Skipped = append(res.Skipped, target)
	} else {
		body, err := skillFS.ReadFile(skillFile)
		if err != nil {
			// Should be impossible: the embed directive is right above.
			return nil, fmt.Errorf("claude install: read embedded SKILL.md: %w", err)
		}
		if err := agentfs.WriteAtomic(target, body, 0644); err != nil {
			return nil, fmt.Errorf("claude install: write %s: %w", target, err)
		}
		res.Files = append(res.Files, target)
	}

	// 2. The project MCP server registration. Skipped for a user-global
	//    install: .mcp.json is inherently per-repo, and a single user-scoped
	//    server with a fixed root is exactly the footgun this avoids — the
	//    server resolves the repo from $CLAUDE_PROJECT_DIR at spawn instead.
	if !opts.UserGlobal {
		mcpPath, wrote, err := ensureProjectMCPConfig(opts.Root, opts.Force)
		if err != nil {
			return nil, err
		}
		if wrote {
			res.Files = append(res.Files, mcpPath)
		} else {
			res.Skipped = append(res.Skipped, mcpPath)
		}
	}

	return res, nil
}

// MCPServerName is the key under .mcp.json's mcpServers for agent-memory.
const MCPServerName = "agent-memory"

// mcpConfigFile is the project-scoped MCP config Claude Code reads.
const mcpConfigFile = ".mcp.json"

// ensureProjectMCPConfig creates or merges the repo's .mcp.json so it registers
// the agent-memory stdio server scoped to THIS repo. The server is invoked as
// `agent-memory mcp --root ${CLAUDE_PROJECT_DIR:-.}` — Claude Code expands
// CLAUDE_PROJECT_DIR to the project root at spawn, so the config is portable
// across clones/machines and each repo's agent writes to its own .agent-memory/.
// Being project-scoped, it also takes precedence over any stray user-scoped
// server of the same name (Claude Code: local > project > user).
//
// Merge is non-destructive: other servers and top-level keys in an existing
// .mcp.json are preserved. If an agent-memory entry already exists it is left
// untouched (reported as skipped) unless force is set.
func ensureProjectMCPConfig(root string, force bool) (target string, wrote bool, err error) {
	if root == "" {
		return "", false, errors.New("claude install: root is required to write .mcp.json")
	}
	target = filepath.Join(root, mcpConfigFile)

	doc := map[string]any{}
	switch existing, rerr := os.ReadFile(target); {
	case rerr == nil:
		if len(existing) > 0 {
			if uerr := json.Unmarshal(existing, &doc); uerr != nil {
				return target, false, fmt.Errorf("claude install: parse %s: %w", target, uerr)
			}
		}
	case errors.Is(rerr, os.ErrNotExist):
		// fresh file
	default:
		return target, false, fmt.Errorf("claude install: read %s: %w", target, rerr)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers[MCPServerName]; exists && !force {
		return target, false, nil // already registered — leave it alone
	}
	servers[MCPServerName] = map[string]any{
		"type":    "stdio",
		"command": "agent-memory",
		"args":    []any{"mcp", "--root", "${CLAUDE_PROJECT_DIR:-.}"},
	}
	doc["mcpServers"] = servers

	out, merr := json.MarshalIndent(doc, "", "  ")
	if merr != nil {
		return target, false, fmt.Errorf("claude install: encode %s: %w", target, merr)
	}
	out = append(out, '\n')
	if werr := agentfs.WriteAtomic(target, out, 0644); werr != nil {
		return target, false, fmt.Errorf("claude install: write %s: %w", target, werr)
	}
	return target, true, nil
}

// SkillContent returns the embedded SKILL.md bytes verbatim. Exposed for
// tests and for `agent-memory install claude --print` (future enhancement)
// so users can preview the asset before writing it.
func SkillContent() []byte {
	b, _ := skillFS.ReadFile(skillFile)
	return b
}

// resolveBase picks the directory under which .claude/skills/ should live.
// UserGlobal → HomeDir override or os.UserHomeDir(); otherwise opts.Root.
func resolveBase(opts Options) (string, error) {
	if opts.UserGlobal {
		if opts.HomeDir != "" {
			return opts.HomeDir, nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("claude install: resolve home: %w", err)
		}
		return h, nil
	}
	if opts.Root == "" {
		return "", errors.New("claude install: Root is required when UserGlobal is false")
	}
	return opts.Root, nil
}
