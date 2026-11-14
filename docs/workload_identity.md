# Workload identity assertion sources

The Go implementation exchanges workload identity assertions for short-lived OpenAI access tokens. Tokens are cached in memory; refresh acquires another assertion. The process session pins the source configuration, including the SPIFFE socket, audience and selected identity.

## File source

File acquisition remains the default. Configure `OPENAI_FEDERATION_RULE_ID` and an absolute `OPENAI_IDENTITY_TOKEN_FILE`. `OPENAI_IDENTITY_TOKEN_SOURCE=file` is optional. The file is reopened for each exchange so its owner can rotate the assertion.

## SPIFFE source

The SPIFFE source is implemented for Unix platforms. Native Unix integration testing is still pending for this synchronization; see the verification section below. Non-Unix platforms return an explicit unsupported-platform error.

Configure the process that owns authentication, using deployment-specific values:

```sh
export OPENAI_FEDERATION_RULE_ID="<federation-rule-id>"
export OPENAI_IDENTITY_TOKEN_SOURCE="spiffe"
export SPIFFE_ENDPOINT_SOCKET="unix:///run/spire/agent.sock"
export OPENAI_SPIFFE_AUDIENCE="<audience-accepted-by-the-federation-rule>"
export OPENAI_SPIFFE_ID="spiffe://example.org/workload"
unset OPENAI_IDENTITY_TOKEN_FILE
```

`OPENAI_SPIFFE_ID` may be omitted. Exactly one JWT-SVID must be returned even when an identity is selected; ambiguous responses are rejected. An explicitly empty ID is invalid. Source names are case-sensitive and must not contain surrounding spaces. File and SPIFFE sources cannot be configured together. SPIFFE audience/ID settings require the SPIFFE source. An ambient `SPIFFE_ENDPOINT_SOCKET` alone does not select workload identity.

Only absolute Unix socket URIs are accepted; `unix:/path/to/socket` is also accepted. TCP, an authority/host, query, fragment, NUL and the root path alone are rejected. A remote app-server needs the socket on the host that owns authentication, rather than only on the TUI client host.

## Acquisition and errors

Each exchange opens a new connection and calls `/SpiffeWorkloadAPI/FetchJWTSVID` with `workload.spiffe.io=true`. Connection and request share a ten-second timeout. The assertion is held in memory for the exchange. The client checks the assertion size, response cardinality, SPIFFE subject and requested audience. Signature and lifetime verification remain the token endpoint's responsibility; a `kid` header is not required by local selection checks.

Agent error text is discarded; only the gRPC status code is retained. Unavailable acquisition and the upstream retryable statuses (Unavailable, DeadlineExceeded, ResourceExhausted, Aborted, Canceled, Internal) are transient. PermissionDenied, Unauthenticated, ambiguous identity and invalid assertions are not transient.

The agent's attestation policy controls which processes may acquire the identity. This implementation does not grant sandboxed tools additional socket permissions. OpenAI-specific workload identity settings are removed from model-launched environments; the general `SPIFFE_ENDPOINT_SOCKET` variable remains available. Issuers may reuse JWT-SVIDs, so deployments using replay protection must configure the issuer accordingly.

## Verification status

Windows auth regressions and in-memory gRPC protocol tests have passed. Linux test binaries have cross-compiled; that does not establish native Unix runtime correctness. The native test covers Unix socket acquisition, HTTP token exchange, caching, agent restart and coalesced concurrent refresh:

```sh
go test ./auth -run '^TestSPIFFEUnixExchangeRefreshAfterAgentRestart$' -count=1
```

Run it on Unix before treating this synchronization as platform-validated. The full synchronization is not complete. Implementation provenance: Rust Codex commit `6242064da0412b4c12176a2e53ef91afb333002d` (#61207), with SPIFFE parsing behavior checked against `spiffe 0.18.0`.
