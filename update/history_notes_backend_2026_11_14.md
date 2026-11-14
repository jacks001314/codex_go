# History/notes backend boundary (#61251)

## Behavior

- Go's history/notes HTTP backend already accepts opaque JSON object arguments and adds the trusted `context` object (`session_id`, `current_agent_name`) before transport.
- Catalog overrides change the model-facing description/schema without replacing the runtime request or endpoint.
- An unknown future catalog parameter is preserved verbatim rather than being decoded into a fixed request type.

## Regression

`TestHistoryNotesForwardsUnknownCatalogArgumentsLikeRust` sends `future_catalog_parameter` through `Backend.Call` and asserts it reaches the HTTP endpoint alongside the trusted context.

## Evidence

- `go test ./historynotes -count=1` passed.
- Log: `update/history_notes_backend_2026_11_14.log`.
