# #44038 Handle copied credentials in the broker and shell snapshots

## Upstream change

Shell startup can copy a credential into another variable and unset its source.
Rust discovers supported credentials embedded in environment values without
losing provider/source restrictions, distinguishes adjacent credentials and
path false positives, and masks/restores known dummy values in shell snapshots.

## Go carriers

- Configured credential providers build embedded patterns and scan non-canonical
  environment values for complete credential tokens.
- `minEmbeddedCredentialLength` plus the distinctive-literal-prefix exception
  mirrors Rust's short embedded credential rule.
- Host bindings remain provider/source-owned, so unbound enterprise credentials
  do not acquire the default cloud host.
- Generated dummies that embed another provider's complete token are rejected.
- `MaskSnapshotText`, `RestoreSnapshotCredentials`, and the brokered shell
  snapshot wrapper mask real values, restore known dummies on fail-open launch,
  and preserve exported functions.

## Verification

- `go test ./network ./tool -run 'CredentialBroker|CredentialAlias|EmbeddedAlias|SnapshotCredential|BrokeredCredential' -count=1`
