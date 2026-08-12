"""Re-pin the contract oracle's OpenAPI document. Stdlib only.

The docs site embeds the whole document in its page HTML; no spec URL exists.
This reads the page on stdin and pins the hyperlift subset:

  curl -fsSL https://docs.spaceship.dev/ | python3 scripts/refresh_spec.py

The subset keeps the /v1/hyperlift/ paths and what they reference. The full
document covers every Spaceship product, and a full pin would fail the CI
drift check on the other products' changes.
"""

import json
import sys

PIN = "internal/testapi/testdata/spaceship-public-api.json"
MARKER = "const __redoc_state = "


def extract(page):
    i = page.find(MARKER)
    if i < 0:
        sys.exit("error: no embedded spec found; the docs site layout changed")

    # Parse exactly one JSON value; the page continues with JavaScript after it.
    state, _ = json.JSONDecoder().raw_decode(page[i + len(MARKER):])

    # The document arrives inline or as a JSON string.
    spec = state["spec"]["data"]
    return json.loads(spec) if isinstance(spec, str) else spec


# collect_refs records every "$ref" pointer address found anywhere under node.
def collect_refs(node, into):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str) and value.startswith("#/"):
                into.add(value)
            else:
                collect_refs(value, into)
    elif isinstance(node, list):
        for value in node:
            collect_refs(value, into)


# resolve follows "#/components/schemas/X" through the dict; None if it dangles.
def resolve(doc, pointer):
    node = doc
    for part in pointer[2:].split("/"):
        if not isinstance(node, dict) or part not in node:
            return None

        node = node[part]

    return node


def reduce(doc):
    paths = {url: ops for url, ops in doc["paths"].items() if url.startswith("/v1/hyperlift/")}
    if not paths:
        sys.exit("error: the published document has no /v1/hyperlift/ paths")

    # Keep every component the paths reference, following $refs until the set
    # stops growing. Anything less leaves dangling $refs.
    refs = set()
    collect_refs(paths, refs)

    while True:
        before = len(refs)
        for ref in list(refs):
            collect_refs(resolve(doc, ref), refs)

        if len(refs) == before:
            break

    # Keep only the entries whose own address was reached.
    components = {}
    for section, entries in doc["components"].items():
        kept = {name: entry for name, entry in entries.items() if f"#/components/{section}/{name}" in refs}
        if kept:
            components[section] = kept

    schemes = {name for requirement in doc.get("security", []) for name in requirement}

    kept = {name: scheme for name, scheme in doc["components"].get("securitySchemes", {}).items() if name in schemes}
    if kept:
        components["securitySchemes"] = kept

    return {
        "openapi": doc["openapi"],
        "info": doc["info"],
        "servers": doc.get("servers", []),
        "security": doc.get("security", []),
        "paths": paths,
        "components": components,
    }


def main():
    spec = reduce(extract(sys.stdin.read()))

    with open(PIN, "w", encoding="utf-8") as out:
        json.dump(spec, out, indent=2, ensure_ascii=False)
        out.write("\n")

    print(f"pinned {len(spec['paths'])} hyperlift paths to {PIN}", file=sys.stderr)
    print("now run: go test ./internal/testapi/", file=sys.stderr)


if __name__ == "__main__":
    main()
