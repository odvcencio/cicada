#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
  echo 'usage: check-commit-hygiene.sh <base-commit> <head-commit>' >&2
  exit 2
fi
base=$(git rev-parse --verify "$1^{commit}")
head=$(git rev-parse --verify "$2^{commit}")
commits=$(git rev-list --reverse "$base..$head")
count=0
failed=0
while IFS= read -r commit; do
  [[ -n "$commit" ]] || continue
  count=$((count + 1))
  emails=$(git show -s --format='%ae%n%ce' "$commit")
  # Here-strings, not pipes: grep -q exits at its first match, and under
  # pipefail the writer's broken pipe would read as "no match" and let a
  # prohibited address or trailer pass.
  if grep -Eiq '^noreply@anthropic\.com$' <<<"$emails"; then
    echo "commit $commit: prohibited author or committer address" >&2
    failed=1
  fi
  message=$(git show -s --format=%B "$commit")
  trailers=$(grep -Ei '(co[-]authored[-]by|generated[[:space:]]+with)' <<<"$message" || true)
  if grep -Eiq '(anthropic|claude|openai|chatgpt|codex|copilot|gemini|cursor|windsurf|devin|replit|grok|tabnine|aider|buckley|(^|[^[:alnum:]_])AI([^[:alnum:]_]|$))' <<<"$trailers"; then
    echo "commit $commit: prohibited attribution trailer" >&2
    failed=1
  fi
done <<< "$commits"
[[ "$failed" == 0 ]] || exit 1
echo "Commit hygiene passed for $count commits."
