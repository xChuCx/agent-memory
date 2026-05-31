# Roadmap

Forward-looking direction. For the historical MVP build log (milestones
M0–M8, shipped through v0.3.0) see
[agent-memory-implementation-plan.md](agent-memory-implementation-plan.md);
shipped changes live in [CHANGELOG.md](CHANGELOG.md).

This roadmap is intent, not a contract — items move as we learn (and as
the eval harness tells us what actually helps). Issues and discussion are
welcome.

## North star

**agent-memory is the knowledge substrate for coding agents** — the
answer to *"what must the agent know to act well here?"*, kept as plain,
reviewable Markdown that lives in your repo.

We grow along the **knowledge** axis:

> per-repo memory → reviewable team memory (git) → system / landscape
> memory (federation) → standards-aware design

…not along the infrastructure axis (a cloud backend, a validation engine,
a scaffolding tool). Every step extends the same engine — Markdown source
of truth, `@id` sections, MCP `fetch`/`propose`, provenance, the staging
review gate — rather than bolting on a separate product.

## Principles (the lens for every roadmap decision)

1. **Local-first.** Memory is files in your repo. **Git is the sync** —
   no server required to share memory across a team or machine.
2. **Reviewable.** Durable changes stage for human approval (`review
   --diff` → `apply`); nothing opaque lands silently.
3. **Safe by default.** Secrets/PII are scanned out; logs and memory never
   carry credential bytes. There is no global "disable safety" switch.
4. **No lock-in.** Markdown is the source of truth; the SQLite index is a
   rebuildable cache. Delete the tool and your memory is still readable.
5. **Eval-driven.** Features earn their place by improving agent outcomes
   (see *Prove it*), not by sounding good.
6. **Boring tech.** Single static binary, CGo-free, few dependencies.

## Where we are — v0.3.0 (shipped)

Three MCP tools (`fetch_context`, `propose_update`, `status`) + a full
CLI. Eight structured operations, byte-preserving Markdown engine, SQLite
FTS5 shadow index with BM25 + ranking signals + near-duplicate dedup,
staging/review/apply/reject/rebase lifecycle, secret + PII + provenance
guards, git auto-stage, deterministic `index.md`, structured logging,
adapters for Claude/Cursor/AGENTS.md/Gemini, cross-platform release
binaries. The project dogfoods its own memory (`.agent-memory/`).

## Near-term → 1.0 — *harden and prove the core*

Goal of 1.0: a **stable memory format + MCP contract** and a **proven**
single-repo / git-team workflow. Mostly polish + evidence, not new
surface.

- ✅ **Team sharing via git, done right** *(shipped, M7).* The section-aware
  `git` merge driver unions concurrent edits to `.agent-memory/` instead of
  conflicting — the one gap git left for shared memory. See
  [docs/patterns/merge-driver.md](docs/patterns/merge-driver.md).
- **Prove it — behavioural eval.** The offline **retrieval-quality eval**
  (recall/MRR/nDCG, CI-guarded) is ✅ shipped
  ([docs/eval/retrieval.md](docs/eval/retrieval.md)). Still to do: the
  **behavioural A/B** — a "groundhog-day" measurement of whether memory
  cuts an agent's repeated mistakes / redundant rediscovery. That's the
  task-success number a launch leads with. *(was M8)*
- **Hygiene & ergonomics.** `clean-local` (GC orphaned branch-local
  files), write-time semantic-duplication check, a setup smoothing pass
  (one-line install, MCP config snippet), `review`/`propose` UX from
  dogfooding.
- **Format & contract stability.** Lock the schema + MCP shapes; add a
  schema-version migration path so existing stores upgrade cleanly.

## The bet — Federated / system-level memory *(post-1.0)*

Today memory is single-repo. The next leap serves **solution
architecture**: when an agent designs a feature spanning many services, it
needs a map of the surrounding system — components, their public
contracts, owners, dependencies, what will change.

- **Multi-store fetch.** A project's `.agent-memory/` can reference one or
  more **shared landscape stores** (e.g. a platform/architecture-memory
  repo). `fetch_context` assembles from local + referenced stores, ranked
  together, provenance preserved.
- **Component / contract / actor schema.** First-class section kinds to
  describe a service, its API surface, owning team, dependencies, and
  integration points — so a designer agent can reason about the
  environment it's changing, not just one repo.
- **Generated, not hand-kept.** Importers that turn existing sources of
  truth into landscape memory: OpenAPI / AsyncAPI specs, service catalogs
  (e.g. Backstage), and service registries. Memory stays in sync with the
  real system instead of rotting.

## Standards-aware design *(post-1.0, alongside the bet)*

Make agents **aware** of an organisation's accepted patterns and
templates — so they design with them instead of reinventing.

- **Standards as memory.** A category for corporate patterns
  (integration, security, observability) and approved starter templates,
  marked *required/active*, **surfaced contextually** when the agent works
  in the relevant area.
- **Lightweight conformance check.** A `doctor`-style check: "does this
  design reference the standards required for its area?" — a nudge, not a
  gate.

We deliberately stop at *awareness + surfacing*. Instantiating templates
and enforcing policy belong to the agent plus existing tools (Backstage
templates, cookiecutter, policy engines) — see non-goals.

## Exploratory — *only if an eval proves the need*

- Vector / hybrid search (only if BM25 + ranking proves insufficient on
  the retrieval eval).
- Richer review surfaces: a TUI selector, a GitHub Action that posts the
  staged diff as a PR comment.
- IDE surfacing of the context pack.
- Per-task overlay state; memory-quality scoring.

## Non-goals (what we deliberately won't build)

Saying no keeps the tool sharp:

- **A cloud / SaaS memory backend.** Git is the sync; local-first is the
  point. (A far-future *optional* hosted index for very large monorepos
  would be eval-gated and never a hosted SaaS of your memory.)
- **A scaffolding / templating engine.** We make agents aware of approved
  templates; instantiation stays with existing tools.
- **A compliance / policy-enforcement engine.** We surface standards and a
  light conformance nudge; deep enforcement is a separate concern.
- **A general wiki / doc store.** Memory is agent working knowledge —
  current state, decisions, pitfalls, conventions, the system map — not a
  replacement for your docs site.
- **Mandatory vector search or a non-Go rewrite** unless profiling/eval
  justifies it.

## How to influence this

Open an issue describing the *use case* (not just the feature). Items that
align with the principles and that an eval can show help real agent
outcomes move up fastest.
