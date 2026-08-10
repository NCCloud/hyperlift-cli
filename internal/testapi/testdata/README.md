`spaceship-public-api.yaml` is a pinned copy of the published Spaceship External
API OpenAPI document. The contract tests validate the mock against it, so the
pin must track the live contract: refresh it with `make refresh-spec` at each
release and whenever the Hyperlift contract changes upstream. The documentation
site offers no separate download for the spec — the page embeds the whole
document in its HTML, and the script extracts it from there.

- Retrieved: 2026-08-10
- Declared version: OpenAPI 3.0.0, API `info.version` 1.0.0 (this field does not
  change between publications — identify the pin by the checksum below)
- sha256: 1d93c5190e0d367178141215967be684ab16670b0be13ea3417f54cd7ee1e2ac

The file includes the published documentation's example code samples, whose
`X-API-Key` / `X-API-Secret` headers carry the placeholder value
`REPLACE_KEY_VALUE`. Those are documentation placeholders, not credentials, but
naive secret scanners may flag them.
