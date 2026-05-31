# Behavioural eval (LLM-in-loop A/B) — harness

The [retrieval](../../docs/eval/retrieval.md) and
[continuity](../../docs/eval/continuity.md) evals are deterministic and run
in CI: they show memory **surfaces** the right knowledge across sessions.
This harness measures the next question — does an agent **act** on it?
i.e. does memory cut *repeated mistakes*? That needs a real LLM in the
loop, so you run it with your model; it is intentionally **not** in CI.

> Status: scaffold. Scenarios + protocol + a pluggable runner are here;
> wire in your agent (`$AGENT_CMD`) and fill the results table.

## The experiment ("groundhog day")

For each scenario, two conditions, each repeated over `TRIALS` runs (LLM
output is non-deterministic):

- **with memory** — a fresh repo where the lesson is already recorded in
  `.agent-memory/` and the `agent-memory mcp` server is available, so the
  agent *can* `fetch_context`.
- **without memory** — the same task in a repo with no `.agent-memory/`
  and no memory tools (today's default agent).

Then give the agent the **session-2 task** and score its output:

- **repeated the mistake** — the output exhibits `mistake_signal` (it made
  the exact error the recorded lesson warns about).
- **applied the lesson** — the output exhibits `correct_signal`.

**Headline metric:** mistake-avoidance rate = `1 − repeated/​trials`, with
vs without memory, and the lift. Secondary: redundant-rediscovery (did the
agent re-derive what memory already had — proxy via tool calls / tokens /
wall-clock).

## Run it

```bash
# A command that runs YOUR agent on a task in the current directory and
# writes its answer/patch to stdout. Examples:
#   export AGENT_CMD='claude -p'                 # Claude Code headless
#   export AGENT_CMD='your-agent --task'         # any CLI agent
export AGENT_CMD='...'
export TRIALS=5            # runs per (scenario, condition)
export MODEL='...'         # record which model — goes in the report

bash eval/behavioural/run.sh        # prints a per-scenario tally
```

`run.sh` sets up the two repo conditions per scenario (in `with` it seeds
the lesson with `agent-memory propose --apply` and writes a project
`.mcp.json`), invokes `$AGENT_CMD` `TRIALS` times each, and scores the
output against the scenario's signals.

## Scoring rigor

Signal-grep is a **coarse** proxy — fine for an obvious mistake string,
weak for nuanced behaviour. For a number you'd publish, also have a
**judge** (an LLM or a human, blind to condition) grade each transcript
*applied the lesson? yes/no*. Report both. Keep the model, temperature, and
prompt fixed and disclosed; small N is illustrative, not definitive.

## Scenarios

[`scenarios.jsonl`](scenarios.jsonl) — one JSON object per line:

| field | meaning |
|---|---|
| `id` | scenario id |
| `lesson` | the `agent-memory propose` args recorded in the `with` condition (session 1) |
| `task` | the session-2 prompt handed to the agent |
| `mistake_signal` | substring/regex whose presence = repeated the mistake |
| `correct_signal` | substring/regex whose presence = applied the lesson |

The scenarios mirror the continuity eval's lessons, so the deterministic
"is it available?" floor and the behavioural "did it act on it?" ceiling
line up.

## Results (fill after running)

```
model: <...>   trials: <N>   date: <...>
scenario              | mistake-avoided (with) | (without) | lift
----------------------|------------------------|-----------|------
cookie-samesite       |        ? / N           |   ? / N   |  +?
kafka-offset          |        ? / N           |   ? / N   |  +?
postgres-pool         |        ? / N           |   ? / N   |  +?
timestamps-utc        |        ? / N           |   ? / N   |  +?
----------------------|------------------------|-----------|------
overall               |        ?%              |    ?%     |  +?
```
