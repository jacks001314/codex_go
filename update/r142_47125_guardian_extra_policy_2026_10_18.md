# Rust #47125: Guardian extra policy configuration

Upstream commit: `26cb4d73e2ce25575644038d7af5beb2440d0ed0`

## Result

Equivalent implementation was already present.

- Managed `guardian_extra_policy` replaces the `[auto_review].extra_policy`
  config value; blank managed or configured values are treated as unset.
- Requirements parsing, clone/emptiness checks, and cloud/local requirement
  merging preserve the policy text.
- Template rendering performs tenant-policy substitution first and then
  substitutes every `{{ extra_policy }}` occurrence without recursively
  interpreting placeholders from the inserted text.
- The composed policy reaches the Guardian reviewer instructions.

## Verification

```text
go test ./config -run 'GuardianExtraPolicy|GuardianPolicy' -count=1
go test ./state -run 'GuardianInstructions|GuardianExtraPolicy' -count=1
go test ./appserver -run 'Guardian.*ExtraPolicy|GuardianReviewInstructions' -count=1
```

Passed on 2026-10-18.
