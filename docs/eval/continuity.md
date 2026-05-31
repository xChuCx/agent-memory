# Cross-session continuity eval

Does a lesson an agent records in one session survive into the next? This
is the amnesia problem agent-memory exists to fix, measured deterministically
through the **real** write→persist→retrieve loop.

- **Harness:** [`internal/eval/continuity_test.go`](../../internal/eval/continuity_test.go)
- **Reproduce:** `go test -run TestMemoryContinuity -v ./internal/eval/`
- **Runs in CI** (no LLM, deterministic).

## Result

5 cross-session scenarios (pitfalls + decisions). In each, "session 1"
records a lesson via `memory.ProposeUpdate` (and `ApplyStaged` where the
category stages) — exactly the agent write path — and a fresh "session 2"
calls `memory.BuildContextPack` for a related task:

| | prior-session knowledge available to session 2 |
|---|---|
| **with agent-memory** | **5 / 5** |
| without (no memory layer) | 0 / 5 |

## What this means (and doesn't)

- **This is the necessary precondition for behaviour change.** With memory,
  the lesson recorded earlier is in the context pack the next session's
  agent receives, so it *can* avoid repeating the mistake. Without a memory
  layer the lesson was never persisted, so the agent starts blank — the
  amnesia baseline.
- **It is not a task-success claim.** Whether the LLM actually *acts* on the
  surfaced lesson (avoids the mistake, applies the decision) requires an LLM
  in the loop — see the behavioural A/B harness in
  [`eval/behavioural/`](../../eval/behavioural/). Continuity is the floor;
  task-success is the ceiling that harness measures.
- **Full loop, not just search.** Unlike the [retrieval eval](retrieval.md)
  (which scores ranking on a fixed corpus), this exercises
  propose → validate → stage/apply → re-index → fetch across two sessions,
  so it also guards the write path and persistence.
