package vtp

// ProtocolVersion defines the canonical VTP protocol version.
const ProtocolVersion = "VTP/1.0"

// BountySpec defines the reward attached to a task.
type BountySpec struct {
	Currency string `json:"currency" yaml:"currency"`
	Amount   int    `json:"amount" yaml:"amount"`
}

// OracleSpec defines the automated verification requirements.
type OracleSpec struct {
	Type       string   `json:"type" yaml:"type"`             // e.g. "execution@1", "rule_kb@1"
	Target     string   `json:"target" yaml:"target"`         // e.g. target class, package, or script
	Assertions []string `json:"assertions" yaml:"assertions"` // assertions to satisfy
}

// ContextSpec captures the baseline repository context.
type ContextSpec struct {
	Repository string `json:"repository" yaml:"repository"`
	BaseCommit string `json:"base_commit" yaml:"base_commit"`
	IssueRef   string `json:"issue_ref,omitempty" yaml:"issue_ref,omitempty"`
}

// TaskSpec represents Phase 1: TASK-SPEC published by creator/maintainer.
type TaskSpec struct {
	Protocol string      `json:"protocol" yaml:"protocol"`
	TaskID   string      `json:"task_id" yaml:"task_id"`
	Title    string      `json:"title" yaml:"title"`
	Bounty   BountySpec  `json:"bounty" yaml:"bounty"`
	Oracle   OracleSpec  `json:"oracle" yaml:"oracle"`
	Context  ContextSpec `json:"context" yaml:"context"`
}

// TaskClaim represents Phase 2: TASK-CLAIM by an autonomous worker.
type TaskClaim struct {
	Protocol       string `json:"protocol" yaml:"protocol"`
	Type           string `json:"type" yaml:"type"` // "CLAIM"
	TaskID         string `json:"task_id" yaml:"task_id"`
	Worker         string `json:"worker" yaml:"worker"`
	WorkerID       string `json:"worker_id" yaml:"worker_id"`
	ClaimedSeq     int64  `json:"claimed_seq" yaml:"claimed_seq"`
	TTLSeq         int64  `json:"ttl_seq" yaml:"ttl_seq"`
	IdempotencyKey string `json:"idempotency_key" yaml:"idempotency_key"`
}

// ExecutionReceipt captures the execution details for Phase 3.
type ExecutionReceipt struct {
	StdoutSHA256       string `json:"stdout_sha256" yaml:"stdout_sha256"`
	DiffHunksSHA256    string `json:"diff_hunks_sha256" yaml:"diff_hunks_sha256"`
	ExecutedAssertions int    `json:"executed_assertions" yaml:"executed_assertions"`
	ExitCode           int    `json:"exit_code" yaml:"exit_code"`
}

// TaskReceipt represents Phase 3: TASK-RECEIPT published upon task completion.
type TaskReceipt struct {
	Protocol       string           `json:"protocol" yaml:"protocol"`
	Type           string           `json:"type" yaml:"type"` // "RECEIPT"
	TaskID         string           `json:"task_id" yaml:"task_id"`
	Worker         string           `json:"worker" yaml:"worker"`
	Execution      ExecutionReceipt `json:"execution" yaml:"execution"`
	Artifacts      []string         `json:"artifacts" yaml:"artifacts"`
	IdempotencyKey string           `json:"idempotency_key" yaml:"idempotency_key"`
}

// TaskVerify represents Phase 4: TASK-VERIFY dual-oracle evaluation.
type TaskVerify struct {
	Protocol       string `json:"protocol" yaml:"protocol"`
	Type           string `json:"type" yaml:"type"` // "VERIFY"
	TaskID         string `json:"task_id" yaml:"task_id"`
	Verifier       string `json:"verifier" yaml:"verifier"`
	OracleType     string `json:"oracle_type" yaml:"oracle_type"`
	Verdict        string `json:"verdict" yaml:"verdict"` // "PASS", "FAIL", "CONTESTED"
	Basis          string `json:"basis" yaml:"basis"`     // e.g. "FACT_CONSISTENT", "COUNTER_EXAMPLE"
	EvidenceSHA256 string `json:"evidence_sha256" yaml:"evidence_sha256"`
	IsDisjointSeat bool   `json:"is_disjoint_seat" yaml:"is_disjoint_seat"` // Clause B compliance
}

// TaskSettle represents Phase 5: TASK-SETTLE economic transfer or ledger mint.
type TaskSettle struct {
	Protocol         string `json:"protocol" yaml:"protocol"`
	Type             string `json:"type" yaml:"type"` // "SETTLE"
	TaskID           string `json:"task_id" yaml:"task_id"`
	SettlementMethod string `json:"settlement_method" yaml:"settlement_method"` // "GRN_TRANSFER", "ESCROW_RELEASE"
	Payer            string `json:"payer" yaml:"payer"`
	Payee            string `json:"payee" yaml:"payee"`
	Amount           int    `json:"amount" yaml:"amount"`
	ReceiptRef       string `json:"receipt_ref" yaml:"receipt_ref"`
	SettledSeq       int64  `json:"settled_seq" yaml:"settled_seq"`
}
