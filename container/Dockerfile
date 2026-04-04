# GopherClaw Agent Container
# Runs opencode CLI in an isolated Linux environment.
# Credentials are injected by the host's OneCLI gateway — never passed here directly.

FROM debian:bookworm-slim

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    nodejs \
    npm \
    && rm -rf /var/lib/apt/lists/*

# Create dedicated non-root user
RUN useradd -m -s /bin/bash opencode

# Create workspace directories (group-isolated + shared global)
RUN mkdir -p /workspace/group /workspace/global \
    && chown -R opencode:opencode /workspace

# Switch to non-root user for all subsequent steps
USER opencode

# Install opencode CLI (or claude CLI)
RUN npm install -g opencode

# Copy entrypoint script
COPY --chown=opencode:opencode entrypoint.sh /home/opencode/entrypoint.sh
RUN chmod +x /home/opencode/entrypoint.sh

WORKDIR /workspace/group

# Input: ContainerInput JSON on stdin
# Output: agent response bracketed by GOPHERCLAW_OUTPUT_START / _END markers
ENTRYPOINT ["/home/opencode/entrypoint.sh"]
