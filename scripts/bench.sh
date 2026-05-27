#!/usr/bin/env bash
# Run the agent-memory benchmark harness with consistent flags.
#
# Usage:
#   scripts/bench.sh                  # default: -count=3, all bench packages
#   scripts/bench.sh -count=5         # pass-through any go-test flags
#   scripts/bench.sh -bench=Fetch     # subset by name pattern
#
# Output goes to stdout; pipe to a file for comparison runs:
#   scripts/bench.sh > /tmp/bench-before.txt
#   # ... make changes ...
#   scripts/bench.sh > /tmp/bench-after.txt
#   benchstat /tmp/bench-before.txt /tmp/bench-after.txt
#
# benchstat is at golang.org/x/perf/cmd/benchstat — `go install` it for
# proper statistical comparison.

set -euo pipefail

PKGS=(
  ./internal/bench/...
  ./internal/markdown/...
  ./internal/memory/...
  ./internal/index/...
)

# Run-flag filter: skip non-bench tests so we're not paying for the
# regular unit suite each invocation.
EXTRA_FLAGS=("-bench=." "-benchmem" "-run=^$" "-count=3")

# Allow caller to override any of the above by passing extra args:
#   scripts/bench.sh -count=5 -bench=ProposeUpdate
# The user's args go LAST so they win over the defaults.
go test "${EXTRA_FLAGS[@]}" "$@" "${PKGS[@]}"
