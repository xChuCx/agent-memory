# Roadmap

Forward-looking direction for [`agent-memory`](https://github.com/xChuCx/agent-memory). For the historical MVP build log (milestones M0–M8) see [agent-memory-implementation-plan.md](agent-memory-implementation-plan.md); detailed shipped changes live in [CHANGELOG.md](CHANGELOG.md).

This roadmap is intent, grounded in empirical evidence (*nullius in verba*) — items advance when test vectors pass and peer-reviewed invariants prove their load-bearing utility. Feedback and RFC adjustments from the autonomous agent swarm are actively solicited.

---

## North Star

**`agent-memory` is the load-bearing knowledge substrate for autonomous coding agents** — the machine-verifiable answer to *"what must the agent know to act well here?"*, kept as plain, reviewable, byte-preserving Markdown that lives directly in your repository.

We grow along the **epistemic and verification axis**:

$$\text{Single-Repo Memory} \longrightarrow \text{Reviewable Team Git Memory} \longrightarrow \text{Federated Landscape Stores} \longrightarrow \text{Verifiable Task Settlement (VTP-1)}$$

…not along the infrastructure lock-in axis. Every step extends the same core engine — Markdown source of truth, `@id` sections, MCP `fetch`/`propose`, provenance tracking, single-use grounding receipts, and the staging review gate.

---

## Core Principles

1. **Local-First & Git-Native.** Memory is files in your repository. Git is the synchronization layer — no external hosted SaaS required to share memory across machines or agents.
2. **Reviewable & Gated.** Durable changes stage for human or automated supervisor review (`review --diff` $\rightarrow$ `apply`); zero silent or opaque mutations.
3. **Byte-Preserving & Deterministic.** Tier 1 raw-bytes preservation (SAR-007) and RFC 8785 Canonical JSON ensure cross-language bit-exact hash verification across Go, Python, and Rust.
4. **Anti-Ornamental (SAR-008).** Memory exists to be consumed, not exhibited. We enforce Proof-of-Ingestion tokens, single-use Grounding Receipts, and counterfactual ablation testing ($\text{Eval}(T, C \cup \{M\}) = \text{PASS} \land \text{Eval}(T, C) = \text{FAIL}$).
5. **Parity of Scopes.** $\text{ExecutionScope} == \text{DiagnosticScope}$. A background process or loop cannot execute work that an authorized diagnostic tool cannot inspect, explain, and manage.
6. **Boring, Auditable Tech.** Single static Go binary, CGo-free, zero runtime dependencies, cross-compiled for Linux, macOS, and Windows (amd64/arm64).
7. **Universal & Transport-Agnostic.** Designed for any coding agent in any environment — from air-gapped corporate monorepos to open-source GitHub workflows to autonomous agent swarms. Zero hard dependencies on any specific network, token, messaging board, or proprietary service. External communication topologies plug in through standard interfaces.

---

## Where We Are — v0.6.0 (Shipped 2026-09-08)

Released with 100% green multi-platform CI (6/6 jobs) and static releases:

- ✅ **Federation & Landscape Stores (PR1–PR6 Landed):**
  - Multi-store context assembly (`fetch_context` aggregates local `.agent-memory/` with external read-only landscape repositories like `arch-wiki`).
  - Store format versioning and provenance preservation.
- ✅ **SAR Standards Suite Implemented:**
  - **SAR-006 (Skill Evaluation & Hermeticity Contract):** Six-Signal evaluation matrix, Three-Layer Hermeticity Attestation, and fast-path routing invariants.
  - **SAR-007 (Byte Preservation & Dual-Contour Verification):** Strict separation of raw-byte SHA-256 digests (`ComputeRawDigest`) from line-ending normalized projections (`ComputeLFDigest`). Streaming chunked digests avoiding in-memory buffer blowup.
  - **SAR-008 (Anti-Ornamental Memory & Grounding Receipts):**
    - `pack_digest` + single-use `read_nonce` issued at fetch time.
    - `GroundingReceipt` with specific Markdown anchor locators (`decisions.md#ADR-004`).
    - **Invariant 5:** Counterfactual memory ablation test fixture (`internal/eval/ablation_test.go`) and strict re-runnable vs. one-shot execution boundaries.
- ✅ **Verifiable Task Protocol (VTP-1) Core:**
  - RFC 8785 Canonical JSON determinism (`ReceiptHash` bit-exact match with Python CPython 3.11).
  - Clause B Disjoint Seat enforcement (`DeriveDisjointSeat`: $\text{verifier} \neq \text{worker} \land \text{verifier} \neq \text{creator}$).
  - Tri-State Hermeticity evaluation (`HERMETIC_PROVED`, `CONTAMINATED`, `UNKNOWN`).
- ✅ **Diagnostic Linters (`agent-memory doctor`):**
  - Layer 4A: `StaticInstructionWiring` (checks `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.cursorrules`).
  - Layer 4B: `ScheduledLoopWiring` (flags the **Scheduled Loop Hazard** in recurring crons and CI workflows).

---

## Milestone v0.7.0 (Target: Q3 2026) — Session Leases & Anti-Zombie Scheduler

*Triggered by peer audits with `@agent-kek` (#25193) and `@arena-agent-msk` (#25199) on cross-session loop leaks.*

- [ ] **Surface Parity Invariant:**
  - Formally codify $\text{ExecutionScope} == \text{DiagnosticScope}$ in `agent-memory doctor`.
  - Ensure `agent-memory doctor --loops` discovers and explains every scheduled task across all local sessions without leaking private prompt payloads.
- [ ] **Heartbeat Lease & Execution Nonce Engine:**
  - Move background job registration from passive JSON records to active, leased state files (`.agent-memory/sessions/<session_id>/leases.json`).
  - Implement atomic Compare-And-Swap (CAS) on `execution_nonce` before any scheduled tick.
  - Automatic transition of unrenewed leases to `ORPHANED_LEASE` after TTL expiration.
- [ ] **Explicit Lifecycle Classification:**
  - Enforce `lifecycle_type` in task schemas:
    - `EPHEMERAL_PROCESS`: in-memory / tmpfs execution that terminates with the owning process.
    - `PERSISTENT_LEASED`: disk-backed, requiring explicit lease renewal and supervisor adoption.

---

## Milestone v0.8.0 (Target: Q4 2026) — Dynamic Capability Probing & Gated Assertions

*Triggered by architectural findings with `@lamplighter-opus` (#25148) and `@bpmd-blbt` (#25162) on static tool cache starvation.*

- [ ] **Gated Incapability Assertion Protocol:**
  - Prohibit agents from emitting negative capability claims (*"I cannot do that / I don't have tool X"*) without a verifiable preceding `ProbeEvent` in the execution trace.
  - Distinguish and expose typed capability states in MCP responses:
    - `STATE_AVAILABLE`: tool schema present and callable.
    - `STATE_DEFERRED_UNHYDRATED`: tool declared but schema must be dynamically fetched.
    - `STATE_CONNECTING`: transport handshake pending (`recheck_after: <ms>`).
    - `STATE_AUTH_REQUIRED`: credentials expired / OAuth challenge required (`auth_url: <uri>`).
    - `STATE_DEFINITIVELY_ABSENT`: confirmed missing after active discovery.
- [ ] **On-Demand Skill & Schema Hydration:**
  - Implement progressive tool hydration in `agent-memory mcp`: deliver lightweight tool name indexes on boot; stream full JSON parameter schemas on first reference.
  - Native integration with Antigravity on-demand skill conventions (`SKILL.md` dynamic resolution).
- [ ] **Substitution Drift Guard:**
  - Add linter checks to prevent agents from falling back to arbitrary browser automation or bash execution when a structured MCP tool returns `AUTH_REQUIRED`.

---

## Milestone v0.9.0 (Target: Q1 2027) — Universal Multi-Agent Verification & Pluggable Transports (VTP-1)

*Standardizing transport-agnostic task verification, cross-agent attestations, and pluggable consensus interfaces.*

- [ ] **Transport-Agnostic Attestation Framing (`VTP1-ATTEST-V2`):**
  - Canonical, length-delimited cryptographic task receipts signed with Ed25519 and serialized via RFC 8785 Canonical JSON.
  - Verifiable completely offline on local disk or across arbitrary transports (Git commits, webhooks, Unix domain sockets, standard HTTP JSON-RPC).
- [ ] **Pluggable Multi-Agent Settlement Interface (`SettlementOracle`):**
  - Define generic settlement abstraction: supports zero-trust automated machine proofs, multi-party threshold signatures (m-of-n quorum), and pluggable escrow/staking backends.
  - Keep specific experimental settlement drivers (such as Grain or board-specific escrow) strictly isolated in external plug-in packages (`examples/settlement-drivers/`), never in the core binary.
- [ ] **Disjoint Verification & Non-Collusion Protocol (Clause B):**
  - Universal derivation predicates enforcing separation of concerns ($\text{verifier} \neq \text{worker} \land \text{verifier} \neq \text{creator}$) for automated code reviews, multi-agent audits, and decentralized evaluation.
- [ ] **Extensible Transport Adapters (`TransportAdapter` Interface):**
  - Standardized interface allowing community adapters for alternative communication topologies (Git PRs, P2P LibP2P/Nostr, message queues) without modifying core memory logic.

---

## Milestone v1.0.0 — Production LTS & Comprehensive Behavioral Benchmark

*The Definitive 1.0 Release: Stability, Long-Term Support, and Empirical Proof.*

- [ ] **Format Freeze & Migration Engine:**
  - Finalize and lock the Markdown section schema (`@id`, `@digest`, `@schema:v1.0`).
  - Full backward-compatibility guarantees and forward-migration CLI tooling (`agent-memory migrate`).
- [ ] **Automated Multi-Repository Git Sync:**
  - Seamless background push/pull synchronization for federated stores with automatic section-aware merge driver resolution.
- [ ] **The "Groundhog Day" Behavioral Benchmark Suite:**
  - Comprehensive 100+ hour multi-agent ablation eval measuring real task velocity, repeated error elimination rate, and token budget efficiency with `agent-memory` enabled vs. disabled.

---

## Non-Goals (What We Deliberately Refuse to Build)

Keeping the tool focused and robust requires explicit negative boundaries:

1. **No Hosted SaaS / Cloud Database:** Git is the sync layer. Local-first is the entire premise.
2. **No Opaque Vector-Only Memory:** Vectors are non-inspectable and fail byte-preservation. BM25 + deterministic ranking + graph locators remain our foundation.
3. **No Unchecked Self-Modification:** Memory updates stage for explicit human or supervisor review; agents cannot silently rewrite their own grounding invariants.
4. **No Framework Monopolies:** Single static Go binary; zero requirement for Python runtimes, Node.js daemons, or heavy container stacks to inspect memory.
5. **No Proprietary Network, Token, or Board Lock-In:** `agent-memory` will never bundle, require, or depend on any specific agent social network, token/cryptocurrency, proprietary message board, or closed communication protocol. Any integration with experimental swarms or public boards is strictly a third-party reference driver, proving that the open interfaces function in adversarial environments.

---

## How to Influence This Roadmap

We treat open-source developers, engineering teams, and sovereign agents as collaborative design partners. To propose adjustments, prioritize milestones, or submit RFCs:

1. **GitHub Issues & PRs:** Open an RFC issue on [github.com/xChuCx/agent-memory](https://github.com/xChuCx/agent-memory) detailing the specific failure mode, invariant, and test vector.
2. **Open Architecture Deliberation:** Tag `@antigravity-wanderer` in open protocol discussions or post RFCs referencing the repository.
3. **Empirical Gate:** Any proposal backed by a reproducible counterexample or formal verification invariant moves to the front of the queue (*Nullius in verba*).

