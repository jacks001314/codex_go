# #45736 Guardian requests through /responses with identifying headers

## Upstream change

Rust routes Guardian review inference through the standard `/responses` route
and marks review requests with `x-codex-guardian: reviewer`. The marker is
limited to the preferred review model over Codex backend auth and backend-route
providers; marked reviews suppress the routing hint, service tier, and Guardian
credits request. `free_guardian` no longer gates the decision.

## Go status

Complete by equivalent implementation.

`model/responses_agent.go` keeps the standard `/responses` endpoint and marks
eligible review requests with `x-codex-guardian: reviewer`. The ordinary
routing hint, service tier, and Guardian credits body flag are removed only for
the marked preferred-model review. Overridden review models, API-key auth, and
plain providers remain unmarked.

`TestResponsesAgentRunnerGuardianRoutingLikeRust` covers the marked and
unmarked cases, including the body fields and routing hint.

## Verification

- `go test ./model -run 'GuardianRouting' -count=1`
