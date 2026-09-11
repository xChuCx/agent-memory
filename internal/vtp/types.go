package vtp

import (
	"crypto/ed25519"
)

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
	Hermetic   bool     `json:"hermetic,omitempty" yaml:"hermetic,omitempty"` // Whether test is hermetic (pure, zero network/ambient dependency)
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
	Creator  string      `json:"creator,omitempty" yaml:"creator,omitempty"`
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

// CommitmentSpec captures cryptographic commitments linking Phase 1-2 to Phase 3.
type CommitmentSpec struct {
	SpecSHA256        string `json:"spec_sha256,omitempty" yaml:"spec_sha256,omitempty"`
	DatasetCommitment string `json:"dataset_commitment,omitempty" yaml:"dataset_commitment,omitempty"`
	ClaimSHA256       string `json:"claim_sha256,omitempty" yaml:"claim_sha256,omitempty"`
}

// ArtifactSpec represents an individual delivery artifact with its content hash.
type ArtifactSpec struct {
	Path   string `json:"path" yaml:"path"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// HermeticityStatus defines the tri-state evaluation of execution hermeticity (SAR-006 / second-thought audit #22345).
type HermeticityStatus string

const (
	HermeticStatusVerifiedHermetic    HermeticityStatus = "VERIFIED_HERMETIC"
	HermeticStatusDeclaredNonHermetic HermeticityStatus = "DECLARED_NON_HERMETIC"
	HermeticStatusUnknown             HermeticityStatus = "UNKNOWN"
)

// HermeticityReason provides an orthogonal, unambiguous diagnostic code explaining why an attestation
// succeeded or failed hermetic evaluation (addressing @just-nik audit #22960).
type HermeticityReason string

const (
	HermeticReasonVerified                HermeticityReason = "REASON_VERIFIED"
	HermeticReasonDeclaredNonHermetic     HermeticityReason = "REASON_DECLARED_NON_HERMETIC"
	HermeticReasonMissingSandbox          HermeticityReason = "REASON_MISSING_SANDBOX"
	HermeticReasonMissingSignature        HermeticityReason = "REASON_MISSING_SIGNATURE"
	HermeticReasonMissingPolicyDigest     HermeticityReason = "REASON_MISSING_POLICY_DIGEST"
	HermeticReasonMissingIssuer           HermeticityReason = "REASON_MISSING_ISSUER"
	HermeticReasonNetworkCapabilityDenied HermeticityReason = "REASON_NETWORK_CAPABILITY_DENIED"
	HermeticReasonIssuerUntrusted         HermeticityReason = "REASON_ISSUER_UNTRUSTED"
	HermeticReasonPolicyUnapproved        HermeticityReason = "REASON_POLICY_UNAPPROVED"
	HermeticReasonKeyRevoked              HermeticityReason = "REASON_KEY_REVOKED"
	HermeticReasonEpochStale              HermeticityReason = "REASON_EPOCH_STALE"
	HermeticReasonExpired                 HermeticityReason = "REASON_EXPIRED"
	HermeticReasonFutureIssuedAt          HermeticityReason = "REASON_FUTURE_ISSUED_AT"
	HermeticReasonInvalidTimeWindow       HermeticityReason = "REASON_INVALID_TIME_WINDOW"
	HermeticReasonMissingKeyRegistry      HermeticityReason = "REASON_MISSING_KEY_REGISTRY"
	HermeticReasonKeyUnregistered         HermeticityReason = "REASON_KEY_UNREGISTERED"
	HermeticReasonKeyRunnerMismatch       HermeticityReason = "REASON_KEY_RUNNER_MISMATCH"
	HermeticReasonSignatureInvalid        HermeticityReason = "REASON_SIGNATURE_INVALID"
)

// TrustedKeyBinding binds an ED25519 public key to an authorized issuer and runner identity (astranaut01 audit #22956).
type TrustedKeyBinding struct {
	PublicKey ed25519.PublicKey `json:"-" yaml:"-"`
	Issuer    string            `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	RunnerID  string            `json:"runner_id,omitempty" yaml:"runner_id,omitempty"`
	Verifier  string            `json:"verifier,omitempty" yaml:"verifier,omitempty"`
}

// SandboxAttestation captures cryptographically bound evidence of sandbox isolation (SAR-006 / second-thought audit #22398, #22431).
type SandboxAttestation struct {
	RunnerID     string   `json:"runner_id,omitempty" yaml:"runner_id,omitempty"`         // Enclave / runner instance identifier
	Issuer       string   `json:"issuer,omitempty" yaml:"issuer,omitempty"`               // Trusted runner / enclave authority identifier
	KeyID        string   `json:"key_id,omitempty" yaml:"key_id,omitempty"`               // Public key ID used by runner to sign attestation
	PolicyDigest string   `json:"policy_digest,omitempty" yaml:"policy_digest,omitempty"` // Digest of sandbox isolation profile
	PolicyEpoch  uint64   `json:"policy_epoch,omitempty" yaml:"policy_epoch,omitempty"`   // Policy generation / epoch
	RuntimeImage string   `json:"runtime_image,omitempty" yaml:"runtime_image,omitempty"` // Digest of container/runtime image
	InputDigest  string   `json:"input_digest,omitempty" yaml:"input_digest,omitempty"`   // Digest of declared input artifacts
	AllowedCaps  []string `json:"allowed_caps,omitempty" yaml:"allowed_caps,omitempty"`   // Declared permitted capabilities (must not include network)
	IssuedAt     int64    `json:"issued_at,omitempty" yaml:"issued_at,omitempty"`         // Unix timestamp of attestation issuance
	ExpiresAt    int64    `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`       // Unix timestamp of attestation expiration
	Signature    string   `json:"signature,omitempty" yaml:"signature,omitempty"`         // ED25519 signature (hex) or canonical digest over bound tuple
}

// HermeticAllowlist defines verifier-approved trusted sandbox issuers, isolation policies, and verifier key registry with revocation.
type HermeticAllowlist struct {
	TrustedIssuers        []string                     `json:"trusted_issuers" yaml:"trusted_issuers"`
	ApprovedPolicyDigests []string                     `json:"approved_policy_digests" yaml:"approved_policy_digests"`
	PublicKeys            map[string]ed25519.PublicKey `json:"-" yaml:"-"`
	KeyBindings           map[string]TrustedKeyBinding `json:"-" yaml:"-"`
	RevokedKeyIDs         map[string]bool              `json:"revoked_key_ids,omitempty" yaml:"revoked_key_ids,omitempty"`
	CurrentEpoch          uint64                       `json:"current_epoch,omitempty" yaml:"current_epoch,omitempty"`
	MinAcceptedEpoch      uint64                       `json:"min_accepted_epoch,omitempty" yaml:"min_accepted_epoch,omitempty"`
	CurrentTime           int64                        `json:"current_time,omitempty" yaml:"current_time,omitempty"` // Test injection hook for deterministic evaluation
}

// FreshnessPolicy enforces tenant-level access control and cache freshness on verification reuse (astranaut01 audit #22956).
type FreshnessPolicy struct {
	MaxAgeSeconds int64           `json:"max_age_seconds,omitempty" yaml:"max_age_seconds,omitempty"`
	CurrentTime   int64           `json:"current_time,omitempty" yaml:"current_time,omitempty"`
	TenantACL     map[string]bool `json:"tenant_acl,omitempty" yaml:"tenant_acl,omitempty"`
}

// AssertionResult captures the execution verdict and evidence of an individual oracle assertion.
type AssertionResult struct {
	ID       string `json:"id" yaml:"id"`
	Passed   bool   `json:"passed" yaml:"passed"`
	Evidence string `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

// ExecutionReceipt captures the execution details for Phase 3.
type ExecutionReceipt struct {
	StdoutSHA256       string              `json:"stdout_sha256" yaml:"stdout_sha256"`
	DiffHunksSHA256    string              `json:"diff_hunks_sha256" yaml:"diff_hunks_sha256"`
	ExecutedAssertions int                 `json:"executed_assertions" yaml:"executed_assertions"`
	AssertionResults   []AssertionResult   `json:"assertion_results,omitempty" yaml:"assertion_results,omitempty"`
	ExitCode           int                 `json:"exit_code" yaml:"exit_code"`
	Hermetic           bool                `json:"hermetic,omitempty" yaml:"hermetic,omitempty"` // Self-declared worker flag
	Sandbox            *SandboxAttestation `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`   // Verifiable isolation evidence
}

// TaskReceipt represents Phase 3: TASK-RECEIPT published upon task completion.
type TaskReceipt struct {
	Protocol       string           `json:"protocol" yaml:"protocol"`
	Type           string           `json:"type" yaml:"type"` // "RECEIPT"
	TaskID         string           `json:"task_id" yaml:"task_id"`
	Commitments    *CommitmentSpec  `json:"commitments,omitempty" yaml:"commitments,omitempty"`
	Worker         string           `json:"worker" yaml:"worker"`
	Execution      ExecutionReceipt `json:"execution" yaml:"execution"`
	Artifacts      any              `json:"artifacts" yaml:"artifacts"` // []string or []ArtifactSpec
	IdempotencyKey string           `json:"idempotency_key" yaml:"idempotency_key"`
}

// TaskVerify represents Phase 4: TASK-VERIFY dual-oracle evaluation.
type TaskVerify struct {
	Protocol             string            `json:"protocol" yaml:"protocol"`
	Type                 string            `json:"type" yaml:"type"` // "VERIFY"
	TaskID               string            `json:"task_id" yaml:"task_id"`
	Verifier             string            `json:"verifier" yaml:"verifier"`
	OracleType           string            `json:"oracle_type" yaml:"oracle_type"`
	Verdict              string            `json:"verdict" yaml:"verdict"` // "PASS", "FAIL", "PARTIAL", "CONTESTED"
	Basis                string            `json:"basis" yaml:"basis"`     // e.g. "FACT_CONSISTENT", "COUNTER_EXAMPLE"
	EvidenceSHA256       string            `json:"evidence_sha256" yaml:"evidence_sha256"`
	DistinctAccountIDs   bool              `json:"distinct_account_ids" yaml:"distinct_account_ids"`       // Derived: verifier != worker && verifier != creator
	OperatorIndependence string            `json:"operator_independence" yaml:"operator_independence"` // "INDEPENDENT", "CORROBORATED_SAME_OPERATOR", or "UNKNOWN"
	IsDisjointSeat       bool              `json:"is_disjoint_seat" yaml:"is_disjoint_seat"`               // Backwards compatibility alias
	HermeticityStatus    HermeticityStatus `json:"hermeticity_status" yaml:"hermeticity_status"`           // VERIFIED_HERMETIC, DECLARED_NON_HERMETIC, UNKNOWN
	HermeticityReason    HermeticityReason `json:"hermeticity_reason,omitempty" yaml:"hermeticity_reason,omitempty"` // Orthogonal diagnostic reason code (@just-nik audit #22960)
	IsHermetic           bool              `json:"is_hermetic" yaml:"is_hermetic"`                         // Backwards-compatible alias (true ONLY if VERIFIED_HERMETIC)
	VerifiedAt           int64             `json:"verified_at,omitempty" yaml:"verified_at,omitempty"`     // Unix timestamp when verification was evaluated
	Signature            string            `json:"signature,omitempty" yaml:"signature,omitempty"`         // ED25519 signature (hex) over canonical TaskVerify bytes
	VerifierKeyID        string            `json:"verifier_key_id,omitempty" yaml:"verifier_key_id,omitempty"` // Key identifier in allowlist
}

// TaskSettle represents Phase 5: TASK-SETTLE economic transfer or ledger mint.
type TaskSettle struct {
	Protocol          string            `json:"protocol" yaml:"protocol"`
	Type              string            `json:"type" yaml:"type"` // "SETTLE"
	TaskID            string            `json:"task_id" yaml:"task_id"`
	SettlementMethod  string            `json:"settlement_method" yaml:"settlement_method"` // "GRN_TRANSFER", "ESCROW_RELEASE"
	Payer             string            `json:"payer" yaml:"payer"`
	Payee             string            `json:"payee" yaml:"payee"`
	Amount            int               `json:"amount" yaml:"amount"`
	HermeticityStatus HermeticityStatus `json:"hermeticity_status,omitempty" yaml:"hermeticity_status,omitempty"`
	IsHermetic        bool              `json:"is_hermetic,omitempty" yaml:"is_hermetic,omitempty"`
	ReceiptRef        string            `json:"receipt_ref" yaml:"receipt_ref"`
	SettledSeq        int64             `json:"settled_seq" yaml:"settled_seq"`
}
