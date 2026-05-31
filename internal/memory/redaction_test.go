package memory

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/logging"
)

// These tests are the executable form of the design's secret-safety rule
// (§13.2 / §23.3): structured logs may record stable reason codes and
// counts, but NEVER the matched credential/PII bytes. We capture every
// record the production text handler would emit (at Debug, so nothing is
// filtered) into a buffer and assert the sensitive bytes are absent while
// the outcome was still logged.

// leakProbeSecret is a canonical AWS access-key-id shaped token. Declared
// once so the injected content and the absence assertion can't drift.
const leakProbeSecret = "AKIAIOSFODNN7EXAMPLE"

// leakProbeCard is a Luhn-valid test card number; PII scanning rejects it.
const leakProbeCard = "4242 4242 4242 4242"

// captureLogger builds the same text logger the CLI/MCP use, but writes to
// buf at Debug level so the test sees every line (entry + terminal).
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return logging.New(buf, slog.LevelDebug)
}

func TestProposeUpdate_LogsNeverLeakSecretBytes(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	var buf bytes.Buffer

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nKey: " + leakProbeSecret + "\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir, Logger: captureLogger(&buf)})
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Reason != ReasonSecretDetected {
		t.Fatalf("Reason = %q, want %q", resp.Reason, ReasonSecretDetected)
	}

	logs := buf.String()
	// Guard against vacuous success: the terminal log must actually have
	// fired and recorded the (safe) reason code.
	if !strings.Contains(logs, "propose_update rejected") {
		t.Fatalf("expected terminal rejection log; captured:\n%s", logs)
	}
	if !strings.Contains(logs, string(ReasonSecretDetected)) {
		t.Errorf("expected reason code in log; captured:\n%s", logs)
	}
	// The actual point: no credential bytes, in full or via the AKIA prefix.
	if strings.Contains(logs, leakProbeSecret) || strings.Contains(logs, "AKIA") {
		t.Errorf("secret bytes leaked into logs:\n%s", logs)
	}
}

func TestProposeUpdate_LogsNeverLeakPIIBytes(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	var buf bytes.Buffer

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nCard: " + leakProbeCard + "\n",
					IfExists: "replace",
				},
			},
		},
		UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir, Logger: captureLogger(&buf)})
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Reason != ReasonPIIDetected {
		t.Fatalf("Reason = %q, want %q", resp.Reason, ReasonPIIDetected)
	}

	logs := buf.String()
	if !strings.Contains(logs, string(ReasonPIIDetected)) {
		t.Fatalf("expected reason code in log; captured:\n%s", logs)
	}
	// No card digits, spaced or compact.
	if strings.Contains(logs, leakProbeCard) || strings.Contains(logs, strings.ReplaceAll(leakProbeCard, " ", "")) {
		t.Errorf("PII digits leaked into logs:\n%s", logs)
	}
}

// TestBuildContextPack_DoesNotLogQuery locks in the deliberate decision in
// fetch.go to omit the raw query from the served-summary log: a query is
// agent-controlled free text and could itself echo a secret the caller is
// searching for. The summary records mode + counts + budget only.
func TestBuildContextPack_DoesNotLogQuery(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()
	var buf bytes.Buffer
	deps.Logger = captureLogger(&buf)

	_, err := BuildContextPack(context.Background(), FetchRequest{Query: leakProbeSecret}, deps)
	if err != nil {
		t.Fatalf("BuildContextPack: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "fetch_context served") {
		t.Fatalf("expected served-summary log; captured:\n%s", logs)
	}
	if strings.Contains(logs, leakProbeSecret) || strings.Contains(logs, "AKIA") {
		t.Errorf("fetch logged the raw query:\n%s", logs)
	}
}
