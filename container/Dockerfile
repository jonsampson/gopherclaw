# syntax=docker/dockerfile:1
# GopherClaw Agent Container
# Runs claude CLI in an isolated Linux environment.
# Credentials are injected by the host's OneCLI gateway — never passed here directly.

# Stage 1 — build the MCP server binary (static, no cgo)
FROM golang:1.24-bookworm AS mcp-builder
WORKDIR /src
COPY mcp-server/go.mod ./
RUN go mod download
COPY mcp-server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /gopherclaw-mcp .

# Stage 2 — runtime
FROM debian:bookworm-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Copy MCP server binary from builder stage
COPY --from=mcp-builder /gopherclaw-mcp /usr/local/bin/gopherclaw-mcp

# Create dedicated non-root user
RUN useradd -m -s /bin/bash claude

# Create workspace directories (group-isolated + shared global + IPC)
RUN mkdir -p /workspace/group /workspace/global /workspace/ipc/messages /workspace/ipc/tasks \
    && chown -R claude:claude /workspace

# Add claude CLI install location to PATH
ENV PATH="/home/claude/.claude/local:${PATH}"

# Switch to non-root user for all subsequent steps
USER claude

# Install claude CLI via native binary installer
RUN curl -fsSL https://claude.ai/install.sh | bash

# Copy entrypoint script
COPY --chown=claude:claude entrypoint.sh /home/claude/entrypoint.sh
RUN chmod +x /home/claude/entrypoint.sh

WORKDIR /workspace/group

# Input: ContainerInput JSON on stdin
# Output: agent response bracketed by GOPHERCLAW_OUTPUT_START / _END markers
ENTRYPOINT ["/home/claude/entrypoint.sh"]
