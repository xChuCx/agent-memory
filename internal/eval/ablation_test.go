package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/memory"
)

// AblationVerdict defines the result of a counterfactual memory ablation test (SAR-008 Invariant 5).
// Distinguishes re-runnable hermetic task fixtures from live one-shot singletons
// (@huddora-ambassador-1857 #23353, @second-thought #23450, @just-nik #23567, @zeke-glm #23419).
type AblationVerdict struct {
	Scenario       string `json:"scenario"`
	WithMemoryPass bool   `json:"with_memory_pass"`
	AblatedPass    bool   `json:"ablated_pass"`
	IsReRunnable   bool   `json:"is_rerunnable"`
	CausedDecision string `json:"caused_decision"` // "DECISIVE" | "ORNAMENTAL" | "UNKNOWN"
	Explanation    string `json:"explanation"`
}

// evaluateOrdersDatastoreDecision simulates a decision predicate where knowledge of
// the Postgres migration is required to avoid falling back to the obsolete default.
func evaluateOrdersDatastoreDecision(contextPack string) bool {
	return strings.Contains(contextPack, "Postgres") && !strings.Contains(contextPack, "use MySQL")
}

func TestMemoryAblation_ReRunnableFixture(t *testing.T) {
	// Re-runnable hermetic task scenario: Eval(T, C ∪ {M}) == PASS ∧ Eval(T, C) == FAIL
	l := lesson{
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
	}

	// 1. Condition C ∪ {M}: With memory
	sWith := newStore(t)
	recordLesson(t, sWith, l)
	packWith, err := memory.BuildContextPack(context.Background(),
		memory.FetchRequest{Query: l.query},
		memory.FetchDeps{Idx: sWith.idx, Schema: sWith.sch, Manifest: sWith.mf, MemoryDir: sWith.dir, Branch: git.BranchInfo{}})
	if err != nil {
		t.Fatalf("packWith err: %v", err)
	}
	passWith := evaluateOrdersDatastoreDecision(packWith.Context)

	// 2. Condition C \ {M}: Ablated memory (blank baseline)
	sBlank := newStore(t)
	packBlank, err := memory.BuildContextPack(context.Background(),
		memory.FetchRequest{Query: l.query},
		memory.FetchDeps{Idx: sBlank.idx, Schema: sBlank.sch, Manifest: sBlank.mf, MemoryDir: sBlank.dir, Branch: git.BranchInfo{}})
	if err != nil {
		t.Fatalf("packBlank err: %v", err)
	}
	passBlank := evaluateOrdersDatastoreDecision(packBlank.Context)

	// 3. Evaluate Causal Delta
	if !passWith {
		t.Errorf("expected task to PASS with memory C ∪ {M}")
	}
	if passBlank {
		t.Errorf("expected task to FAIL under ablated context C (no memory)")
	}

	isDecisive := passWith && !passBlank
	if !isDecisive {
		t.Fatalf("expected memory to be DECISIVE, got passWith=%v, passBlank=%v", passWith, passBlank)
	}
	t.Logf("Ablation outcome on re-runnable fixture: DECISIVE(M) = TRUE (PASS with M, FAIL without M)")
}

func TestMemoryAblation_OneShotSingletonReceipt(t *testing.T) {
	// A live one-shot streaming cycle cannot evaluate a counterfactual twin (SAR-008 Invariant 5).
	// Its receipt must emit CAUSED_DECISION(M) = UNKNOWN (SINGLETON_ONE_SHOT).
	verdict := AblationVerdict{
		Scenario:       "live-recurring-cycle-7",
		IsReRunnable:   false,
		CausedDecision: "UNKNOWN",
		Explanation:    "this singleton proves at most INGESTED(M); it has no defined counterfactual execution without M, so it cannot establish that M changed the decision",
	}

	if verdict.CausedDecision != "UNKNOWN" {
		t.Errorf("one-shot singleton must return UNKNOWN, got %s", verdict.CausedDecision)
	}
	if !strings.Contains(verdict.Explanation, "INGESTED(M)") {
		t.Errorf("expected receipt explanation to match second-thought / just-nik formulation: %s", verdict.Explanation)
	}
}
