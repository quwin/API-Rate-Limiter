#!/usr/bin/env bash
set -euo pipefail

URL="${URL:-http://localhost:8080/api/hello}"
API_KEY="${API_KEY:-demo-key-1}"
REQUESTS="${REQUESTS:-20}"

allowed=0
rejected=0
unauthorized=0

for i in $(seq 1 "$REQUESTS"); do
  code=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "X-API-Key: $API_KEY" \
    "$URL")

  if [ "$code" = "200" ]; then
    allowed=$((allowed + 1))
  elif [ "$code" = "429" ]; then
    rejected=$((rejected + 1))
  elif [ "$code" = "401" ]; then
    unauthorized=$((unauthorized + 1))
  fi

  echo "$i status=$code allowed=$allowed rejected=$rejected unauthorized=$unauthorized"
done

echo
echo "Total allowed:      $allowed"
echo "Total rejected:     $rejected"
echo "Total unauthorized: $unauthorized"