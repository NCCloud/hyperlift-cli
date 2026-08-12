`spaceship-public-api.json` is the hyperlift subset of the published Spaceship
External API OpenAPI document: the `/v1/hyperlift/` paths, the components they
reference, and the security schemes. The contract tests validate the mock
against it, so the pin must track the live contract. CI re-extracts the
published document on every PR and fails when this pin drifts; refresh it with
`make refresh-spec` (needs `curl` and `python3`) and commit the result. The
file's git history is its provenance.

The pin includes the published documentation's example code samples, whose
`X-API-Key` / `X-API-Secret` headers carry the placeholder value
`REPLACE_KEY_VALUE`. Those are documentation placeholders, not credentials, but
naive secret scanners may flag them.
