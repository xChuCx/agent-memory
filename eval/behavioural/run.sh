#!/usr/bin/env bash
#
# Behavioural A/B runner (scaffold). Per scenario it measures whether an
# agent uses a PROJECT-SPECIFIC API/convention it can only know from memory —
# with agent-memory available vs without. See README.md for the protocol and
# the (important) caveats on signal matching and scenario design.
#
# A scenario only shows lift if its "correct" token is something the model
# can't already produce (an in-house API like httpx.NewClient). A generic
# best-practice the model knows cold will read high in BOTH arms — expected,
# not a bug.
#
# Requires: agent-memory on PATH, jq, git, and $AGENT_CMD — a command that
# runs YOUR agent on the task passed as a trailing arg and writes its answer
# (text and/or patch) to stdout. The runner exports vars AGENT_CMD MAY use:
#   $AM_MCP — MCP-config flags for THIS condition (with: our server only;
#             without: an empty server set). Both use --strict-mcp-config so
#             a globally-installed MCP can't contaminate the baseline.
#
# Claude Code (headless) — see README.md "Run it on Claude":
#   export AGENT_CMD='claude -p --dangerously-skip-permissions $AM_MCP --model sonnet'
# Any other CLI agent that takes the task as a trailing arg:
#   export AGENT_CMD='my-agent --task'
#
# Usage: export AGENT_CMD='...'; TRIALS=5 MODEL=... [DEBUG=1] bash eval/behavioural/run.sh
set -euo pipefail
shopt -s nocasematch   # case-insensitive [[ == ]] substring scoring (no grep)

: "${AGENT_CMD:?set AGENT_CMD to a command that runs your agent on a task arg}"
TRIALS="${TRIALS:-5}"
MODEL="${MODEL:-unspecified}"
# Same nudge in BOTH conditions — this is the role the installed skill plays
# in real use (tell the agent to consult memory first); only the *environment*
# differs between arms, not the prompt, and it does not reveal the answer.
# Override, or set HINT='' to rely purely on skill auto-load.
HINT="${HINT:-Before answering, consult any available project memory/context tools for known pitfalls and prior decisions, then do the task: }"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCENARIOS="${SCENARIOS:-$HERE/scenarios.jsonl}"   # override for a 1-line smoke
DEBUG="${DEBUG:-}"                                # set to 1: save transcripts + per-trial diagnostics
DEBUG_DIR="${DEBUG_DIR:-$PWD/behavioural-transcripts}"   # findable, in the repo root

for dep in agent-memory jq git; do
  command -v "$dep" >/dev/null || { echo "missing dependency: $dep" >&2; exit 1; }
done

if [ -n "$DEBUG" ]; then
  mkdir -p "$DEBUG_DIR"
  dbg_win="$DEBUG_DIR"; command -v cygpath >/dev/null 2>&1 && dbg_win="$(cygpath -w "$DEBUG_DIR")"
  echo "DEBUG: transcripts -> $DEBUG_DIR (one file per scenario.cond.trial)"
  [ "$dbg_win" != "$DEBUG_DIR" ] && echo "DEBUG: open in Windows at -> $dbg_win"
  echo "DEBUG: per-trial diagnostics (len/mistake/correct/verdict) print below as trials run."
fi

echo "model: $MODEL   trials: $TRIALS"
printf '%-16s | %-18s | %-13s | %s\n' "scenario" "applied-rule(with)" "(without)" "lift"
printf -- '-----------------|--------------------|---------------|------\n'

# run the agent on $task in a freshly-prepared repo; echo "applied" iff the
# answer uses the project-specific API (correct_signal present).
# Args: cond lesson task mistake correct id trial
run_trial() {
  local cond="$1" lesson="$2" task="$3" mistake="$4" correct="$5" id="$6" trial="$7"
  local work mcp root; work="$(mktemp -d)"
  # Git Bash: $work is an MSYS path (/tmp/...); the native agent-memory that
  # Claude spawns needs a Windows path. cygpath -m -> C:/... (no-op elsewhere).
  root="$work"; command -v cygpath >/dev/null 2>&1 && root="$(cygpath -m "$work")"
  ( cd "$work" && git init -q . )
  if [ "$cond" = "with" ]; then
    ( cd "$work"
      agent-memory init --name bench >/dev/null 2>&1
      agent-memory propose --intent add_pitfall --op append_to_section \
        --path pitfalls.md --section-id pitfalls --content "$lesson" --apply >/dev/null 2>&1
      cat > .mcp.json <<JSON
{ "mcpServers": { "agent-memory": { "type": "stdio", "command": "agent-memory", "args": ["mcp", "--root", "$root"] } } }
JSON
    )
    mcp="--mcp-config .mcp.json --strict-mcp-config"        # with: our server only
  else
    ( cd "$work" && printf '{"mcpServers":{}}' > .mcp-empty.json )
    mcp="--mcp-config .mcp-empty.json --strict-mcp-config"  # without: no servers
  fi
  local out
  out="$(cd "$work" && TASK="$HINT$task" AM_MCP="$mcp" eval "$AGENT_CMD \"\$TASK\"" 2>&1 || true)"
  [ -n "$DEBUG" ] && printf '%s\n' "$out" > "$DEBUG_DIR/$id.$cond.$trial.txt"
  rm -rf "$work" 2>/dev/null || true   # MCP child may briefly hold the dir; ignore
  # bash substring match (nocasematch) — no external grep (some MSYS greps
  # crash on piped -F) and signals like uuid.NewV4/http.Client stay literal.
  local m=0 c=0
  [[ "$out" == *"$correct"* ]] && c=1
  [[ "$out" == *"$mistake"* ]] && m=1
  # "applied" = uses the project-specific API (correct signal). We deliberately
  # do NOT also require the mistake absent: for a "use Y, not X" rule the model
  # routinely names X in its prose ("used Y instead of X"), which would
  # false-trip an X detector. The correct signal is an in-house token the model
  # can't produce without memory, so its presence is the reliable signal; the
  # mistake count is shown in DEBUG for context only.
  local verdict=missed
  [ "$c" = 1 ] && verdict=applied
  [ -n "$DEBUG" ] && printf '  [%-14s %-7s #%s] len=%-5s mistake=%s correct=%s -> %s\n' \
    "$id" "$cond" "$trial" "${#out}" "$m" "$c" "$verdict" >&2
  echo "$verdict"
}

while IFS= read -r line; do
  [ -z "$line" ] && continue
  id="$(jq -r '.id' <<<"$line")"
  lesson="$(jq -r '.lesson' <<<"$line")"
  task="$(jq -r '.task' <<<"$line")"
  mistake="$(jq -r '.mistake_signal' <<<"$line")"
  correct="$(jq -r '.correct_signal' <<<"$line")"

  declare -A f=( [with]=0 [without]=0 )
  for cond in with without; do
    for t in $(seq 1 "$TRIALS"); do
      [ "$(run_trial "$cond" "$lesson" "$task" "$mistake" "$correct" "$id" "$t")" = applied ] \
        && f[$cond]=$(( f[$cond] + 1 ))
    done
  done
  lift=$(( f[with] - f[without] ))
  printf '%-16s | %-18s | %-13s | %+d\n' \
    "$id" "${f[with]} / $TRIALS" "${f[without]} / $TRIALS" "$lift"
done < "$SCENARIOS"

echo
echo "applied-rule = the answer uses the project-specific API (correct signal) —"
echo "a token the model can't produce without memory, so lift>0 proves memory"
echo "changed behaviour. lift=0 both-low => check DEBUG=1 transcripts (did the agent"
echo "run and fetch memory?); both-high => the correct signal is guessable, make it"
echo "more idiosyncratic. Substring matching is coarse — add a blind judge to publish."
