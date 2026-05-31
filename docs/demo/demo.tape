# VHS tape — renders docs/demo/demo.gif.
#   1. install vhs: https://github.com/charmbracelet/vhs
#   2. put `agent-memory` on PATH (go install … / build + PATH)
#   3. vhs docs/demo/demo.tape
#
# This mirrors docs/demo/demo.sh (the canonical, tested flow) but types the
# commands with pacing so the gif reads as a live session.

Output docs/demo/demo.gif
Require agent-memory

Set Shell bash
Set FontSize 18
Set Width 1240
Set Height 820
Set Padding 22
Set Theme "Catppuccin Mocha"

# --- hidden setup: throwaway repo + the decision body ---
Hide
Type "cd $(mktemp -d) && git init -q . && agent-memory init --name demo >/dev/null" Enter
Type "printf '## Use Postgres for the orders store\n<!-- @id: db-postgres -->\n\n**Date:** 2026-05-31\n**Status:** active\n**Confidence:** confirmed\n\nChose Postgres over MySQL: transactional guarantees + JSONB for order payloads.\n' > decision.md && clear" Enter
Show

Type "# An agent records a durable decision — it STAGES for human review" Sleep 600ms Enter Sleep 800ms
Type "agent-memory propose --intent record_decision --op append_section --path decisions.md --heading 'Use Postgres for the orders store' --heading-level 2 --content-file decision.md --source user:design-review --confidence confirmed" Sleep 500ms Enter
Sleep 2.5s

Type "# See EXACTLY what would land — a unified diff — then approve it" Sleep 600ms Enter Sleep 800ms
Type "agent-memory review --latest --diff" Sleep 500ms Enter
Sleep 3s
Type "agent-memory apply --latest" Sleep 500ms Enter
Sleep 2s

Type "# A later session fetches it back — memory persists, in your repo" Sleep 600ms Enter Sleep 800ms
Type "agent-memory fetch 'which database did we choose for orders'" Sleep 500ms Enter
Sleep 3.5s
