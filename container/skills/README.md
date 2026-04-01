# Container skills

Container skills are scripts and config files bundled into the agent container
image that extend what the Claude agent can do at runtime.

They live in this directory and are copied into the image at build time by
`container/Dockerfile`. The entrypoint (`container/entrypoint.sh`) sources any
`*.sh` files found in `/home/claude/skills/` before invoking the claude CLI,
so a skill script runs once per agent invocation in the context of the
container.

---

## What a container skill can do

| Task | How |
|------|-----|
| Register an MCP server | Write to `~/.claude/settings.json` (persisted via the `.claude/` volume mount) |
| Pre-install a CLI tool | Add a `RUN` layer to `Dockerfile` via the skill branch |
| Inject config or context | Write files into `/workspace/group/` before claude runs |
| Set environment variables | `export VAR=value` in the skill script |

---

## Writing a container skill

A container skill is typically a shell script at
`container/skills/<name>.sh`:

```sh
#!/bin/sh
# container/skills/my-mcp-tool.sh
# Registers my-tool as an MCP server for every agent run in this group.
set -e

SETTINGS="$HOME/.claude/settings.json"

# Only add if not already registered (idempotent).
if ! jq -e '.mcpServers["my-tool"]' "$SETTINGS" >/dev/null 2>&1; then
    tmp=$(mktemp)
    jq '.mcpServers["my-tool"] = {
        "command": "/home/claude/skills/bin/my-tool",
        "args": []
    }' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
fi
```

If `~/.claude/settings.json` does not exist yet, initialise it first:

```sh
if [ ! -f "$SETTINGS" ]; then
    echo '{"mcpServers":{}}' > "$SETTINGS"
fi
```

The script must be idempotent — it may run on every agent invocation.

---

## Session-state and MCP config persistence

`groups/<name>/.claude/` on the host is mounted at `/home/claude/.claude/`
inside every container run for that group. This means:

- MCP server registrations written to `~/.claude/settings.json` persist across
  runs without any action from gopherclaw.
- Claude session transcripts (used by `--resume`) are stored here and also
  persist.
- Config written by one container run is visible to the next.

This is why container skills that write MCP config are idempotent: the file
survives the `--rm` container lifecycle and is already present on the second
run.

---

## MCP servers that need to run as separate processes

If your MCP server is a long-running process (rather than a stdio tool invoked
on demand by the claude CLI), you have two options:

1. **Host-side process**: Run the MCP server on the host and expose it over TCP.
   Register it with `"type": "sse"` or `"type": "http"` in `settings.json` and
   use `host.docker.internal` (or the Docker bridge gateway IP) as the host.

2. **Bundled binary**: Copy the MCP server binary into the container image via
   a `Dockerfile` `RUN` or `COPY` layer (added by the skill branch). The skill
   script then starts it as a background process before claude runs.

---

## Applying a container skill as a skill branch

Container skills that are broadly useful are published as `skill/*` branches.
Apply one with:

```sh
git merge skill/mcp-brave-search   # example
```

The skill branch adds:
- The skill script under `container/skills/`
- Any required `Dockerfile` changes (e.g. installing the MCP server binary)
- A `SKILL.md` at the repo root documenting prerequisites and configuration

Rebuild the container image after merging:

```sh
./container/build.sh
```
