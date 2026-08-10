#!/bin/sh
# refresh-spec.sh — re-pin the contract oracle's OpenAPI document.
#
# The API reference (https://docs.spaceship.dev) embeds the OpenAPI document
# in the page HTML (Redoc's standard `__redoc_state` build output) and has no
# separate download, so this script extracts it from the page.
#
# Run at each release and whenever the Hyperlift contract changes upstream
# (see internal/testapi/testdata/README.md).
#
# Usage: make refresh-spec

set -eu

PIN="internal/testapi/testdata/spaceship-public-api.yaml"
URL="https://docs.spaceship.dev/"

[ -f "$PIN" ] || { echo "error: run from the repository root" >&2; exit 1; }

echo "Fetching ${URL}..." >&2
page="$(mktemp "${TMPDIR:-/tmp}/spec-page.XXXXXX")"
trap 'rm -f "$page"' EXIT
curl -fsSL "$URL" -o "$page"

python3 - "$page" "$PIN" <<'PY'
import json, sys, yaml

html = open(sys.argv[1], encoding="utf-8").read()
marker = "const __redoc_state = "
i = html.find(marker)
if i < 0:
    sys.exit("error: no embedded spec found; the docs site layout changed")
state, _ = json.JSONDecoder().raw_decode(html[i + len(marker):])
spec = state["spec"]["data"]
if isinstance(spec, str):
    spec = json.loads(spec)


class Dumper(yaml.SafeDumper):
    """Write every node in full, so no node becomes a YAML anchor."""

    def ignore_aliases(self, data):
        return True


with open(sys.argv[2], "w") as out:
    yaml.dump(spec, out, Dumper=Dumper, sort_keys=False,
              default_flow_style=False, width=100, allow_unicode=True)
print(f"paths: {len(spec['paths'])}, openapi {spec['openapi']}", file=sys.stderr)
PY

# Record the new provenance in the testdata README, so the pin and its
# documentation cannot drift apart. Both lines are rewritten whole, so they must
# stay on one line each in the README.
DOC="internal/testapi/testdata/README.md"
sum="$(shasum -a 256 "$PIN" | awk '{print $1}')"
today="$(date +%Y-%m-%d)"
sed -i.bak \
  -e "s/^- Retrieved: .*/- Retrieved: ${today}/" \
  -e "s/^- sha256: .*/- sha256: ${sum}/" \
  "$DOC" && rm -f "${DOC}.bak"

echo "Pinned. sha256 ${sum} (README updated)." >&2
echo "Now run: GOTOOLCHAIN=go1.25.0 go test ./internal/testapi/" >&2
