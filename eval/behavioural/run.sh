#!/usr/bin/env bash
#
# Behavioural A/B runner (scaffold). Measures whether an agent repeats a
# recorded mistake WITH agent-memory vs WITHOUT. See README.md for the
# protocol and the (important) caveats on signal-grep scoring.
#
# Requires: agent-memory on PATH, jq, git, and $AGENT_CMD — a command that
# runs YOUR agent on a task given as one argument and writes its answer
# (text and/or patch) to stdout. Examples:
#   export AGENT_CMD='claude -p'        # Claude Code headless
#   export AGENT_CMD='my-agent --task'
#
# Usage:
#   export AGENT_CMD='...'; export TRIALS=5; export MODEL='...'
#   bash eval/behavioural/run.sh
set -euo pipefail

: "${AGENT_CMD:?set AGENT_CMD to a command that runs your agent on a task arg}"
TRIALS="${TRIALS:-5}"
MODEL="${MODEL:-unspecified}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for dep in agent-memory jq git; do
  command -v "$dep" >/dev/null || { echo "missing dependency: $dep" >&2; exit 1; }
done

echo "model: $MODEL   trials: $TRIALS"
printf '%-18s | %-22s | %-13s | %s\n' "scenario" "mistake-avoided(with)" "(without)" "lift"
printf -- '-------------------|------------------------|---------------|------\n'

# run the agent on $task inside a freshly-prepared repo, return "avoided"
# (1 = the recorded mistake's signal is absent from the output) or "repeated".
run_trial() { # $1=cond(with|without) $2=lesson $3=task $4=mistake_signal
  local cond="$1" lesson="$2" task="$3" mistake="$4"
  local work; work="$(mktemp -d)"
  ( cd "$work" && git init -q . )
  if [ "$cond" = "with" ]; then
    ( cd "$work"
      agent-memory init --name bench >/dev/null 2>&1
      agent-memory propose --intent add_pitfall --op append_to_section \
        --path pitfalls.md --section-id pitfalls --content "$lesson" --apply >/dev/null 2>&1
      cat > .mcp.json <<JSON
{ "mcpServers": { "agent-memory": { "command": "agent-memory", "args": ["mcp", "--root", "$work"] } } }
JSON
    )
  fi
  local out
  out="$(cd "$work" && TASK="$task" eval "$AGENT_CMD \"\$TASK\"" 2>&1 || true)"
  rm -rf "$work"
  if printf '%s' "$out" | grep -qiF -- "$mistake"; then echo repeated; else echo avoided; fi
}

while IFS= read -r line; do
  [ -z "$line" ] && continue
  id="$(jq -r '.id' <<<"$line")"
  lesson="$(jq -r '.lesson' <<<"$line")"
  task="$(jq -r '.task' <<<"$line")"
  mistake="$(jq -r '.mistake_signal' <<<"$line")"

  declare -A avoided=( [with]=0 [without]=0 )
  for cond in with without; do
    for _ in $(seq 1 "$TRIALS"); do
      [ "$(run_trial "$cond" "$lesson" "$task" "$mistake")" = avoided ] && avoided[$cond]=$(( avoided[$cond] + 1 ))
    done
  done
  lift=$(( avoided[with] - avoided[without] ))
  printf '%-18s | %-22s | %-13s | %+d\n' \
    "$id" "${avoided[with]} / $TRIALS" "${avoided[without]} / $TRIALS" "$lift"
done < "$HERE/scenarios.jsonl"

echo
echo "NOTE: signal-grep is a coarse proxy — for a publishable number also"
echo "have a blind judge (LLM or human) grade each transcript. See README.md."
