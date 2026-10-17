# Round 116 · completed symbol-ledger reconciliation

The following upstream rows were previously classified `not_applicable` by
symbol-name matching, but their behavior is now implemented and covered by Go
regressions:

| PR | Go carrier | Regression |
| --- | --- | --- |
| `#53963` | `memories.sanitizeMemoryResponseItem` removes ciphertext before budget accounting; bounded rollout normalization clears legacy payload copies | `TestSanitizeMemoryResponseItemDropsCiphertextLikeRust`, `TestBoundedRolloutInputDropsCiphertextWithoutDisplacingEvidence` |
| `#53706` | `config.rewriteExternalTermsPreservingLinks` | `TestExternalRewritePreservesLinksLikeRust`, `TestExternalRewritePreservesCursorLinks` |
| `#51908` | config/method gate, root-agent catalog filter, legacy capability mapping | `TestExperimentalRequestUserInputEnabledLikeRust`, `TestRequestUserInputAsyncAvailableLikeRust`, `TestBuildToolRegistryMapsLegacyAsyncCapabilityToRequestUserInputAsync` |
| `#53369` | turn construction no longer requires an advertised message-board post tool | `TestDisableDirectMessageAllowsUnavailableBoardLikeRust` |

## Verification

```text
go test ./memories -run 'Ciphertext|BoundedRolloutInput' -count=1       # ok
go test ./config -run ExternalRewrite -count=1                         # ok
go test ./appserver -run 'RequestUserInputAsyncAvailable|DisableDirectMessageAllowsUnavailableBoard' -count=1  # ok
go test ./turn -run 'LegacyAsync|DisabledRequestUserInput' -count=1    # ok
```

`update/r109_symbol_coverage_2026_10_17.tsv` now records these four rows as
`complete`.
