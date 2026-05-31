# Demo

The repo README embeds `demo.gif` from this directory. It shows the core
loop: an agent proposes a durable decision → it **stages** → you see the
exact unified **diff** → **apply** → a later **fetch** surfaces it. Local,
git-native, reviewable.

## Files

- **`demo.sh`** — the canonical, runnable flow (the source of truth). It's
  plain CLI, so you can verify it works end-to-end:
  ```bash
  # with `agent-memory` on PATH
  bash docs/demo/demo.sh
  ```
- **`demo.tape`** — a [VHS](https://github.com/charmbracelet/vhs) script
  that types the same flow with pacing and renders `demo.gif`.

## Rendering the gif

```bash
# 1. install VHS: https://github.com/charmbracelet/vhs
# 2. put agent-memory on PATH:
go install github.com/xChuCx/agent-memory/cmd/agent-memory@latest
# 3. render (writes docs/demo/demo.gif):
vhs docs/demo/demo.tape
# 4. commit the gif
git add docs/demo/demo.gif && git commit -m "docs: render demo gif"
```

Keep `demo.sh` and `demo.tape` in sync — `demo.sh` is the one that's
tested by running it; the tape only changes pacing/presentation.
