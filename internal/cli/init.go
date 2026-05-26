package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	agentfs "github.com/agent-memory/agent-memory/internal/fs"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// initOptions bundles arguments to runInit. Exposed at the package level so
// tests can call runInit directly without going through cobra's flag parsing.
type initOptions struct {
	// Root is the absolute path to the repo root. If empty, runInit
	// resolves it via resolveRoot (the --root flag value or cwd).
	Root string

	// ProjectName ends up as Manifest.Project.Name. If empty, runInit
	// falls back to filepath.Base(root).
	ProjectName string

	// Force allows overwriting an existing .agent-memory/ directory.
	Force bool

	// WithMergeDriver is the user-facing flag for the M7 git merge driver
	// setup. M1 acknowledges and skips — see runInit.
	WithMergeDriver bool
}

// NewInitCmd returns the `agent-memory init` subcommand.
func NewInitCmd() *cobra.Command {
	var opts initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize an .agent-memory/ directory in the current repo",
		Long: `Create the .agent-memory/ scaffold:

  - meta/manifest.yaml, meta/schema.yaml (default config)
  - .gitignore (local state and derived files)
  - index.md, conventions.md, decisions.md, pitfalls.md (durable Markdown templates)
  - modules/, archive/, local/, sessions/, staging/, meta/ (directory layout)
  - meta/lock (empty placeholder for the OS advisory lock)

Refuses to overwrite an existing .agent-memory/ unless --force is given.

--with-merge-driver is accepted but lands in M7; M1 prints a notice and
skips that step.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Root, "root", "", "repo root (default: current working directory)")
	cmd.Flags().StringVar(&opts.ProjectName, "name", "", "project name (default: basename of root)")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite an existing .agent-memory/")
	cmd.Flags().BoolVar(&opts.WithMergeDriver, "with-merge-driver", false, "configure git merge driver (M7; currently a no-op)")
	return cmd
}

// runInit performs the init workflow. Exposed for direct test calls.
func runInit(out io.Writer, opts initOptions) error {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	memDir := memoryDir(root)

	if exists, err := pathExists(memDir); err != nil {
		return fmt.Errorf("init: stat %s: %w", memDir, err)
	} else if exists && !opts.Force {
		return fmt.Errorf(".agent-memory/ already exists at %s (use --force to overwrite)", memDir)
	}

	projectName := opts.ProjectName
	if projectName == "" {
		projectName = filepath.Base(root)
	}

	if err := makeLayout(memDir); err != nil {
		return fmt.Errorf("init: create layout: %w", err)
	}
	if err := writeGitignore(memDir); err != nil {
		return fmt.Errorf("init: write .gitignore: %w", err)
	}
	if err := schema.WriteDefault(filepath.Join(memDir, "meta", "schema.yaml")); err != nil {
		return fmt.Errorf("init: write schema.yaml: %w", err)
	}
	if err := config.WriteDefault(filepath.Join(memDir, "meta", "manifest.yaml"), projectName); err != nil {
		return fmt.Errorf("init: write manifest.yaml: %w", err)
	}
	if err := writeDurableTemplates(memDir); err != nil {
		return fmt.Errorf("init: write Markdown templates: %w", err)
	}
	// Empty lock placeholder. gofrs/flock would create it on demand at
	// first Acquire, but writing it now keeps the layout explicit.
	if err := agentfs.WriteAtomic(filepath.Join(memDir, "meta", "lock"), nil, 0644); err != nil {
		return fmt.Errorf("init: write lock placeholder: %w", err)
	}

	if opts.WithMergeDriver {
		fmt.Fprintln(out, "Note: --with-merge-driver requested; merge driver setup lands in M7. Skipping.")
	}

	fmt.Fprintf(out, "Initialized .agent-memory/ in %s\n", memDir)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  agent-memory status     # verify the layout")
	fmt.Fprintln(out, "  agent-memory doctor     # check for issues")
	return nil
}

// makeLayout creates the .agent-memory/ directory tree, including the
// git-tracked-but-empty modules/ and archive/ with .gitkeep markers.
//
// The local/, sessions/, staging/ directories are gitignored by the
// .gitignore the caller writes next, so no .gitkeep is needed there —
// they survive on disk but aren't tracked.
func makeLayout(memDir string) error {
	dirs := []string{
		memDir,
		filepath.Join(memDir, "modules"),
		filepath.Join(memDir, "archive"),
		filepath.Join(memDir, "local"),
		filepath.Join(memDir, "sessions"),
		filepath.Join(memDir, "staging"),
		filepath.Join(memDir, "meta"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	for _, p := range []string{
		filepath.Join(memDir, "modules", ".gitkeep"),
		filepath.Join(memDir, "archive", ".gitkeep"),
	} {
		if err := agentfs.WriteAtomic(p, nil, 0644); err != nil {
			return err
		}
	}
	return nil
}

const gitignoreContent = `# Local state — per-developer, never committed.
local/
sessions/
staging/

# Derived indexes and caches — rebuildable from the Markdown source.
meta/*.sqlite
meta/*.sqlite-*
meta/*.db
meta/*.db-*

# Advisory lock files — kernel state, not portable.
meta/lock
meta/lock.info
`

func writeGitignore(memDir string) error {
	return agentfs.WriteAtomic(filepath.Join(memDir, ".gitignore"), []byte(gitignoreContent), 0644)
}

// writeDurableTemplates writes the four canonical durable Markdown files
// with minimal scaffolding. The exact layout of each file is documented in
// design doc v0.4.1 §10; we just provide the heading and an explanatory
// placeholder so the file is valid Markdown and visible in `status` counts.
func writeDurableTemplates(memDir string) error {
	templates := map[string]string{
		"index.md": "# Agent Memory Index\n" +
			"<!-- @generated: do not edit by hand; use `agent-memory rebuild-index` -->\n\n" +
			"## Always include\n" +
			"- conventions.md — build, test, style, workflow rules\n\n" +
			"## Topic map\n" +
			"(none yet — populated as memory grows)\n\n" +
			"## Freshness\n" +
			"Last full validation: <init>\n",
		"conventions.md": "# Conventions\n" +
			"<!-- @id: conventions -->\n\n" +
			"(Add project-wide working rules here: test commands, formatting,\n" +
			"branching, naming, required review practices. See design doc §10.3.)\n",
		"decisions.md": "# Decisions\n" +
			"<!-- @id: decisions -->\n\n" +
			"(Architectural and product decisions land here as individual sections.\n" +
			"See design doc §10.4 for the recommended per-decision format:\n" +
			"Date / Status / Confidence / Context / Decision / Consequences / Sources.)\n",
		"pitfalls.md": "# Pitfalls\n" +
			"<!-- @id: pitfalls -->\n\n" +
			"(Known traps and recurring failures, organised by area. Each entry\n" +
			"should record: what failed, why, how to avoid repeating it, related\n" +
			"files/tests, freshness/confidence. See design doc §10.5.)\n",
	}
	for name, content := range templates {
		if err := agentfs.WriteAtomic(filepath.Join(memDir, name), []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}
