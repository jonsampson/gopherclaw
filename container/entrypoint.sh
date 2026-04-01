#!/bin/bash
# GopherClaw agent container entrypoint.
# Reads ContainerInput JSON from stdin, configures the MCP server, runs claude CLI,
# and writes the response between GOPHERCLAW_OUTPUT_START / _END sentinels on stdout.
# The first line inside the sentinel block is SESSION_ID:<id>, which
# processGroup in main.go extracts to update the persisted session.
set -e

INPUT=$(cat)
PROMPT=$(printf "%s" "$INPUT" | jq -r ".Prompt")
SESSION_ID=$(printf "%s" "$INPUT" | jq -r ".SessionID // empty")
CHAT_JID=$(printf "%s" "$INPUT" | jq -r ".ChatJID // empty")
GROUP_FOLDER=$(printf "%s" "$INPUT" | jq -r ".GroupFolder // empty")
IS_MAIN=$(printf "%s" "$INPUT" | jq -r "if .IsMain then \"1\" else \"0\" end")

# Ensure IPC directories exist (also created in the image, but the mount
# may shadow them so we recreate here to be safe).
mkdir -p /workspace/ipc/messages /workspace/ipc/tasks

# Write MCP server config to ~/.claude/settings.json so the claude CLI
# loads gopherclaw-mcp and exposes the IPC tools to the agent.
# jq is used for safe JSON generation (handles special chars in JID/folder).
mkdir -p ~/.claude
jq -n \
    --arg chatJid "$CHAT_JID" \
    --arg groupFolder "$GROUP_FOLDER" \
    --arg isMain "$IS_MAIN" \
    '{
        mcpServers: {
            gopherclaw: {
                command: "/usr/local/bin/gopherclaw-mcp",
                args: [],
                env: {
                    GOPHERCLAW_IPC_DIR: "/workspace/ipc",
                    GOPHERCLAW_CHAT_JID: $chatJid,
                    GOPHERCLAW_GROUP_FOLDER: $groupFolder,
                    GOPHERCLAW_IS_MAIN: $isMain
                }
            }
        }
    }' > ~/.claude/settings.json

cd /workspace/group

# Resume an existing session when a session ID is available; start fresh otherwise.
if [ -n "$SESSION_ID" ]; then
    CLAUDE_JSON=$(claude --dangerously-skip-permissions \
        --resume "$SESSION_ID" --output-format json -p "$PROMPT" 2>&1)
else
    CLAUDE_JSON=$(claude --dangerously-skip-permissions \
        --output-format json -p "$PROMPT" 2>&1)
fi

NEW_SESSION_ID=$(printf "%s" "$CLAUDE_JSON" | jq -r ".session_id // empty")
RESPONSE=$(printf "%s" "$CLAUDE_JSON" | jq -r ".result // empty")

echo "---GOPHERCLAW_OUTPUT_START---"
printf "SESSION_ID:%s\n" "$NEW_SESSION_ID"
printf "%s\n" "$RESPONSE"
echo "---GOPHERCLAW_OUTPUT_END---"
