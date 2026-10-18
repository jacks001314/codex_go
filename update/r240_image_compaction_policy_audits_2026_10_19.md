# Prepared images, compaction checkpoints, Guardian policy audits

Audited upstream commits:

- `#46065` Route prepared images through the attachment store
- `#46009` Centralize compaction checkpoint selection and validation
- `#45957` Centralize Guardian policy resolution in config and protocol

## Verdicts

- `#46065` complete: image preparation uses the attachment store, preserves
  durable/ephemeral intent, attaches usage/origin tags, and replaces inline data
  with file references when the store returns them.
- `#46009` complete: rollout reconstruction selects and validates compacted
  checkpoints, including replacement/resume metadata and turn attribution.
- `#45957` complete: managed Guardian policy requirements take precedence over
  auto-review values, policy templates render through the shared placeholder
  path, and app-server review sessions use the resolved policy.

## Verification

```text
go test ./model -run 'PrepareResponseInputImages|EphemeralReachesUploads' -count=1
go test ./rollout -run 'Compaction|Checkpoint' -count=1
go test ./config -run GuardianPolicy -count=1
go test ./appserver -run 'Guardian.*Policy|ReviewPolicy' -count=1
```
