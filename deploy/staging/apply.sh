#!/usr/bin/env bash
set -euo pipefail
export RELEASE=$(git rev-parse HEAD)
compose=(docker compose --env-file "$HOME/staging.env" -f deploy/staging/compose.yaml)
git archive HEAD | docker build -t "looomer-staging:$RELEASE" -f deploy/staging/Dockerfile -
"${compose[@]}" up -d --wait db
"${compose[@]}" run --rm --no-deps ingest init
"${compose[@]}" run --rm --no-deps ingest register src/looomer/apps/photo_curation/app.json
"${compose[@]}" up -d --wait
for attempt in {1..30}; do
  if [[ $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/v1/jobs/probe) == 401 ]]; then
    exit 0
  fi
  sleep 1
done
echo "Staging API failed its readiness check" >&2
exit 1
