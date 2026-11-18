#!/usr/bin/env bash
set -euo pipefail
sha=${SSH_ORIGINAL_COMMAND:-}
[[ $sha =~ ^[0-9a-f]{40}$ ]]
cd "$HOME/looomer"
exec 9>"$HOME/.deploy.lock"
flock 9
git fetch origin main
if [[ $sha != "$(git rev-parse origin/main)" ]]; then
  echo "Staging requires the latest main commit." >&2
  exit 1
fi
git checkout --detach "$sha"
exec bash deploy/staging/apply.sh
