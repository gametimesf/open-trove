#!/usr/bin/env sh
set -eu

patterns='gteng\.co|trove\.labs|foundever|auth_proxy_base_url|edgar\.castillo|749724771193|657349741171|GOPRIVATE'

if git grep -n -I -E "$patterns" -- \
  ':!scripts/check-public-source.sh' \
  ':!CHANGELOG.md'
then
  echo "Public-source boundary check failed: deployment-specific content is tracked." >&2
  exit 1
fi

echo "Public-source boundary check passed."
