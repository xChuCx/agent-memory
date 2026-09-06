# Pattern: Deterministic Merkle State Digest

**Status:** Implemented in [`internal/memory/digest.go`](../../internal/memory/digest.go) and [`internal/cli/digest.go`](../../internal/cli/digest.go).
**Owner:** `internal/memory/`, `internal/cli/`.
**Relevance:** VTP-1 Task Verification, Cross-Platform Multi-Agent Consensus, Byte Hygiene.

## Problem

When AI coding agents collaborate across diverse environments (e.g., Windows 11 vs. Linux vs. macOS) or execute verifiable tasks under the **Verifiable Task Protocol (VTP-1)**, proving that durable memory has remained intact without silent corruption or unauthorized alteration is critical:

1. **Git Line-Ending Mutilation:** Windows and Linux clients frequently check out repositories with differing line endings (`\r\n` vs. `\n`). Raw byte hashing across platforms produces spurious mismatches even when semantic content is identical.
2. **Order Ambiguity:** Walking filesystem directories produces arbitrary, non-deterministic file orderings across runtimes.
3. **Auditable Settlement Receipts:** VTP-1 task claims require autonomous agents to attest to their exact memory state before and after task execution.

## Solution

`agent-memory digest` computes a canonical, cross-platform deterministic SHA-256 Merkle root across all active durable memory files:

1. **Scope:** Scans root-level category files (`conventions.md`, `decisions.md`, `index.md`, `pitfalls.md`) and all domain modules (`modules/*.md`). Ephemeral state (`staging/`, `local/`, `sessions/`, `archive/`, `meta/`) is excluded.
2. **CRLF Invariance:** Normalizes `\r\n` to `\n` prior to leaf hashing, ensuring 100% byte-for-byte hash identity between Windows and Unix runtimes.
3. **Canonical Sorting:** Sorts all discovered files lexicographically by normalized forward-slash relative path (`conventions.md` < `decisions.md` < `index.md` < `modules/auth.md` < `pitfalls.md`).
4. **Pairwise Merkle Tree:**
   - Leaves: $L_i = \text{SHA-256}(\text{normalized\_bytes}(F_i))$
   - Parent nodes: $P_{j} = \text{SHA-256}(\text{bytes.fromhex}(L_{2j}) \mathbin{\Vert} \text{bytes.fromhex}(L_{2j+1}))$
   - Odd nodes at any level are promoted to the next level.
   - Root: The single resulting root hash string.

## CLI Usage

```bash
# Compute state digest
agent-memory digest

# Output structured JSON receipt (agent_memory_digest/1)
agent-memory digest --json

# Assert memory state matches expected root (exits 0 on match, non-zero on mismatch)
agent-memory digest --verify 2fb9170fc4cf8b019fee325713d2397b895b06648ea29c2c2e481fa957ad5955
```

## VTP-1 Protocol Integration

Autonomous agents executing tasks published under VTP-1 include the memory digest in their task settlement payload:

```json
{
  "protocol": "vtp/1.0",
  "action": "settle_task",
  "task_id": "task-4912",
  "memory_digest": "e0711b89ac7312e09e8ea0409423bd4ef8b302ca8e678748d0cad0232c34865e",
  "receipt": "verified"
}
```

This guarantees dual-oracle verifiability: any auditor can recompute the Merkle root directly from the committed `.agent-memory/` state.
