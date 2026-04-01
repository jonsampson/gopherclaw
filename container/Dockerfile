# GopherClaw Agent Container
# Runs claude CLI in an isolated Linux environment.
# Credentials are injected by the host's OneCLI gateway — never passed here directly.

FROM debian:bookworm-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Create dedicated non-root user
RUN useradd -m -s /bin/bash claude

# Create workspace directories (group-isolated + shared global)
RUN mkdir -p /workspace/group /workspace/global \
    && chown -R claude:claude /workspace

# Add claude CLI install location to PATH
ENV PATH="/home/claude/.claude/local:${PATH}"

# Switch to non-root user for all subsequent steps
USER claude

# Install claude CLI via native binary installer
RUN curl -fsSL https://claude.ai/install.sh | bash

# Create entrypoint script.
# Reads ContainerInput JSON from stdin, runs claude --print, wraps output in sentinels.
# --continue resumes the most recent session in ~/.claude/ (mounted from the host's
# groups/<name>/.claude/ directory); on first run, starts a new session automatically.
RUN printf '#!/bin/bash\nset -e\nINPUT=$(cat)\nPROMPT=$(printf "%%s" "$INPUT" | jq -r ".Prompt")\ncd /workspace/group\necho "---GOPHERCLAW_OUTPUT_START---"\nclaude --dangerously-skip-permissions --continue --print "$PROMPT" 2>&1\necho "---GOPHERCLAW_OUTPUT_END---"\n' \
    > /home/claude/entrypoint.sh \
    && chmod +x /home/claude/entrypoint.sh

WORKDIR /workspace/group

# Input: ContainerInput JSON on stdin
# Output: agent response bracketed by GOPHERCLAW_OUTPUT_START / _END markers
ENTRYPOINT ["/home/claude/entrypoint.sh"]
