# Browser and computer-use requirements

Rust upstream: `0f1a30b5c245913e953e761d27ef0a9e177df565` -
`Expose browser and computer-use requirements through app-server (#40000)`.

## Alignment

Go's `ConfigRequirements` wire model already exposes the complete policy:

- browser-use origin policies, default origin policy, history/global approval
  gates, download/upload/CDP access, and approval lifetime;
- computer-use persistent approval and default app access;
- macOS bundle-ID allow/deny rules;
- Windows AUMID and executable publisher/product/binary rules;
- top-level browser-and-computer-use allowance.

Requirements parsing, JSON marshaling and `configRequirements/read` app-server
responses are covered by existing tests.

## Verification

```text
go test ./config -run 'BrowserUse|ComputerUse|Requirements' -count=1
go test ./appserver -run 'ConfigRequirementsRead' -count=1
go test ./parity -count=1
```
