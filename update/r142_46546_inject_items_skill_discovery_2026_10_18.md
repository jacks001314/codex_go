# Rust #46546: skip skill discovery for initialized-thread injection

Upstream commit: `7e0463b568f8506f61b58ca46dcb1846e06ba31c`

## Result

Equivalent implementation.

- `thread/inject_items` parses and validates response items, then appends them
  directly to the persisted thread record and rollout.
- For ephemeral threads the items go through the runtime append path. For an
  active turn they are also enqueued into the steer mailbox.
- No turn context is constructed during injection, so host skill discovery is
  not run.
- A subsequent real turn still uses the full turn-context path and performs
  normal skill discovery and selected-skill loading.

## Verification

```text
go test ./appserver -run 'ThreadInjectItems|InjectItems' -count=1
```

Passed on 2026-10-18.
