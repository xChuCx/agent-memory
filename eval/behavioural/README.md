# Behavioural eval (LLM-in-loop A/B) — harness

The [retrieval](../../docs/eval/retrieval.md) and
[continuity](../../docs/eval/continuity.md) evals are deterministic and run
in CI: they show memory **surfaces** the right knowledge across sessions.
This harness measures the next question — does an agent **act** on it?
Concretely: does it use a project-specific convention it could *only* learn
from memory? That needs a real LLM in the loop, so you run it with your
model; it is intentionally **not** in CI.

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

- `correct_signal` present — the answer used the **project-specific API** the
  rule prescribes (e.g. `httpx.NewClient`): an in-house token the model can't
  produce unless it learned it from memory.

**Headline metric: applied-rule rate** = fraction of trials whose answer
contains `correct_signal`, with vs without memory, and the **lift** between
them. Because the token is unguessable, `without` ≈ 0 and any `with` hits
*prove* memory was fetched and used.

We score on the **positive** signal only — not on "avoided the mistake."
When the rule is "use Y, not X", the model's prose routinely names X ("I used
Y instead of X"), so an X-detector false-fires even on a *correct* answer (we
saw exactly this with `time.Now()`). `mistake_signal` is therefore
informational — shown in the `DEBUG` line, not scored. Secondary:
redundant-rediscovery (tool calls / tokens; `--output-format stream-json`).

## What makes a scenario show lift

Memory can only change behaviour the model wouldn't already produce. A
scenario shows lift **only if its `correct_signal` is a token the model can't
guess** — an in-house API or convention, not a general best practice:

- ✅ *"All outbound HTTP goes through `httpx.NewClient()`."* The model's
  default is `http.Client{}`; it can't know your `httpx` wrapper exists, so
  `without` never emits `httpx.NewClient` and `with` (via memory) does.
  Clear, self-validating lift.
- ❌ *"Inject a `Clock` instead of calling `time.Now()`."* A strong model
  already injects a clock for testability → **both** arms emit `Clock` and
  lift ≈ 0. Not memory failing — the convention was guessable.

So `lift = 0` with **both arms high** means `correct_signal` was guessable —
make it a more idiosyncratic in-house name. `lift = 0` with **both arms low**
means something upstream broke — run `DEBUG=1` (it prints `len=` per trial):
if `len` ≈ 0 the agent emitted nothing, so test `$AGENT_CMD` in isolation,
e.g. `claude -p --model sonnet "reply with one word: ok"; echo $?` — a
retired model name or a rejected flag makes `claude -p` exit non-zero with
empty output. The shipped [`scenarios.jsonl`](scenarios.jsonl) uses in-house
APIs (`flags.Enabled`, `ids.New`, `httpx.NewClient`, `errs.Wrap`).

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
export AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP --model sonnet'
export TRIALS=5 MODEL=sonnet
bash eval/behavioural/run.sh
```

> **Isolation caveat (important).** If you've registered agent-memory as a
> *user-scoped* MCP server (`claude mcp add agent-memory ...`), `claude -p`
> connects to it in **both** arms **regardless of `--strict-mcp-config`** (an
> undocumented gap) — so the no-memory baseline is contaminated *and* the agent
> can write test lessons into your real store. The runner refuses to start if it
> finds one in `~/.claude.json`. Remove it for the run, then re-add:
>
> ```bash
> claude mcp remove agent-memory          # ... run the eval ...
> claude mcp add -s user agent-memory -- /path/to/agent-memory mcp --root /path/to/repo
> ```
>
> `ALLOW_GLOBAL_MEMORY=1` bypasses the guard, but the numbers will be invalid.
> A canary scenario (a nonsense `correct_signal` like `EmitZorp`) is the way to
> prove isolation: if the **without** arm ever emits it, memory is leaking.

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
- **Model name / turns.** Prefer the version-stable `--model sonnet`/`opus`
  alias — a pinned name like `claude-sonnet-4-5` can be retired, after which
  `claude -p` errors and emits nothing (both arms then read 0). `--max-turns N`
  *exits with an error* if the limit is reached, so add it only once it works.
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
export AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP --model sonnet'
export TRIALS=5 MODEL=sonnet
bash eval/behavioural/run.sh
```

Alternatively, **WSL** runs it as plain Linux — but then install the Linux
builds of `agent-memory` and `claude` *inside* WSL (the Windows `.exe`s
aren't on the WSL `PATH`). A native PowerShell port isn't shipped; open an
issue if you'd use one.

## Scoring rigor

Substring matching is a **coarse** proxy — fine for a crisp in-house token
like `httpx.NewClient`, weak for nuanced behaviour. Inspect what actually happened
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
| `mistake_signal` | the default token the rule forbids; **informational** (shown in `DEBUG`, not scored) |
| `correct_signal` | the in-house token the rule prescribes; present ⇒ **applied** (this is the scored signal) |

Signals match as **literal substrings** (bash `[[ == ]]`, no regex/grep), so
values like `httpx.NewClient` and `uuid.NewV4` need no escaping. Make
`correct_signal` a distinctive in-house name the model can't emit without
memory — that's what produces real lift and makes a `with` hit self-validating.

## Results (fill after running)

```
model: <...>   trials: <N>   date: <...>
scenario         | applied-rule (with) | (without) | lift
-----------------|---------------------|-----------|------
feature-flag     |        ? / N        |   ? / N   |  +?
id-format        |        ? / N        |   ? / N   |  +?
http-client      |        ? / N        |   ? / N   |  +?
error-wrap       |        ? / N        |   ? / N   |  +?
-----------------|---------------------|-----------|------
overall          |        ?%           |    ?%     |  +?
```
