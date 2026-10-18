# Resumable misalignment details

Rust upstream: `7276d67081dd4149a15c72972e639b0d707915a4` -
`Expose resumable misalignment details through app server (#40952)`.

## Alignment

Go's `TurnError` exposes optional `MisalignmentErrorDetails`:

- open-ended `errorType`;
- localized `detailedExplanation`;
- `steer.message` for the next-turn continuation instruction;
- later `reviewTarget` support from the upstream follow-up.

The details are decoded from the Responses API error, attached to turn failure
notifications, preserved in thread resume/read response history, and serialized
in the app-server wire shape.

## Verification

```text
go test ./appserver -run 'MisalignmentDetails|MisalignmentJSON|MisalignmentPolicy|ReviewTarget' -count=1
go test ./parity -count=1
```
