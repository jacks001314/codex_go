# Rust #40266: preserve annotations when filtering forked developer history

Upstream commit: `184ff338ad7d737b0bd6d5e913a070cfd5c666bd`

## Result

Fixed.

`retainForkedDeveloperMessage` already realigned
`content_item_kinds` when a fragment was removed by its harness-owned kind, but
not when the same filter removed a fragment by its developer-text marker. A
surviving content part could therefore inherit the removed part's positional
classification.

The filter now records every text or kind removal and rewrites both the raw
`internal_chat_message_metadata_passthrough.content_item_kinds` metadata and
the session item projection from the retained parts.

`TestFilterInheritedDeveloperFragmentsRealignsKindsAfterTextFilterLikeRust`
pins the text-filter path with a `<multi_agent_role>` fragment followed by
preserved developer context, verifying the surviving classification remains
`context.compaction_summary`.

## Verification

```text
go test ./appserver -run 'FilterInheritedDeveloper|MultiAgentCatalog|Fork.*Content|ContentItemKinds' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
