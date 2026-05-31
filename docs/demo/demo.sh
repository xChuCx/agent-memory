#!/usr/bin/env bash
#
# Reproducible 30-second showcase. It is the canonical, runnable source of
# truth for the demo gif (docs/demo/demo.gif, rendered via demo.tape).
#
# Requires `agent-memory` on PATH:
#   go install github.com/xChuCx/agent-memory/cmd/agent-memory@latest
# or build from source and add ./bin to PATH.
#
# Run it directly to verify the flow:  bash docs/demo/demo.sh
set -euo pipefail

demo="$(mktemp -d)"
cd "$demo"
git init -q .
agent-memory init --name demo >/dev/null

cat > decision.md <<'MD'
## Use Postgres for the orders store
<!-- @id: db-postgres -->

**Date:** 2026-05-31
**Status:** active
**Confidence:** confirmed

Chose Postgres over MySQL: transactional guarantees + JSONB for order payloads.
MD

echo "# An agent records a durable decision — it STAGES for human review:"
agent-memory propose --intent record_decision --op append_section \
  --path decisions.md --heading "Use Postgres for the orders store" --heading-level 2 \
  --content-file decision.md --source user:design-review --confidence confirmed

echo
echo "# See EXACTLY what would land (unified diff), then approve it:"
agent-memory review --latest --diff
agent-memory apply --latest

echo
echo "# A later session fetches it back — memory persists, in your repo:"
agent-memory fetch "which database did we choose for orders"
