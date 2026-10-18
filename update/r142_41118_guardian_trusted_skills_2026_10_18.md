# #41118 Propagate trusted root skills to delegated workers

## Upstream change

Rust records verified user-owned skill paths as turn-matched Guardian evidence,
includes the current worker and root snapshot paths when reviewing delegated
workers, and drops the trust on the next root turn.

## Go architecture

Go has no `GuardianReviewEvidence` trusted-skill registry or delegated-worker
`GuardianRootSnapshot`. The same architectural absence was recorded for
`#43599`, where Rust trusted-skill evidence was moved into Guardian context.
Go expands skill inputs for the active turn but does not retain a bounded,
turn-matched trusted-path set for later delegated reviews.

The synchronous reviewer already carries trusted tool descriptions and retained
user/authorization context through its existing prompt sections.

## Conclusion

N/A by architecture; no Go code change is warranted.

## Verification

- `go test ./state -run 'GuardianToolDescriptions|GuardianRetained|GuardianRoot' -count=1`
