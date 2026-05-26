package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/lock"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// StatusReport is the structured shape returned by `agent-memory status`.
// It is the JSON output schema when --json is used; the human renderer
// reads the same struct.
type StatusReport struct {
	Repo         string         `json:"repo"`
	Version      string         `json:"memory_version"`
	Root         string         `json:"root"`
	MemoryDir    string         `json:"memory_dir"`
	ManifestPath string         `json:"manifest_path"`
	SchemaPath   string         `json:"schema_path"`
	Categories   map[string]int `json:"categories"`
	Lock         lock.Metadata  `json:"lock,omitempty"`
}

// NewStatusCmd returns the `agent-memory status` subcommand.
func NewStatusCmd() *cobra.Command {
	var (
		rootFlag string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print agent-memory state for the current repo",
		Long: `Reads .agent-memory/meta/manifest.yaml and meta/schema.yaml,
counts Markdown files per category, and reports the last-known lock
metadata. Returns an error if .agent-memory/ does not exist.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := runStatus(rootFlag)
			if err != nil {
				return err
			}
			if asJSON {
				return writeStatusJSON(cmd.OutOrStdout(), r)
			}
			return writeStatusHuman(cmd.OutOrStdout(), r)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return cmd
}

// runStatus assembles the StatusReport. Exposed for direct test calls.
func runStatus(rootFlag string) (*StatusReport, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return nil, err
	}
	memDir := memoryDir(root)
	if ok, _ := pathExists(memDir); !ok {
		return nil, fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
	}

	manifestPath := filepath.Join(memDir, "meta", "manifest.yaml")
	schemaPath := filepath.Join(memDir, "meta", "schema.yaml")

	m, err := config.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("status: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("status: load schema: %w", err)
	}

	categories, err := countCategoriesUnder(memDir, sch)
	if err != nil {
		return nil, fmt.Errorf("status: count files: %w", err)
	}

	// Lock metadata is best-effort. ReadMetadata returns an empty Metadata
	// (no error) if the sidecar is missing/empty/malformed — which is the
	// normal state right after init.
	meta, _ := lock.ReadMetadata(filepath.Join(memDir, "meta", "lock"))

	return &StatusReport{
		Repo:         m.Project.Name,
		Version:      m.Version,
		Root:         root,
		MemoryDir:    memDir,
		ManifestPath: manifestPath,
		SchemaPath:   schemaPath,
		Categories:   categories,
		Lock:         meta,
	}, nil
}

// countCategoriesUnder walks memDir and counts .md files per schema category.
// Files that don't match any category (e.g., stray notes someone dropped in)
// are silently ignored — they don't show up in counts.
func countCategoriesUnder(memDir string, sch *schema.Schema) (map[string]int, error) {
	counts := make(map[string]int, len(sch.Categories))
	for name := range sch.Categories {
		counts[name] = 0
	}

	err := filepath.WalkDir(memDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, err := filepath.Rel(memDir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if cat, ok := sch.CategoryForPath(relSlash); ok {
			counts[cat.Name]++
		}
		return nil
	})
	return counts, err
}

func writeStatusJSON(w io.Writer, r *StatusReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func writeStatusHuman(w io.Writer, r *StatusReport) error {
	fmt.Fprintf(w, "agent-memory %s\n", r.Version)
	fmt.Fprintf(w, "repo: %s\n", r.Repo)
	fmt.Fprintf(w, "root: %s\n", r.Root)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Categories:")
	// Stable display order: alphabetical.
	names := make([]string, 0, len(r.Categories))
	for name := range r.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-15s %d\n", name, r.Categories[name])
	}
	fmt.Fprintln(w)

	if r.Lock.OwnerID != "" {
		ts := r.Lock.AcquiredAt.UTC().Format("2006-01-02T15:04:05Z")
		fmt.Fprintf(w, "Last known lock owner: %s (%s, pid %d) at %s\n",
			r.Lock.OwnerID, r.Lock.OwnerKind, r.Lock.OwnerPID, ts)
		if r.Lock.OpID != "" {
			fmt.Fprintf(w, "  op_id: %s\n", r.Lock.OpID)
		}
	} else {
		fmt.Fprintln(w, "No prior lock metadata.")
	}
	return nil
}
