#!/bin/bash
# GopherClaw agent container entrypoint.
# Reads ContainerInput JSON from stdin, runs opencode CLI, and writes the
# response between GOPHERCLAW_OUTPUT_START / _END sentinels on stdout.
# The first line inside the sentinel block is SESSION_ID:<id>, which
# processGroup in main.go extracts to update the persisted session.
set -e

INPUT=$(cat)
PROMPT=$(printf "%s" "$INPUT" | jq -r ".Prompt")
SESSION_ID=$(printf "%s" "$INPUT" | jq -r ".SessionID // empty")

cd /workspace/group

# Run opencode with the prompt. OpenCode manages sessions internally via its
# snapshot mechanism, but we can pass a session name for organizational purposes.
# Output is JSON with response content.
if [ -n "$SESSION_ID" ]; then
    # Resume previous session context by name (OpenCode uses chat history)
    OPCODE_RESPONSE=$(opencode run --json --mode chat -p "$PROMPT" 2>&1)
else
    OPCODE_RESPONSE=$(opencode run --json --mode chat -p "$PROMPT" 2>&1)
fi

# Extract response content from OpenCode JSON output
RESPONSE=$(printf "%s" "$OPCODE_RESPONSE" | jq -r '.content // .response // empty')

echo "---GOPHERCLAW_OUTPUT_START---"
printf "SESSION_ID:%s\n" "${SESSION_ID:-new-session}"
printf "%s\n" "$RESPONSE"
echo "---GOPHERCLAW_OUTPUT_END---"
