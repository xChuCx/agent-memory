# @xchucx/agent-memory

npm launcher for [**agent-memory**](https://github.com/xChuCx/agent-memory) —
local, git-native project memory for AI coding agents, exposed over MCP.

This package is a tiny, dependency-free shim. On first run it downloads the
prebuilt `agent-memory` binary for your platform from the matching GitHub
release, verifies it against the release's SHA-256 checksums, caches it under
`~/.cache/agent-memory/<version>/`, and execs it with your arguments. There is
no compiled code here and nothing to build.

## Use as an MCP server

```jsonc
// .mcp.json (or your client's MCP config)
{
  "mcpServers": {
    "agent-memory": {
      "command": "npx",
      "args": ["-y", "@xchucx/agent-memory", "mcp", "--root", "."]
    }
  }
}
```

## Use as a CLI

```bash
npx -y @xchucx/agent-memory --help
npx -y @xchucx/agent-memory init
```

Prefer a managed binary, `go install`, or building from source? See the
[main README](https://github.com/xChuCx/agent-memory#install). Apache-2.0.
