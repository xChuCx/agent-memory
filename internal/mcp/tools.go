package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/agent-memory/agent-memory/internal/config"
	agentgit "github.com/agent-memory/agent-memory/internal/git"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/memory"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// MemoryDirName is duplicated from internal/cli to avoid a cli → mcp import
// cycle. The constant value is part of the on-disk contract and shouldn't
// drift.
const memoryDirName = ".agent-memory"

// FetchContextInput is the JSON schema for the memory.fetch_context tool's
// input. Struct tags drive both serialisation and the JSON Schema the SDK
// advertises to clients.
type FetchContextInput struct {
	Query          string   `json:"query,omitempty" jsonschema:"search query; empty returns the bootstrap pack"`
	Scope          []string `json:"scope,omitempty" jsonschema:"paths or module names to prioritize via substring match"`
	Budget         int      `json:"budget,omitempty" jsonschema:"approximate character budget for the returned pack; 0 uses manifest default"`
	Include        []string `json:"include,omitempty" jsonschema:"context categories to include (advisory in M2; M3 enforces)"`
	ExcludeArchive bool     `json:"exclude_archive,omitempty" jsonschema:"if true, archive/ files are skipped entirely; defaults to false"`
}

// FetchContextOutput mirrors memory.FetchResponse but lives in this package
// so the SDK generates a JSON schema we control. Field names match the
// design doc §15.1.
type FetchContextOutput struct {
	Context              string                 `json:"context" jsonschema:"the Markdown context pack"`
	IncludedFiles        []memory.IncludedFile  `json:"included_files" jsonschema:"per-file provenance for everything in the pack"`
	Omitted              []memory.OmittedFile   `json:"omitted,omitempty" jsonschema:"candidates that were dropped (budget exhausted, parse error, etc.)"`
	SuggestedNextQueries []string               `json:"suggested_next_queries,omitempty"`
	ContextMetadata      memory.ContextMetadata `json:"context_metadata"`
}

// registerFetchContext wires memory.fetch_context onto the given server.
// The root path is captured in the handler closure; every call re-resolves
// manifest/schema/index/branch so live edits between calls are picked up.
func registerFetchContext(server *mcpsdk.Server, root string) error {
	handler := func(ctx context.Context, req *mcpsdk.CallToolRequest, input FetchContextInput) (
		*mcpsdk.CallToolResult,
		FetchContextOutput,
		error,
	) {
		resp, err := runFetchContext(ctx, root, input)
		if err != nil {
			return nil, FetchContextOutput{}, err
		}
		return nil, *resp, nil
	}

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "memory.fetch_context",
		Description: "Return a budgeted, ranked Markdown context pack assembled from " +
			"the project's .agent-memory/ files. Call this before reading source " +
			"files manually; the pack contains current task state, conventions, " +
			"and any sections relevant to the query. An empty query returns the " +
			"bootstrap pack (local current state + conventions + index summary).",
	}, handler)
	return nil
}

// runFetchContext is the request-level handler. Separated from the closure
// in registerFetchContext so it can be unit-tested directly.
func runFetchContext(ctx context.Context, root string, input FetchContextInput) (*FetchContextOutput, error) {
	memDir := filepath.Join(root, memoryDirName)

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.fetch_context: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.fetch_context: load schema: %w", err)
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("memory.fetch_context: open index: %w", err)
	}
	defer idx.Close()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("memory.fetch_context: init index: %w", err)
	}
	if n, err := idx.CountSections(ctx); err == nil && n == 0 {
		if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{AssignMissingIDs: true}); err != nil {
			return nil, fmt.Errorf("memory.fetch_context: rebuild index: %w", err)
		}
	}

	branch, err := agentgit.ActiveBranch(root)
	if err != nil {
		branch = agentgit.BranchInfo{} // non-fatal; fetch falls back to shared local state
	}

	fetched, err := memory.BuildContextPack(ctx, memory.FetchRequest{
		Query:          input.Query,
		Scope:          input.Scope,
		Budget:         input.Budget,
		Include:        input.Include,
		ExcludeArchive: input.ExcludeArchive,
	}, memory.FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  manifest,
		MemoryDir: memDir,
		Branch:    branch,
	})
	if err != nil {
		return nil, err
	}

	return &FetchContextOutput{
		Context:              fetched.Context,
		IncludedFiles:        fetched.IncludedFiles,
		Omitted:              fetched.Omitted,
		SuggestedNextQueries: fetched.SuggestedNextQueries,
		ContextMetadata:      fetched.ContextMetadata,
	}, nil
}
