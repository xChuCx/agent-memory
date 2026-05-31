// Memory-continuity eval: a deterministic, no-LLM measurement of the
// behaviour agent-memory exists for — does a lesson recorded in one session
// survive into the next session's context?
//
// Each scenario runs the REAL write→persist→retrieve loop: "session 1"
// records a lesson through memory.ProposeUpdate (and ApplyStaged when the
// category stages), exactly as an agent would; a fresh "session 2" then
// calls memory.BuildContextPack and we check the lesson is in the pack the
// agent would receive.
//
// WITH memory the lesson is available to session 2; WITHOUT it (the lesson
// was never persisted, because there was no memory layer) it is not. This
// is the necessary precondition for an agent to avoid repeating a recorded
// mistake — it is NOT a claim that an LLM acted on it. The task-success
// number (did the agent actually avoid the mistake) needs an LLM in the
// loop: see eval/behavioural/.
//
//	go test -run TestMemoryContinuity -v ./internal/eval/
package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/index"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// lesson is one cross-session scenario: what session 1 records, and the
// query + marker that prove session 2 can see it.
type lesson struct {
	name    string
	request memory.ProposeRequest // how session 1 records the lesson
	query   string                // session 2's follow-up task
	marker  string                // must appear in session 2's pack iff memory carried the lesson
}

var lessons = []lesson{
	{
		name: "pitfall: cookie SameSite breaks OAuth",
		request: memory.ProposeRequest{
			Intent: memory.IntentAddPitfall,
			Operations: []memory.OperationInput{{
				Op:        "append_to_section",
				Path:      "pitfalls.md",
				SectionID: "pitfalls",
				Content:   "- Cookie SameSite=Lax breaks the OAuth redirect in Safari; use None+Secure.\n",
			}},
		},
		query:  "cookie samesite oauth redirect safari",
		marker: "SameSite",
	},
	{
		name: "pitfall: kafka rebalance drops in-flight work",
		request: memory.ProposeRequest{
			Intent: memory.IntentAddPitfall,
			Operations: []memory.OperationInput{{
				Op:        "append_to_section",
				Path:      "pitfalls.md",
				SectionID: "pitfalls",
				Content:   "- Kafka consumer rebalance drops in-flight messages; commit the offset only after the handler succeeds.\n",
			}},
		},
		query:  "kafka consumer rebalance offset in-flight",
		marker: "rebalance",
	},
	{
		name: "decision: use Postgres for orders",
		request: memory.ProposeRequest{
			Intent:     memory.IntentRecordDecision,
			Rationale:  "orders datastore choice",
			Sources:    []memory.Source{{Type: "user", Ref: "design-review"}},
			Confidence: "confirmed",
			Operations: []memory.OperationInput{{
				Op:           "append_section",
				Path:         "decisions.md",
				Heading:      "Use Postgres for the orders store",
				HeadingLevel: 2,
				Content:      "## Use Postgres for the orders store\n<!-- @id: dec-postgres -->\n\n**Date:** 2026-05-31\n**Status:** active\n**Confidence:** confirmed\n\nChose Postgres over MySQL: transactional guarantees and JSONB for order payloads.\n",
			}},
		},
		query:  "which database did we choose for orders",
		marker: "Postgres",
	},
	{
		name: "decision: short-lived JWT for service auth",
		request: memory.ProposeRequest{
			Intent:     memory.IntentRecordDecision,
			Rationale:  "service auth mechanism",
			Sources:    []memory.Source{{Type: "user", Ref: "design-review"}},
			Confidence: "confirmed",
			Operations: []memory.OperationInput{{
				Op:           "append_section",
				Path:         "decisions.md",
				Heading:      "Short-lived JWT for service auth",
				HeadingLevel: 2,
				Content:      "## Short-lived JWT for service auth\n<!-- @id: dec-jwt -->\n\n**Date:** 2026-05-31\n**Status:** active\n**Confidence:** confirmed\n\nServices authenticate with short-lived rotating JWTs; no long-lived shared secrets.\n",
			}},
		},
		query:  "how do services authenticate with each other",
		marker: "JWT",
	},
	{
		name: "pitfall: postgres pool exhaustion under load",
		request: memory.ProposeRequest{
			Intent: memory.IntentAddPitfall,
			Operations: []memory.OperationInput{{
				Op:        "append_to_section",
				Path:      "pitfalls.md",
				SectionID: "pitfalls",
				Content:   "- Postgres connection pool exhausts under burst load and requests hang; cap max connections and front it with pgbouncer.\n",
			}},
		},
		query:  "postgres connection pool exhausted under load",
		marker: "pgbouncer",
	},
}

func TestMemoryContinuity(t *testing.T) {
	withMem, withoutMem := 0, 0
	for _, l := range lessons {
		// WITH memory: session 1 records the lesson, session 2 fetches.
		s := newStore(t)
		recordLesson(t, s, l)
		if fetchHasMarker(t, s, l.query, l.marker) {
			withMem++
		} else {
			t.Errorf("[with memory] %q: marker %q not in session-2 pack", l.name, l.marker)
		}

		// WITHOUT memory: the lesson was never recorded (no memory layer);
		// session 2 starts blank.
		blank := newStore(t)
		if fetchHasMarker(t, blank, l.query, l.marker) {
			withoutMem++
			t.Errorf("[without memory] %q: marker %q unexpectedly present", l.name, l.marker)
		}
	}

	n := len(lessons)
	t.Logf("\nMemory continuity — %d cross-session scenarios (record → persist → retrieve)\n", n)
	t.Logf("  prior-session knowledge available to the next session:")
	t.Logf("    with agent-memory:    %d/%d", withMem, n)
	t.Logf("    without (no memory):  %d/%d", withoutMem, n)

	if withMem != n {
		t.Errorf("with memory: %d/%d lessons carried across sessions, want %d", withMem, n, n)
	}
	if withoutMem != 0 {
		t.Errorf("without memory: %d/%d carried, want 0 (baseline)", withoutMem, n)
	}
}

// --- store scaffolding (real .agent-memory + index) ---

type store struct {
	dir string
	idx *index.Index
	mf  *config.Manifest
	sch *schema.Schema
}

func newStore(t *testing.T) *store {
	t.Helper()
	root := t.TempDir()
	memDir := filepath.Join(root, ".agent-memory")
	for _, sub := range []string{"meta", "modules", "local", "sessions", "staging", "archive"} {
		if err := os.MkdirAll(filepath.Join(memDir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := schema.WriteDefault(filepath.Join(memDir, "meta", "schema.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteDefault(filepath.Join(memDir, "meta", "manifest.yaml"), "continuity-eval"); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"conventions.md": "# Conventions\n<!-- @id: conventions -->\n\nProject conventions.\n",
		"decisions.md":   "# Decisions\n<!-- @id: decisions -->\n\nDurable decisions land here.\n",
		"pitfalls.md":    "# Pitfalls\n<!-- @id: pitfalls -->\n\nKnown traps land here.\n",
		"index.md":       "# Agent Memory Index\n<!-- @generated -->\n\n(empty)\n",
	}
	for rel, body := range stubs {
		if err := os.WriteFile(filepath.Join(memDir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	ctx := context.Background()
	if err := idx.Init(ctx); err != nil {
		t.Fatal(err)
	}
	sch := schema.DefaultSchema()
	if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{}); err != nil {
		t.Fatal(err)
	}
	return &store{dir: memDir, idx: idx, mf: config.DefaultManifest(), sch: sch}
}

// recordLesson runs session 1: propose the lesson, and apply it if the
// category routed to staging — the full agent-write path.
func recordLesson(t *testing.T, s *store, l lesson) {
	t.Helper()
	ctx := context.Background()
	deps := memory.UpdateDeps{Manifest: s.mf, Schema: s.sch, MemoryDir: s.dir, Idx: s.idx}
	resp, err := memory.ProposeUpdate(ctx, l.request, deps)
	if err != nil {
		t.Fatalf("%q: ProposeUpdate: %v", l.name, err)
	}
	switch resp.Status {
	case memory.StatusApplied:
		// done (e.g. add_pitfall append)
	case memory.StatusStaged:
		if _, err := memory.ApplyStaged(ctx, resp.StagingID, deps); err != nil {
			t.Fatalf("%q: ApplyStaged: %v", l.name, err)
		}
	default:
		t.Fatalf("%q: unexpected status %q (%s/%s)", l.name, resp.Status, resp.Reason, resp.Message)
	}
}

// fetchHasMarker runs session 2: build the context pack for the follow-up
// query and report whether the recorded lesson is in it.
func fetchHasMarker(t *testing.T, s *store, query, marker string) bool {
	t.Helper()
	resp, err := memory.BuildContextPack(context.Background(),
		memory.FetchRequest{Query: query},
		memory.FetchDeps{Idx: s.idx, Schema: s.sch, Manifest: s.mf, MemoryDir: s.dir, Branch: git.BranchInfo{}})
	if err != nil {
		t.Fatalf("BuildContextPack(%q): %v", query, err)
	}
	return strings.Contains(resp.Context, marker)
}
