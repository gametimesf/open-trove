#!/usr/bin/env bash
# Test the actual embedded response with no external network or storage access.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d ./guide-test.XXXXXX)
name="guide-test-$(basename "$tmp" | tr '[:upper:]' '[:lower:]')"
container=
cleanup() {
  if [[ -n "$container" ]]; then docker rm -f "$container" >/dev/null; fi
  docker image rm "$name:default" "$name:append" "$name:override" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
printf '  Example authoring →\r\n\n' > "$tmp/extra.txt"
cp docs/llms.txt "$tmp/default.txt"
{ cat docs/llms.txt; printf '\n\n'; cat "$tmp/extra.txt"; } > "$tmp/append.txt"
cp "$tmp/extra.txt" "$tmp/override.txt"
for mode in default append override; do
  args=(--build-arg LLMS_TXT_APPEND= --build-arg LLMS_TXT_OVERRIDE=)
  if [[ "$mode" == append ]]; then args+=(--build-arg "LLMS_TXT_APPEND=$tmp/extra.txt"); fi
  if [[ "$mode" == override ]]; then args+=(--build-arg "LLMS_TXT_OVERRIDE=$tmp/extra.txt"); fi
  docker build "${args[@]}" -t "$name:$mode" .
  container=$(docker run -d --network none \
    -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_EC2_METADATA_DISABLED=true \
    -e TROVE_CONFIG_YAML='{"store":{"type":"s3","s3":{"bucket":"test","region":"us-west-2"}}}' \
    "$name:$mode")
  ready=false
  for attempt in {1..30}; do
    if docker exec "$container" wget -qO- http://127.0.0.1:8080/llms.txt > "$tmp/actual.txt"; then ready=true; break; fi
    if [[ $(docker inspect -f '{{.State.Running}}' "$container") != true ]]; then break; fi
    sleep 1
  done
  if [[ "$ready" != true ]]; then docker logs "$container"; exit 1; fi
  cmp "$tmp/$mode.txt" "$tmp/actual.txt"
  docker rm -f "$container" >/dev/null
  container=
  echo "verified embedded image guide: $mode"
done
if docker build --build-arg "LLMS_TXT_APPEND=$tmp/missing.txt" -t "$name:append" .; then
  echo 'missing customization unexpectedly built' >&2; exit 1
fi
