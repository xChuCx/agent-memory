//go:build e2e

// Package e2e holds end-to-end smoke tests that exercise the agent-memory
// binary as a real subprocess and drive its MCP server via the official
// SDK's CommandTransport. They confirm that the full Release 0.1 user
// flow (init → install → fetch → propose_update → review → apply →
// status → fetch-again → reject) works when the pieces talk to each
// other for real, not just in unit-test isolation.
//
// Build-tagged `e2e` so the default `go test ./...` stays fast; CI runs
// `go test -tags=e2e ./internal/e2e/...` separately on Linux.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// binPath is the compiled agent-memory binary, set by TestMain.
var binPath string

// TestMain compiles the binary into a temp dir once. Per-test setup then
// runs subprocess invocations against this path. Setting AGENT_MEMORY_BIN
// in the env skips compilation — useful for running the same suite
// against a release binary or a different working tree.
func TestMain(m *testing.M) {
	if override := os.Getenv("AGENT_MEMORY_BIN"); override != "" {
		binPath = override
		os.Exit(m.Run())
	}

	tmp, err := os.MkdirTemp("", "agent-memory-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: mktemp: %v\n", err)
		os.Exit(2)
	}
	defer os.RemoveAll(tmp)

	binName := "agent-memory"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath = filepath.Join(tmp, binName)

	// `go build` from the repo root the test was launched in.
	wd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/agent-memory")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: go build failed: %v\n", err)
		os.Exit(2)
	}

	os.Exit(m.Run())
}

// run executes `agent-memory <args>` rooted at root and returns stdout,
// combined stderr, and any error. The --root flag is appended on every
// call so tests don't have to cd into the project dir.
func run(t *testing.T, root string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = root
	var sout, serr bytes.Buffer
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err = cmd.Run()
	return sout.String(), serr.String(), err
}

// runJSON runs a CLI command with --json appended and unmarshals stdout
// into v.
func runJSON(t *testing.T, v any, root string, args ...string) {
	t.Helper()
	args = append(args, "--json")
	out, errOut, err := run(t, root, args...)
	if err != nil {
		t.Fatalf("run %v: %v\nstderr: %s", args, err, errOut)
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("decode %v output: %v\nraw: %s", args, err, out)
	}
}

// =============================================================================
// The single big test: walk the entire Release 0.1 flow end-to-end.
// =============================================================================

func TestRelease01_EndToEnd(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".agent-memory")

	// -------------------------------------------------------------------------
	// 1. init: bring up .agent-memory/
	// -------------------------------------------------------------------------
	t.Run("init", func(t *testing.T) {
		out, errOut, err := run(t, root, "init", "--name", "smoke-test")
		if err != nil {
			t.Fatalf("init: %v\nstdout=%s\nstderr=%s", err, out, errOut)
		}
		for _, want := range []string{
			"meta/manifest.yaml",
			"meta/schema.yaml",
			"conventions.md",
			"decisions.md",
			"pitfalls.md",
			"index.md",
		} {
			abs := filepath.Join(memDir, filepath.FromSlash(want))
			if _, err := os.Stat(abs); err != nil {
				t.Errorf("init did not create %s: %v", want, err)
			}
		}
	})

	// -------------------------------------------------------------------------
	// 2. install claude: SKILL.md lands at the documented path
	// -------------------------------------------------------------------------
	t.Run("install_claude", func(t *testing.T) {
		out, errOut, err := run(t, root, "install", "claude")
		if err != nil {
			t.Fatalf("install: %v\n%s\n%s", err, out, errOut)
		}
		skill := filepath.Join(root, ".claude", "skills", "agent-memory", "SKILL.md")
		body, err := os.ReadFile(skill)
		if err != nil {
			t.Fatalf("SKILL.md not at %s: %v", skill, err)
		}
		// Spot-check the most behavior-critical sentence in the skill.
		for _, want := range []string{
			"memory.fetch_context",
			"memory.propose_update",
			"secret_detected",
		} {
			if !strings.Contains(string(body), want) {
				t.Errorf("SKILL.md missing %q", want)
			}
		}
	})

	// -------------------------------------------------------------------------
	// 3. fetch (bootstrap): the empty-query pack is non-empty
	// -------------------------------------------------------------------------
	t.Run("fetch_bootstrap", func(t *testing.T) {
		out, errOut, err := run(t, root, "fetch")
		if err != nil {
			t.Fatalf("fetch: %v\n%s\n%s", err, out, errOut)
		}
		if len(out) == 0 {
			t.Fatal("bootstrap pack is empty")
		}
		// Conventions are part of the bootstrap set.
		if !strings.Contains(out, "Conventions") {
			t.Errorf("bootstrap missing Conventions section:\n%s", out)
		}
	})

	// -------------------------------------------------------------------------
	// 4. MCP: spawn `agent-memory mcp`, list tools, call propose_update
	// -------------------------------------------------------------------------
	var stagingID string
	t.Run("mcp_propose_decision", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
		if err != nil {
			t.Fatalf("mcp connect: %v", err)
		}
		defer session.Close()

		tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		names := map[string]bool{}
		for _, tl := range tools.Tools {
			names[tl.Name] = true
		}
		for _, want := range []string{"memory.fetch_context", "memory.propose_update", "memory.status"} {
			if !names[want] {
				t.Errorf("tools/list missing %q (got %v)", want, names)
			}
		}

		// Stage a record_decision via the MCP tool.
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "memory.propose_update",
			Arguments: map[string]any{
				"intent":    "record_decision",
				"rationale": "e2e smoke decision",
				"sources":   []map[string]string{{"type": "user", "ref": "smoke-test"}},
				"operations": []map[string]any{
					{
						"operation":     "append_section",
						"path":          "decisions.md",
						"heading":       "E2E Smoke Decision",
						"heading_level": 2,
						"content":       "## E2E Smoke Decision\n<!-- @id: e2e-smoke -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nThis decision was made by the e2e smoke test.\n",
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if result.IsError {
			t.Fatalf("tool returned IsError=true: %+v", result.Content)
		}
		if result.StructuredContent == nil {
			t.Fatalf("expected structured content; got %+v", result)
		}
		// Re-marshal/unmarshal to access fields without a typed struct.
		b, _ := json.Marshal(result.StructuredContent)
		var resp struct {
			Status    string `json:"status"`
			StagingID string `json:"staging_id"`
			Reason    string `json:"reason,omitempty"`
			Message   string `json:"message,omitempty"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode response: %v\n%s", err, b)
		}
		if resp.Status != "staged" {
			t.Fatalf("Status = %q (reason=%s msg=%s), want staged", resp.Status, resp.Reason, resp.Message)
		}
		if resp.StagingID == "" {
			t.Fatal("StagingID empty in staged response")
		}
		stagingID = resp.StagingID
	})

	// -------------------------------------------------------------------------
	// 5. review: the proposal is visible to the CLI
	// -------------------------------------------------------------------------
	t.Run("review_list", func(t *testing.T) {
		var list struct {
			Proposals []struct {
				StagingID string `json:"staging_id"`
			} `json:"proposals"`
		}
		runJSON(t, &list, root, "review")
		found := false
		for _, p := range list.Proposals {
			if p.StagingID == stagingID {
				found = true
			}
		}
		if !found {
			t.Errorf("staging %q not in review list: %+v", stagingID, list)
		}
	})

	t.Run("review_detail_with_show", func(t *testing.T) {
		var d struct {
			Proposal struct {
				StagingID string `json:"staging_id"`
			} `json:"proposal"`
			Files map[string]string `json:"files"`
		}
		runJSON(t, &d, root, "review", stagingID, "--show")
		if d.Proposal.StagingID != stagingID {
			t.Errorf("Proposal.StagingID = %q, want %q", d.Proposal.StagingID, stagingID)
		}
		body, ok := d.Files["decisions.md"]
		if !ok {
			t.Fatalf("Files missing decisions.md: %+v", d.Files)
		}
		if !strings.Contains(body, "e2e-smoke") {
			t.Errorf("staged decisions.md missing e2e-smoke anchor:\n%s", body)
		}
	})

	// -------------------------------------------------------------------------
	// 6. apply: the staged proposal lands on disk
	// -------------------------------------------------------------------------
	t.Run("apply", func(t *testing.T) {
		var res struct {
			Status string   `json:"status"`
			Reason string   `json:"reason,omitempty"`
			Files  []string `json:"files,omitempty"`
		}
		runJSON(t, &res, root, "apply", stagingID)
		if res.Status != "applied" {
			t.Fatalf("Status = %q (reason=%s), want applied", res.Status, res.Reason)
		}
		body, err := os.ReadFile(filepath.Join(memDir, "decisions.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "e2e-smoke") {
			t.Errorf("decisions.md doesn't contain the applied decision:\n%s", body)
		}
		// Staging dir gone.
		if _, err := os.Stat(filepath.Join(memDir, "staging", stagingID)); err == nil {
			t.Errorf("staging dir still present after apply")
		}
	})

	// -------------------------------------------------------------------------
	// 7. status (CLI): category counts include the decision file + §15.11 blocks
	// -------------------------------------------------------------------------
	t.Run("status_reports_decisions", func(t *testing.T) {
		var r struct {
			Categories   map[string]int `json:"categories"`
			DurableFiles int            `json:"durable_files"`
			Security     struct {
				LastSecretScan string `json:"last_secret_scan"`
			} `json:"security"`
		}
		runJSON(t, &r, root, "status")
		if r.Categories["decisions"] < 1 {
			t.Errorf("status categories.decisions = %d, want >= 1: %+v",
				r.Categories["decisions"], r.Categories)
		}
		// §15.11 block flattened into the CLI status JSON.
		if r.DurableFiles < 1 {
			t.Errorf("status durable_files = %d, want >= 1", r.DurableFiles)
		}
		if r.Security.LastSecretScan == "" {
			t.Error("status JSON missing security.last_secret_scan (§15.11 block)")
		}
	})

	// -------------------------------------------------------------------------
	// 7c. index.md regeneration: the server-managed routing file reflects
	// the decision applied back in step 6 (e2e-smoke).
	// -------------------------------------------------------------------------
	t.Run("index_md_regenerated", func(t *testing.T) {
		body, err := os.ReadFile(filepath.Join(memDir, "index.md"))
		if err != nil {
			t.Fatalf("index.md missing: %v", err)
		}
		s := string(body)
		if !strings.Contains(s, "@generated") {
			t.Errorf("index.md missing @generated marker:\n%s", s)
		}
		// The applied e2e-smoke decision (Status: active) must show in the
		// topic-map decision summary.
		if !strings.Contains(s, "decisions.md — durable") || !strings.Contains(s, "active") {
			t.Errorf("index.md topic map doesn't reflect the applied decision:\n%s", s)
		}
	})

	// -------------------------------------------------------------------------
	// 7b. status (MCP): the third tool returns the §15.11 shape
	// -------------------------------------------------------------------------
	t.Run("mcp_status_tool", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
		if err != nil {
			t.Fatalf("mcp connect: %v", err)
		}
		defer session.Close()

		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "memory.status",
			Arguments: map[string]any{},
		})
		if err != nil {
			t.Fatalf("CallTool memory.status: %v", err)
		}
		if result.IsError {
			t.Fatalf("memory.status returned IsError: %+v", result.Content)
		}
		b, _ := json.Marshal(result.StructuredContent)
		var resp struct {
			Repo         string `json:"repo"`
			DurableFiles int    `json:"durable_files"`
			Security     struct {
				LastSecretScan string `json:"last_secret_scan"`
			} `json:"security"`
			Git struct {
				IgnoredLocalState bool `json:"ignored_local_state"`
			} `json:"git"`
			Lock struct {
				Held bool `json:"held"`
			} `json:"lock"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode status: %v\n%s", err, b)
		}
		if resp.Repo == "" {
			t.Error("memory.status repo is empty")
		}
		if resp.DurableFiles < 1 {
			t.Errorf("memory.status durable_files = %d, want >= 1", resp.DurableFiles)
		}
		if resp.Security.LastSecretScan == "" {
			t.Error("memory.status missing security.last_secret_scan")
		}
	})

	// -------------------------------------------------------------------------
	// 8. fetch by query: the applied decision is now searchable
	// -------------------------------------------------------------------------
	t.Run("fetch_query_finds_decision", func(t *testing.T) {
		out, errOut, err := run(t, root, "fetch", "smoke")
		if err != nil {
			t.Fatalf("fetch: %v\n%s\n%s", err, out, errOut)
		}
		if !strings.Contains(out, "E2E Smoke Decision") {
			t.Errorf("query 'smoke' didn't return the decision:\n%s", out)
		}
	})

	// -------------------------------------------------------------------------
	// 9. propose_update rejection: a secret-bearing body must be refused
	// -------------------------------------------------------------------------
	t.Run("mcp_propose_secret_is_rejected", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
		if err != nil {
			t.Fatalf("mcp connect: %v", err)
		}
		defer session.Close()

		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "memory.propose_update",
			Arguments: map[string]any{
				"intent": "update_current",
				"operations": []map[string]any{
					{
						"operation": "create_file",
						"path":      "local/current.shared.md",
						"content":   "# Current\n\nAWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n",
						"if_exists": "replace",
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		// Rejection MUST be in the response body, not a transport error.
		if result.IsError {
			t.Fatalf("MCP layer reported IsError; rejection should be in body: %+v", result.Content)
		}
		b, _ := json.Marshal(result.StructuredContent)
		var resp struct {
			Status   string `json:"status"`
			Reason   string `json:"reason"`
			Findings []struct {
				Type                string `json:"type"`
				Line                int    `json:"line"`
				ApproximateLocation string `json:"approximate_location"`
			} `json:"findings"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode: %v\n%s", err, b)
		}
		if resp.Status != "rejected" {
			t.Fatalf("Status = %q, want rejected", resp.Status)
		}
		if resp.Reason != "secret_detected" {
			t.Errorf("Reason = %q, want secret_detected", resp.Reason)
		}
		if len(resp.Findings) == 0 {
			t.Fatal("Findings empty")
		}
		// The matched bytes must NOT leak via type or location fields.
		for _, f := range resp.Findings {
			if strings.Contains(f.Type, "AKIA") || strings.Contains(f.ApproximateLocation, "AKIA") {
				t.Errorf("Finding leaked token bytes: %+v", f)
			}
		}
	})

	// -------------------------------------------------------------------------
	// 10. reject path: stage a fresh proposal, then `reject` it
	// -------------------------------------------------------------------------
	t.Run("reject_discards_staging", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
		if err != nil {
			t.Fatalf("mcp connect: %v", err)
		}
		defer session.Close()

		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "memory.propose_update",
			Arguments: map[string]any{
				"intent":    "record_decision",
				"rationale": "to be rejected",
				"sources":   []map[string]string{{"type": "user", "ref": "smoke"}},
				"operations": []map[string]any{
					{
						"operation":     "append_section",
						"path":          "decisions.md",
						"heading":       "Soon To Reject",
						"heading_level": 2,
						"content":       "## Soon To Reject\n<!-- @id: soon-to-reject -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nIrrelevant.\n",
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		b, _ := json.Marshal(result.StructuredContent)
		var resp struct {
			StagingID string `json:"staging_id"`
		}
		_ = json.Unmarshal(b, &resp)
		if resp.StagingID == "" {
			t.Fatalf("expected a staging_id, got %s", b)
		}

		// Now reject through the CLI.
		var rejRes struct {
			Status string `json:"status"`
		}
		runJSON(t, &rejRes, root, "reject", resp.StagingID)
		if !strings.HasPrefix(rejRes.Status, "rejected") {
			t.Errorf("Status = %q, want rejected_*", rejRes.Status)
		}
		if _, err := os.Stat(filepath.Join(memDir, "staging", resp.StagingID)); err == nil {
			t.Errorf("staging dir still exists after reject")
		}
	})

	// -------------------------------------------------------------------------
	// 11. archive_section (M4): stage → apply → both files on disk
	// -------------------------------------------------------------------------
	t.Run("mcp_archive_section_roundtrip", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
		if err != nil {
			t.Fatalf("mcp connect: %v", err)
		}
		defer session.Close()

		// Archive the decision we applied back in step 6 (e2e-smoke).
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "memory.propose_update",
			Arguments: map[string]any{
				"intent":  "archive_stale",
				"sources": []map[string]string{{"type": "user", "ref": "e2e-cleanup"}},
				"operations": []map[string]any{
					{
						"operation":    "archive_section",
						"path":         "decisions.md",
						"section_id":   "e2e-smoke",
						"archive_path": "archive/2026-05-e2e-smoke.md",
						"replacement":  "## E2E Smoke Decision\n<!-- @id: e2e-smoke -->\n\n**Date:** 2026-05-27\n**Status:** superseded\n**Confidence:** confirmed\n\nArchived: see `archive/2026-05-e2e-smoke.md`.\n",
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("CallTool archive_section: %v", err)
		}
		b, _ := json.Marshal(result.StructuredContent)
		var resp struct {
			Status    string `json:"status"`
			Reason    string `json:"reason"`
			StagingID string `json:"staging_id"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatalf("decode: %v\n%s", err, b)
		}
		// archive_section always stages.
		if resp.Status != "staged" {
			t.Fatalf("Status = %q (reason=%s), want staged", resp.Status, resp.Reason)
		}
		if resp.StagingID == "" {
			t.Fatal("no staging_id for archive_section")
		}

		// Apply through the CLI.
		var applyRes struct {
			Status string `json:"status"`
		}
		runJSON(t, &applyRes, root, "apply", resp.StagingID)
		if applyRes.Status != "applied" {
			t.Fatalf("apply Status = %q", applyRes.Status)
		}

		// Source decision now a stub; archive file holds the original.
		dec, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
		if !strings.Contains(string(dec), "Archived: see `archive/2026-05-e2e-smoke.md`") {
			t.Errorf("decision not archived in source:\n%s", dec)
		}
		arch, err := os.ReadFile(filepath.Join(memDir, "archive", "2026-05-e2e-smoke.md"))
		if err != nil {
			t.Fatalf("archive file not on disk: %v", err)
		}
		if !strings.Contains(string(arch), "This decision was made by the e2e smoke test.") {
			t.Errorf("archive missing original decision body:\n%s", arch)
		}
	})
}

// =============================================================================
// Latency sanity check: fetch should be fast on a small fixture.
//
// Not a formal benchmark — just a regression guard so a future change can't
// silently regress fetch from O(ms) to O(seconds). Tunable cap.
// =============================================================================

func TestRelease01_FetchLatencyUnderHalfSecond(t *testing.T) {
	root := t.TempDir()
	if _, errOut, err := run(t, root, "init", "--name", "latency-test"); err != nil {
		t.Fatalf("init: %v\n%s", err, errOut)
	}

	// Warm the index by running fetch once (it auto-builds on first call).
	if _, _, err := run(t, root, "fetch"); err != nil {
		t.Fatal(err)
	}

	const cap = 500 * time.Millisecond
	start := time.Now()
	if _, errOut, err := run(t, root, "fetch", "conventions"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, errOut)
	}
	elapsed := time.Since(start)
	if elapsed > cap {
		t.Errorf("warm fetch took %v, want < %v (regression?)", elapsed, cap)
	} else {
		t.Logf("warm fetch latency: %v (cap %v)", elapsed, cap)
	}
}
