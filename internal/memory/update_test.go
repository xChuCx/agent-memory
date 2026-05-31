package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// updateFixture builds a minimal .agent-memory/ tree for orchestrator tests:
//
//   <tmp>/.agent-memory/
//     meta/                    (lock + sidecars)
//     local/                   (current.shared.md, etc.)
//     sessions/                (per-day session logs)
//     decisions.md             (empty file the tests can write to)
//     pitfalls.md
//     modules/
//
// Returns the absolute memDir path, a default manifest, and a default schema.
func updateFixture(t *testing.T) (memDir string, mf *config.Manifest, sch *schema.Schema) {
	t.Helper()
	dir := t.TempDir()
	memDir = filepath.Join(dir, ".agent-memory")
	for _, sub := range []string{
		"meta",
		"local",
		"sessions",
		"modules",
		"archive",
	} {
		if err := os.MkdirAll(filepath.Join(memDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Seed an existing file with a section so AppendToSection can resolve it.
	seedPitfalls := []byte("# Pitfalls\n\n## Stale Lock\n<!-- @id: stale-lock -->\n\nWatch out.\n")
	if err := os.WriteFile(filepath.Join(memDir, "pitfalls.md"), seedPitfalls, 0644); err != nil {
		t.Fatal(err)
	}
	seedDecisions := []byte("# Decisions\n")
	if err := os.WriteFile(filepath.Join(memDir, "decisions.md"), seedDecisions, 0644); err != nil {
		t.Fatal(err)
	}
	seedConventions := []byte("# Conventions\n")
	if err := os.WriteFile(filepath.Join(memDir, "conventions.md"), seedConventions, 0644); err != nil {
		t.Fatal(err)
	}
	// Local current file (per-branch + shared kept simple).
	if err := os.WriteFile(filepath.Join(memDir, "local", "current.shared.md"),
		[]byte("# Current\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mf = config.DefaultManifest()
	mf.Project.Name = "test"
	sch = schema.DefaultSchema()
	return memDir, mf, sch
}

// =============================================================================
// Happy paths
// =============================================================================

func TestProposeUpdate_ApplyImmediately_Current(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	req := ProposeRequest{
		Intent: IntentUpdateCurrent,
		Operations: []OperationInput{
			{
				Op:       "create_file",
				Path:     "local/current.shared.md",
				Content:  "# Current\n\nNew body.\n",
				IfExists: "replace",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("ProposeUpdate err: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s), want applied", resp.Status, resp.Reason)
	}

	got, err := os.ReadFile(filepath.Join(memDir, "local/current.shared.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "New body.") {
		t.Errorf("apply did not write expected body, got: %q", got)
	}
}

func TestProposeUpdate_AppendToSection_PitfallsApplies(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	req := ProposeRequest{
		Intent: IntentAddPitfall,
		Operations: []OperationInput{
			{
				Op:        "append_to_section",
				Path:      "pitfalls.md",
				SectionID: "stale-lock",
				Content:   "- new bullet about lock\n",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q reason=%s msg=%s", resp.Status, resp.Reason, resp.Message)
	}

	got, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	if !strings.Contains(string(got), "new bullet about lock") {
		t.Errorf("expected bullet appended, got %q", got)
	}
}

func TestProposeUpdate_RecordDecision_Stages(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	req := ProposeRequest{
		Intent:    IntentRecordDecision,
		Rationale: "use postgres",
		Sources:   []Source{{Type: "user", Ref: "interview-2026-05-27"}},
		Operations: []OperationInput{
			{
				Op:           "append_section",
				Path:         "decisions.md",
				Heading:      "Use Postgres",
				HeadingLevel: 2,
				Content:      "## Use Postgres\n<!-- @id: use-postgres -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nGo with Postgres.\n",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q (%s/%s), want staged", resp.Status, resp.Reason, resp.Message)
	}
	if resp.StagingID == "" {
		t.Error("StagingID is empty on staged response")
	}

	// Original file should NOT yet contain the new section (only staged).
	got, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if strings.Contains(string(got), "use-postgres") {
		t.Errorf("decision was applied directly; should have been staged. got %q", got)
	}

	// Staged artefacts present.
	stagedFile := filepath.Join(memDir, "staging", resp.StagingID, "files", "decisions.md")
	if _, err := os.Stat(stagedFile); err != nil {
		t.Errorf("staged file missing: %v", err)
	}
	proposal := filepath.Join(memDir, "staging", resp.StagingID, "proposal.json")
	if _, err := os.Stat(proposal); err != nil {
		t.Errorf("proposal.json missing: %v", err)
	}
	checksums := filepath.Join(memDir, "staging", resp.StagingID, "target-checksums.json")
	if _, err := os.Stat(checksums); err != nil {
		t.Errorf("target-checksums.json missing: %v", err)
	}

	// proposal.json must round-trip.
	pb, _ := os.ReadFile(proposal)
	var env StagedProposal
	if err := json.Unmarshal(pb, &env); err != nil {
		t.Errorf("proposal.json malformed: %v", err)
	}
	if env.Request.Intent != IntentRecordDecision {
		t.Errorf("staged proposal intent = %q, want record_decision", env.Request.Intent)
	}
}

func TestProposeUpdate_SessionLog_RewritesPathToToday(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	req := ProposeRequest{
		Intent: IntentSessionLog,
		Operations: []OperationInput{
			{
				Op:       "create_file",
				Path:     "ignored/by-rewrite.md",
				Content:  "# Today\n\nFirst entry.\n",
				IfExists: "append",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s)", resp.Status, resp.Reason)
	}

	want := "sessions/" + time.Now().UTC().Format("2006-01-02") + ".md"
	abs := filepath.Join(memDir, filepath.FromSlash(want))
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("session file not at rewritten path %s: %v", want, err)
	}
	// And the response should reflect the rewritten path.
	if len(resp.Files) == 0 || resp.Files[0] != want {
		t.Errorf("resp.Files = %v, want [%s]", resp.Files, want)
	}
}

func TestProposeUpdate_SessionLog_HonorsExplicitSessionsPath(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	// Agent provides an explicit session path → orchestrator does NOT rewrite.
	explicit := "sessions/2026-05-26.md"
	req := ProposeRequest{
		Intent: IntentSessionLog,
		Operations: []OperationInput{
			{
				Op:       "create_file",
				Path:     explicit,
				Content:  "# Yesterday\n",
				IfExists: "replace",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s)", resp.Status, resp.Reason)
	}
	if resp.Files[0] != explicit {
		t.Errorf("explicit path was overwritten: Files = %v, want [%s]", resp.Files, explicit)
	}
}

// =============================================================================
// Rejection paths — each reason has its own minimal repro
// =============================================================================

func TestProposeUpdate_RejectsInvalidIntent(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	req := ProposeRequest{
		Intent: Intent("not_real"),
		Operations: []OperationInput{
			{Op: "create_file", Path: "decisions.md", Content: "# d\n", IfExists: "replace"},
		},
	}
	resp, _ := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonInvalidIntent {
		t.Errorf("Reason = %q, want %q", resp.Reason, ReasonInvalidIntent)
	}
}

func TestProposeUpdate_RejectsNoOperations(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{Intent: IntentUpdateCurrent},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonNoOperations {
		t.Errorf("Reason = %q, want %q", resp.Reason, ReasonNoOperations)
	}
}

func TestProposeUpdate_RejectsInvalidOperation(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{Op: "not_a_real_op", Path: "local/current.shared.md"},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonInvalidOperation {
		t.Errorf("Reason = %q, want %q", resp.Reason, ReasonInvalidOperation)
	}
}

func TestProposeUpdate_RejectsInvalidPath(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{Op: "create_file", Path: "../escape.md", Content: "# x\n", IfExists: "replace"},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonInvalidPath {
		t.Errorf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonInvalidPath, resp.Message)
	}
}

func TestProposeUpdate_RejectsUnknownCategory(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{Op: "create_file", Path: "random/where.md", Content: "# x\n", IfExists: "replace"},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonUnknownCategory {
		t.Errorf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonUnknownCategory, resp.Message)
	}
}

func TestProposeUpdate_RejectsServerManagedCategory(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{Op: "create_file", Path: "index.md", Content: "# index\n", IfExists: "replace"},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonServerManagedCategory {
		t.Errorf("Reason = %q, want %q", resp.Reason, ReasonServerManagedCategory)
	}
}

func TestProposeUpdate_RejectsSecretDetected(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nKey: AKIAIOSFODNN7EXAMPLE\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonSecretDetected {
		t.Fatalf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonSecretDetected, resp.Message)
	}
	if len(resp.Findings) == 0 {
		t.Error("expected Findings populated on secret rejection")
	}
	for _, f := range resp.Findings {
		if strings.Contains(f.Type, "AKIA") || strings.Contains(f.ApproximateLocation, "AKIA") {
			t.Errorf("finding leaked token bytes: %+v", f)
		}
	}
}

func TestProposeUpdate_RejectsProvenanceMissing(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	// Decisions category requires sources. Omit them.
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentRecordDecision,
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Adopt Foo",
					HeadingLevel: 2,
					Content:      "## Adopt Foo\n<!-- @id: adopt-foo -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nFoo it is.\n",
				},
			},
			// no Sources
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonProvenanceViolation {
		t.Fatalf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonProvenanceViolation, resp.Message)
	}
	if len(resp.ProvenanceViolations) == 0 {
		t.Error("expected ProvenanceViolations populated")
	}
}

func TestProposeUpdate_RejectsForbiddenSourceType(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:  IntentRecordDecision,
			Sources: []Source{{Type: "external", Ref: "https://blog.example.com"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Adopt Bar",
					HeadingLevel: 2,
					Content:      "## Adopt Bar\n<!-- @id: adopt-bar -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nBar it is.\n",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonProvenanceViolation {
		t.Errorf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonProvenanceViolation, resp.Message)
	}
}

// =============================================================================
// Multi-op proposal: ops on same file applied sequentially in memory
// =============================================================================

func TestProposeUpdate_MultiOpSameFile_SequentialPlanning(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	// 1) Create local/current.shared.md fresh.
	// 2) Append a section to it.
	//
	// Step 2's Plan must see step 1's bytes (the freshly-written body),
	// not the on-disk bytes (which still hold the seed "# Current\n").
	req := ProposeRequest{
		Intent: IntentUpdateCurrent,
		Operations: []OperationInput{
			{
				Op:       "create_file",
				Path:     "local/current.shared.md",
				Content:  "# Current\n",
				IfExists: "replace",
			},
			{
				Op:           "append_section",
				Path:         "local/current.shared.md",
				Heading:      "Sub",
				HeadingLevel: 2,
				Content:      "## Sub\n\nSub body.\n",
			},
		},
	}
	resp, err := ProposeUpdate(context.Background(), req,
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}
	got, _ := os.ReadFile(filepath.Join(memDir, "local/current.shared.md"))
	if !strings.Contains(string(got), "## Sub") {
		t.Errorf("post-state missing appended section: %q", got)
	}
	if !strings.Contains(string(got), "# Current") {
		t.Errorf("post-state missing top heading: %q", got)
	}
}

// =============================================================================
// Routing surfaces in response
// =============================================================================

func TestProposeUpdate_ResponseCarriesRouting(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{Op: "create_file", Path: "local/current.shared.md", Content: "# C\n", IfExists: "replace"},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Routing.Mode != schema.ApprovalApply {
		t.Errorf("Routing.Mode = %q, want apply", resp.Routing.Mode)
	}
	if !strings.Contains(resp.Routing.Reason, "update_current") {
		t.Errorf("Routing.Reason missing intent trace: %q", resp.Routing.Reason)
	}
}

// =============================================================================
// makeStagingID smoke
// =============================================================================

// =============================================================================
// Hardening: allowlist limits + PII detection through orchestrator
// =============================================================================

func TestProposeUpdate_RejectsPII_SSN(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	// PIIScan is on in DefaultManifest, so this should reject.
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nNote: SSN 123-45-6789 leaked here.\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonPIIDetected {
		t.Fatalf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonPIIDetected, resp.Message)
	}
	if len(resp.Findings) == 0 {
		t.Error("Findings should be populated on pii_detected")
	}
	// No SSN bytes in any field — same leak guard as secret scanner.
	for _, f := range resp.Findings {
		for _, field := range []string{f.Type, f.ApproximateLocation} {
			if strings.Contains(field, "123-45") {
				t.Errorf("PII finding leaked digits via %q field", field)
			}
		}
	}
}

func TestProposeUpdate_RejectsPII_CreditCard(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nTest card: 4242 4242 4242 4242\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonPIIDetected {
		t.Fatalf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonPIIDetected, resp.Message)
	}
}

func TestProposeUpdate_MixedSecretAndPII_ReportsSecret(t *testing.T) {
	// When BOTH a credential AND PII are present, the orchestrator
	// reports secret_detected (most severe wins).
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nKey: AKIAIOSFODNN7EXAMPLE\nSSN: 123-45-6789\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonSecretDetected {
		t.Errorf("Reason = %q, want secret_detected (secret should win over PII)", resp.Reason)
	}
}

func TestProposeUpdate_AllowsCleanEmailByDefault(t *testing.T) {
	// PIIScanEmail is OFF by default — legitimate emails in
	// documentation shouldn't trigger rejection.
	memDir, mf, sch := updateFixture(t)
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nContact maintainer@example.com for help.\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Status != StatusApplied {
		t.Errorf("email + email-scan-off should apply; got Status=%q Reason=%q",
			resp.Status, resp.Reason)
	}
}

func TestProposeUpdate_RejectsAllowlistLimitExceeded(t *testing.T) {
	memDir, mf, sch := updateFixture(t)

	// Build a body with one huge allowlist region — exceeds the
	// default MaxBytesPerRegion (512). 600 bytes inside.
	bigBody := strings.Repeat("documentation example token format. ", 17) // ~600 chars
	src := "# Current\n\n" +
		"<!-- @secret-scan: allow reason=\"docs\" -->\n" +
		bigBody +
		"\n<!-- @secret-scan: end -->\n"
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  src,
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Reason != ReasonAllowlistLimitExceeded {
		t.Fatalf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonAllowlistLimitExceeded, resp.Message)
	}
	if !strings.Contains(resp.Message, "max allowed = 512") {
		t.Errorf("message should mention the breached limit: %q", resp.Message)
	}
}

func TestProposeUpdate_AllowlistLimits_CanBeDisabled(t *testing.T) {
	// Setting all limits to 0 disables the check entirely. Useful
	// escape hatch for repos with legitimate need for big allowlist
	// regions (rare).
	memDir, mf, sch := updateFixture(t)
	mf.Security.AllowlistLimits.MaxBytesPerFile = 0
	mf.Security.AllowlistLimits.MaxRegionsPerFile = 0
	mf.Security.AllowlistLimits.MaxBytesPerRegion = 0

	bigBody := strings.Repeat("documentation token format example. ", 30) // ~1.1 KB
	src := "# Current\n\n" +
		"<!-- @secret-scan: allow reason=\"docs\" -->\n" +
		bigBody +
		"\n<!-- @secret-scan: end -->\n"
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  src,
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if resp.Status != StatusApplied {
		t.Errorf("zero limits should disable the check; got Status=%q Reason=%q Msg=%s",
			resp.Status, resp.Reason, resp.Message)
	}
}

func TestMakeStagingID_Format(t *testing.T) {
	id := makeStagingID(ProposeRequest{
		Intent:    IntentRecordDecision,
		Rationale: "Use Postgres for write-heavy storage",
	})
	if !strings.Contains(id, "-record-decision") {
		t.Errorf("id = %q, want intent slug in it", id)
	}
	// Timestamp prefix is 15 chars (YYYYMMDDTHHMMSS).
	if len(id) < 16 {
		t.Errorf("id = %q, too short", id)
	}
}

func TestSlugify_TruncatesAndTrims(t *testing.T) {
	out := slugify("Some Long Sentence That Should Truncate Cleanly!!!", 20)
	if len(out) > 20 {
		t.Errorf("slug = %q, exceeds maxLen", out)
	}
	if strings.HasSuffix(out, "-") {
		t.Errorf("slug = %q, has trailing dash", out)
	}
}

func TestSlugify_OnlyLowercaseAlphanum(t *testing.T) {
	out := slugify("Hello, WORLD/42!", 40)
	if out != "hello-world-42" {
		t.Errorf("slug = %q, want hello-world-42", out)
	}
}
