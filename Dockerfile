# Container image for the agent-memory MCP server.
#
# Build a static, CGo-free binary (modernc.org/sqlite is pure Go) and ship it
# on a minimal distroless base. The server speaks MCP over stdio, so run it
# interactively with the repo bind-mounted at /workspace:
#
#   docker run -i --rm -v "$PWD:/workspace" ghcr.io/xchucx/agent-memory
#
# Glama and other registries build this image, start the server, and verify it
# answers MCP introspection (initialize + tools/list) — which works on an empty
# /workspace because tool registration is static and the store opens lazily.

# ---- build ----
FROM golang:1.25 AS build
WORKDIR /src

# Cache module downloads before copying the rest of the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/xChuCx/agent-memory/internal/cli.ProgramVersion=${VERSION}" \
    -o /out/agent-memory ./cmd/agent-memory

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:latest
COPY --from=build /out/agent-memory /usr/local/bin/agent-memory
WORKDIR /workspace

# stdio MCP server rooted at the mounted repo. Clients keep stdin open for the
# JSON-RPC channel (docker run -i); the server exits on stdin EOF.
ENTRYPOINT ["agent-memory", "mcp", "--root", "/workspace"]
