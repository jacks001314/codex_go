# #45506 Background persistence for steered user input

## Upstream change

Rust adds `PersistContext::SteeredUserInput` and
`allows_background_persistence()` so stores may enqueue accepted steered user
input and fence it at a later flush or shutdown. Tool outputs remain
synchronous even in mixed batches, and synchronous stores remain valid.

## Go status

Not applicable by architecture.

Go's thread store is synchronous and durable at append time; there is no
pluggable `PersistContext` or background checkpoint queue to select. The Rust
change explicitly permits stores to keep synchronous persistence for every
context, so Go already provides the required durability semantics for steered
input and tool outputs.

## Verification

- `go test ./appserver -run 'Steer|PendingInput' -count=1`
