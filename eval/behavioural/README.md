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

`$AGENT_CMD` is any command that runs your agent on the task passed as a
**trailing argument** and writes its answer to stdout. Per scenario the
runner prepares both repo conditions and, before each call, exports
`$AM_MCP` (this condition's MCP-config flags) and prepends a shared `$HINT`
(see below).

```bash
export AGENT_CMD='your-agent --task'   # task arrives as the trailing arg
export TRIALS=5                        # runs per (scenario, condition)
export MODEL='your-model'              # recorded in the report
bash eval/behavioural/run.sh           # prints a per-scenario tally
```

In `with`, the runner seeds the lesson with `agent-memory propose --apply`
and writes a project `.mcp.json`; in `without` it writes an empty MCP
config. It then invokes `$AGENT_CMD` `TRIALS`× per arm and scores stdout.

### Run it on Claude (headless)

Claude Code's print mode (`claude -p`) is a ready agent: it reads the
project `.mcp.json`, connects to `agent-memory mcp`, and can call
`memory.fetch_context`. The runner's temp repos are throwaway, so
`--dangerously-skip-permissions` is appropriate (it still refuses
`rm -rf /`). `$AM_MCP` expands to this condition's MCP flags.

```bash
export AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP --model claude-sonnet-4-5 --max-turns 8'
export TRIALS=5 MODEL=claude-sonnet-4-5
bash eval/behavioural/run.sh
```

Notes (verified against Claude Code v2.1.x — adjust if your version differs):

- **MCP isolation.** `$AM_MCP` is `--mcp-config .mcp.json --strict-mcp-config`
  in *with* and `--mcp-config <empty> --strict-mcp-config` in *without*;
  `--strict-mcp-config` ignores user/global MCP servers, so a
  globally-installed agent-memory can't leak into the baseline.
- **`--allowedTools` names** (if you don't skip permissions) spell dots as
  underscores: `mcp__agent-memory__memory_fetch_context`.
- **The `$HINT`.** Project skills don't reliably auto-load in headless mode,
  so the runner prepends one neutral instruction — *"consult any available
  project memory/context tools first"* — to **both** arms. That's the role
  the installed skill plays in real use; it is identical across arms and
  does not reveal the answer. Set `HINT=''` to rely purely on skill auto-load.
- **Secondary metrics.** Add `--output-format json` to capture
  `total_cost_usd`/`usage`, or `--output-format stream-json --verbose` to
  count tool calls (a proxy for "redundant rediscovery").

Fast smoke (one scenario, one trial per arm) via the `SCENARIOS` override:

```bash
head -1 eval/behavioural/scenarios.jsonl > /tmp/one.jsonl
SCENARIOS=/tmp/one.jsonl TRIALS=1 \
  AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP' \
  bash eval/behavioural/run.sh
```

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
