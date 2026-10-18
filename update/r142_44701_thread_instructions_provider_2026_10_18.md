# #44701 Add a provider for thread-scoped instructions

## Upstream change

Rust exposes `ThreadInstructionsProvider` through `StartThreadOptions`. The
provider snapshot composes after global instructions and before repository
instructions, refreshes at model-request boundaries, is inherited as an applied
snapshot by live forks and subagents, and participates in Guardian reviewer
reuse keys. Thread instructions have an independent 10,000-token budget.

## Go architecture

Go has no extension registry or host instruction-provider interface. The
app-server `ThreadStartParams` protocol has no provider carrier, and
`baseInstructions` is a static caller override rather than a refreshable
snapshot, so there is no place to register a `ThreadInstructionsProvider`
equivalent.

The existing AGENTS.md path is the closest Go behavior:

- `turnStepInstructionsProvider` and `refreshedThreadInstructionText` reload
  global instructions at model-request boundaries.
- `refreshThreadGlobalInstructions` re-discovers repository instructions at
  turn boundaries when the cwd or trust snapshot changes.
- Subagents skip their own refresh and inherit the parent's applied snapshot.
- Guardian review sessions are not reused through a structured reuse key; Go
  restarts them using the compaction response ID and model hash, so the new
  thread-instructions reuse-key field has no counterpart.

## Conclusion

N/A by architecture. No Go code change is warranted for the extension provider
or its inheritance/reuse-key plumbing; the existing AGENTS.md refresh behavior
remains covered separately.

## Verification

- `go test ./appserver -run 'GlobalInstructions|ProjectInstructions|PromptCacheKey' -count=1`
