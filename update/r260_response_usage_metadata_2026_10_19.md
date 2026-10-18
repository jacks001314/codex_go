# Response usage metadata

Rust upstream: `2c4a95736bea64256a50f7b8506bd33c181cc85a` -
`Expose response usage metadata in completion events (#41087)`.

## Alignment

Go decodes `usage_metadata.amount` from both SSE and WebSocket terminal
responses and carries it on `rawResponse/completed` as `usageMetadata`.

- absent metadata remains absent;
- an explicit empty object remains present with a null amount;
- string amounts are preserved without floating-point conversion;
- incomplete responses use the same usage-metadata extraction.

## Verification

```text
go test ./model -run 'UsageMetadataFromStreamEventData' -count=1
go test ./appserver -run 'RawResponseCompletedNotificationIncludesUsageMetadata|CacheWriteTokens' -count=1
go test ./parity -count=1
```
