#!/bin/sh
set -eu

input=$(cat)
tool=$(printf '%s' "$input" | jq -r '.tool_name // ""')
payload=$(printf '%s' "$input" | jq -r '(.tool_input.file_path // .tool_input.path // .tool_input.command // "")')

deny() {
  jq -n --arg reason "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'
  exit 0
}

if [ "$tool" = "Bash" ] && printf '%s' "$payload" | grep -Eq '(^|[[:space:]])(rm|unlink)[[:space:]].*_test\.go'; then
  deny "Deleting _test.go files is forbidden"
fi

if printf '%s' "$payload" | grep -Eq '_test\.go' && [ -f "${CLAUDE_PROJECT_DIR:-.}/.claude/green.lock" ]; then
  deny "Test files are locked during the green implementation phase"
fi

exit 0
