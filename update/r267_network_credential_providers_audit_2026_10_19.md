# Network proxy credential providers audit

Rust upstream: `1bfd383890 Add configurable credential providers to the network proxy (#44056)`.

## Verdict

Complete. Go parses configured network-proxy credential providers, matches
hosts/routes, applies credentials for the requested environment snapshot,
honors MITM/hook requirements and local-binding bypass rules, and carries bounded
credential attribution through the proxy path.

## Verification

```text
go test ./network -run 'CredentialProvider|CredentialBroker' -count=1
go test ./appserver -run 'CredentialProvider|CredentialBroker' -count=1
```
