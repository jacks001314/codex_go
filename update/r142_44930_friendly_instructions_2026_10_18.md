# #44930 Embed friendly instructions in bundled GPT-5.4 and GPT-5.5

## Upstream change

Bundled GPT-5.4 and GPT-5.5 replace selectable personality templates with
fixed friendly instructions. Personality is unsupported for these models, and
a submitted personality override leaves their instructions unchanged.

## Go status

Complete by equivalent implementation.

`model/catalog_test.go` verifies that a fixed-instruction model reports no
personality support and that every personality value returns the bundled
template unchanged. `TestBundledCatalogFixedTemplatesReportNoPersonalitySupport`
checks the same for the bundled GPT-5.4 and GPT-5.5 catalog entries.

## Verification

- `go test ./model -run 'Bundled|Personality|Instructions' -count=1`
