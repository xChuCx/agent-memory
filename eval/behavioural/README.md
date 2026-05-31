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

Then give the agent the **session-2 task** and score its output by literal
(case-insensitive) substring match:

- `correct_signal` present — it **applied** the project rule.
- `mistake_signal` present — it made the **default mistake** the lesson warns of.

**Headline metric: followed-rule rate** = fraction of trials where the
answer applied the rule *and* avoided the mistake (`correct ∧ ¬mistake`),
with vs without memory, and the **lift** between them. Secondary:
redundant-rediscovery (did the agent re-derive what memory already had —
proxy via tool calls / tokens / wall-clock; see `--output-format stream-json`).

## What makes a scenario show lift

Memory can only change behaviour the model wouldn't already get right. A
scenario shows lift **only if its rule is something the model can't guess** —
project-specific, counter to the common default, or otherwise non-obvious:

- ✅ *"In this repo inject `billing.Clock`; never call `time.Now()` directly."*
  The model's default is `time.Now()`, so memoryless it makes the mistake;
  with memory it follows the rule. Clear lift.
- ❌ *"Use `TIMESTAMPTZ`, not naive `TIMESTAMP`."* A strong model already
  knows this best practice → **both** arms score ~100% and lift ≈ 0. That's
  not memory failing, just a scenario with no knowledge gap to fill.

So `lift = 0` with **both arms high** means the rule was too obvious — swap
in a more idiosyncratic one. `lift = 0` with **both arms low** means
something upstream broke (agent didn't run, or memory wasn't fetched) — open
the transcripts with `DEBUG=1`. The shipped
[`scenarios.jsonl`](scenarios.jsonl) deliberately uses counter-default rules
(injected clock, in-house feature-flag API, ULID IDs, integer-cents money).

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
Add `DEBUG=1` to save every transcript to a temp dir for inspection.

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

### On Windows

`run.sh` is a bash script — run it from **Git Bash** (ships with Git for
Windows), not PowerShell/cmd. The commands above are identical. Prereqs:

- `agent-memory` and `claude` on `PATH` as the normal **Windows** builds
  (no special Linux build needed).
- `jq` — `winget install jqlang.jq` (or `scoop install jq`).
- The script already converts its temp-dir path to a Windows path
  (`cygpath -m`, bundled with Git Bash) before writing it into `.mcp.json`,
  so the MCP server Claude spawns gets a path it understands.

```bash
# in a Git Bash shell, from the repo root:
export AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP --model claude-sonnet-4-5 --max-turns 8'
export TRIALS=5 MODEL=claude-sonnet-4-5
bash eval/behavioural/run.sh
```

Alternatively, **WSL** runs it as plain Linux — but then install the Linux
builds of `agent-memory` and `claude` *inside* WSL (the Windows `.exe`s
aren't on the WSL `PATH`). A native PowerShell port isn't shipped; open an
issue if you'd use one.

## Scoring rigor

Substring matching is a **coarse** proxy — fine for a crisp signal like
`time.Now()`, weak for nuanced behaviour. Inspect what actually happened
with `DEBUG=1 bash eval/behavioural/run.sh` (saves every transcript to a
temp dir — confirm the agent ran and, in *with*, actually fetched memory).
For a number you'd publish, also have a **judge** (an LLM or a human, blind
to condition) grade each transcript *applied the lesson? yes/no*. Report
both. Keep the model, temperature, and prompt fixed and disclosed; small N
is illustrative, not definitive.

## Scenarios

[`scenarios.jsonl`](scenarios.jsonl) — one JSON object per line:

| field | meaning |
|---|---|
| `id` | scenario id (used in `DEBUG` transcript filenames) |
| `lesson` | the rule recorded into memory in the `with` arm (session 1) |
| `task` | the session-2 prompt handed to the agent |
| `mistake_signal` | literal case-insensitive substring; present ⇒ made the default mistake |
| `correct_signal` | literal case-insensitive substring; present ⇒ applied the rule |

Signals match as **literal substrings** (bash `[[ == ]]`, no regex/grep), so
values like `time.Now()` and `uuid.NewV4` need no escaping. Keep
`correct_signal` from being a substring of `mistake_signal` (e.g. avoid
`TIMESTAMP` vs `TIMESTAMPTZ`), or a mistaken answer would score as correct.

## Results (fill after running)

```
model: <...>   trials: <N>   date: <...>
scenario         | followed-rule (with) | (without) | lift
-----------------|----------------------|-----------|------
clock-injection  |        ? / N         |   ? / N   |  +?
feature-flag     |        ? / N         |   ? / N   |  +?
id-format        |        ? / N         |   ? / N   |  +?
money-type       |        ? / N         |   ? / N   |  +?
-----------------|----------------------|-----------|------
overall          |        ?%            |    ?%     |  +?
```
