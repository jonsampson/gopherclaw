#!/bin/bash
# GopherClaw agent container entrypoint.
# Reads ContainerInput JSON from stdin, runs claude CLI, and writes the
# response between GOPHERCLAW_OUTPUT_START / _END sentinels on stdout.
# The first line inside the sentinel block is SESSION_ID:<id>, which
# processGroup in main.go extracts to update the persisted session.
set -e

INPUT=$(cat)
PROMPT=$(printf "%s" "$INPUT" | jq -r ".Prompt")
SESSION_ID=$(printf "%s" "$INPUT" | jq -r ".SessionID // empty")

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
