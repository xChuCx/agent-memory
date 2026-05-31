package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xChuCx/agent-memory/internal/config"
	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// statusMemoryVersion is stamped into memory.status output. The MCP
// server is constructed with a version string (see New); we thread it
// through so the tool reports the same value the binary's `version`
// subcommand does.
//
// Captured per-server in registerStatus's closure rather than a package
// global so tests with different versions don't interfere.

// StatusInput is the (empty) input shape for memory.status. The tool
// takes no parameters today; the struct exists so the SDK can advertise
// a JSON schema. A future revision might add an `include` filter.
type StatusInput struct{}

// StatusOutput is the JSON shape memory.status returns. It mirrors
// memory.MemoryStatus (design §15.11) field-for-field; the jsonschema
// tags document each block for the tool catalog.
type StatusOutput struct {
	MemoryVersion     string                       `json:"memory_version" jsonschema:"the agent-memory binary version"`
	Repo              string                       `json:"repo" jsonschema:"project name from the manifest"`
	ActiveBranch      string                       `json:"active_branch,omitempty" jsonschema:"current git branch, empty outside a repo"`
	DurableFiles      int                          `json:"durable_files" jsonschema:"count of long-lived git-tracked memory files"`
	ArchiveFiles      int                          `json:"archive_files" jsonschema:"count of files under archive/"`
	LocalSessions     int                          `json:"local_sessions" jsonschema:"count of session-log files under sessions/"`
	LocalCurrentFiles int                          `json:"local_current_files" jsonschema:"count of branch-local current.*.md files"`
	OrphanLocalFiles  []string                     `json:"orphan_local_files,omitempty" jsonschema:"local current files whose branch no longer exists"`
	IndexSizeBytes    int64                        `json:"index_size_bytes" jsonschema:"size of the FTS5 shadow index on disk"`
	CurrentSizeBytes  int64                        `json:"current_size_bytes" jsonschema:"combined size of the active branch + shared current files"`
	StagedUpdates     []memory.StagedStatusEntry   `json:"staged_updates,omitempty" jsonschema:"pending staged proposals with age, TTL, and drift status"`
	StaleNotes        []string                     `json:"stale_notes,omitempty" jsonschema:"files flagged stale by freshness tracking (future)"`
	Security          memory.MemoryStatusSecurity  `json:"security" jsonschema:"secret-scan + provenance posture"`
	Git               memory.MemoryStatusGit       `json:"git" jsonschema:"git integration flags"`
	Lock              memory.MemoryStatusLock      `json:"lock" jsonschema:"advisory-lock state"`
}

// registerStatus wires memory.status onto server. The closure captures
// root + version; every call re-loads manifest+schema and re-walks the
// tree so the report reflects live state.
//
// Unlike propose_update, status is read-only and has no "rejection"
// concept — it returns a Go error only for genuine infrastructure
// failures (missing .agent-memory/, manifest/schema load).
func registerStatus(server *mcpsdk.Server, root, version string) error {
	handler := func(ctx context.Context, req *mcpsdk.CallToolRequest, input StatusInput) (
		*mcpsdk.CallToolResult,
		StatusOutput,
		error,
	) {
		out, err := runStatus(ctx, root, version)
		if err != nil {
			return nil, StatusOutput{}, err
		}
		return nil, *out, nil
	}

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "memory.status",
		Description: "Report memory health and metadata for the project's " +
			".agent-memory/ store: file counts per kind, index + current-state " +
			"sizes, pending staged proposals (with age, TTL remaining, and drift " +
			"status per proposal), orphaned branch-local files, secret-scan / git / " +
			"lock posture. Read-only; never modifies any file. Call this to decide " +
			"whether memory needs maintenance (stale staging, drifted proposals) " +
			"before proposing further updates.",
	}, handler)
	return nil
}

// runStatus is the request-level handler. Separated from the closure so
// unit tests can drive it without an MCP transport.
func runStatus(ctx context.Context, root, version string) (*StatusOutput, error) {
	memDir := filepath.Join(root, memoryDirName)

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.status: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.status: load schema: %w", err)
	}

	// Best-effort branch resolution; zero value is fine for non-git repos.
	branch, _ := agentgit.ActiveBranch(root)

	st, err := memory.BuildStatus(ctx, memory.StatusDeps{
		MemoryDir:     memDir,
		Manifest:      manifest,
		Schema:        sch,
		Branch:        branch,
		MemoryVersion: version,
	})
	if err != nil {
		return nil, fmt.Errorf("memory.status: %w", err)
	}

	return &StatusOutput{
		MemoryVersion:     st.MemoryVersion,
		Repo:              st.Repo,
		ActiveBranch:      st.ActiveBranch,
		DurableFiles:      st.DurableFiles,
		ArchiveFiles:      st.ArchiveFiles,
		LocalSessions:     st.LocalSessions,
		LocalCurrentFiles: st.LocalCurrentFiles,
		OrphanLocalFiles:  st.OrphanLocalFiles,
		IndexSizeBytes:    st.IndexSizeBytes,
		CurrentSizeBytes:  st.CurrentSizeBytes,
		StagedUpdates:     st.StagedUpdates,
		StaleNotes:        st.StaleNotes,
		Security:          st.Security,
		Git:               st.Git,
		Lock:              st.Lock,
	}, nil
}
