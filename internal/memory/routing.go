package memory

import (
	"fmt"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// Intent classifies what KIND of change the agent wants to make. Each intent
// maps to a specific approval slot in manifest.updates.approval; routing.go
// holds that mapping in one place so the orchestrator never has to think
// about category-vs-intent semantics.
//
// The set is closed: an unknown intent is a hard reject ("invalid_intent")
// rather than a silent stage. This matches the design doc v0.4.1 §22.2
// intent enumeration.
type Intent string

const (
	IntentUpdateCurrent     Intent = "update_current"
	IntentUpdateShared      Intent = "update_shared"
	IntentSessionLog        Intent = "session_log"
	IntentAddPitfall        Intent = "add_pitfall"
	IntentRecordDecision    Intent = "record_decision"
	IntentRefreshModule     Intent = "refresh_module"
	IntentUpdateConventions Intent = "update_conventions"
	IntentArchiveStale      Intent = "archive_stale"
)

// AllIntents is the canonical list used by Validate / docs.
var AllIntents = []Intent{
	IntentUpdateCurrent,
	IntentUpdateShared,
	IntentSessionLog,
	IntentAddPitfall,
	IntentRecordDecision,
	IntentRefreshModule,
	IntentUpdateConventions,
	IntentArchiveStale,
}

// IsValidIntent reports whether i is a recognised Intent.
func IsValidIntent(i Intent) bool {
	for _, v := range AllIntents {
		if v == i {
			return true
		}
	}
	return false
}

// Routing is the resolved approval decision for one operation under one
// intent. Mode comes from the manifest's per-slot ApprovalPolicy; Reason
// is a human-readable trace of how we got there (which slot was consulted,
// any overrides, etc.) — surfaced in propose_update's response so the agent
// can learn why a proposal was staged vs applied.
type Routing struct {
	Mode   schema.ApprovalMode
	Reason string
}

// DecideRouting picks the approval slot for (intent, op) and returns the
// resolved Mode from the manifest plus a Reason trace.
//
// The intent → slot mapping (manifest.updates.approval.<slot>):
//
//	update_current       → current
//	update_shared        → current_shared
//	session_log          → sessions
//	add_pitfall + append_to_section → pitfalls_append
//	add_pitfall + anything else     → pitfalls_replace
//	record_decision      → decisions
//	refresh_module       → modules
//	update_conventions   → conventions
//	archive_stale        → archive
//
// add_pitfall is the only intent whose slot depends on the operation kind:
// append-style updates apply without review (low-risk, additive) while
// section-level rewrites require staging (high-risk, can drop knowledge).
// See manifest defaults in config.DefaultManifest.
//
// Returns an error only when the intent is not in the recognised set;
// callers should map that to a "reject: invalid_intent" response.
func DecideRouting(intent Intent, op Operation, manifest *config.Manifest) (Routing, error) {
	if manifest == nil {
		return Routing{}, fmt.Errorf("routing: manifest is required")
	}
	if !IsValidIntent(intent) {
		return Routing{}, fmt.Errorf("routing: unknown intent %q", intent)
	}

	pol := manifest.Updates.Approval
	switch intent {
	case IntentUpdateCurrent:
		return Routing{Mode: pol.Current, Reason: "intent=update_current → updates.approval.current"}, nil
	case IntentUpdateShared:
		return Routing{Mode: pol.CurrentShared, Reason: "intent=update_shared → updates.approval.current_shared"}, nil
	case IntentSessionLog:
		return Routing{Mode: pol.Sessions, Reason: "intent=session_log → updates.approval.sessions"}, nil
	case IntentAddPitfall:
		if op != nil && op.Kind() == "append_to_section" {
			return Routing{Mode: pol.PitfallsAppend, Reason: "intent=add_pitfall op=append_to_section → updates.approval.pitfalls_append"}, nil
		}
		return Routing{Mode: pol.PitfallsReplace, Reason: "intent=add_pitfall op≠append_to_section → updates.approval.pitfalls_replace"}, nil
	case IntentRecordDecision:
		return Routing{Mode: pol.Decisions, Reason: "intent=record_decision → updates.approval.decisions"}, nil
	case IntentRefreshModule:
		return Routing{Mode: pol.Modules, Reason: "intent=refresh_module → updates.approval.modules"}, nil
	case IntentUpdateConventions:
		return Routing{Mode: pol.Conventions, Reason: "intent=update_conventions → updates.approval.conventions"}, nil
	case IntentArchiveStale:
		return Routing{Mode: pol.Archive, Reason: "intent=archive_stale → updates.approval.archive"}, nil
	}
	// Unreachable: IsValidIntent above gates the switch.
	return Routing{}, fmt.Errorf("routing: unhandled intent %q", intent)
}

// CombineRoutings merges per-op Routings into a single proposal-level
// decision. Rules (most restrictive wins):
//
//   - any server_only → reject (the orchestrator translates this to a
//     "server_only_category" rejection; routing itself just reports the mode).
//   - any stage → stage.
//   - else → apply.
//
// Reason is the concatenation of contributing routings' reasons.
func CombineRoutings(routings []Routing) Routing {
	if len(routings) == 0 {
		return Routing{Mode: schema.ApprovalApply, Reason: "no operations"}
	}
	mode := schema.ApprovalApply
	var reasons []string
	for i, r := range routings {
		reasons = append(reasons, fmt.Sprintf("op[%d]: %s → %s", i, r.Reason, r.Mode))
		switch r.Mode {
		case schema.ApprovalServerOnly:
			mode = schema.ApprovalServerOnly
		case schema.ApprovalStage:
			if mode != schema.ApprovalServerOnly {
				mode = schema.ApprovalStage
			}
		}
	}
	return Routing{Mode: mode, Reason: joinReasons(reasons)}
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}
