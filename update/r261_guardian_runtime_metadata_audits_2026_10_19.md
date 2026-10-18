# Guardian runtime and review-thread metadata audits

Rust upstream refactors:

- `#47301` Separate local agent runtime state from controller handles
- `#47302` Use compact display metadata for Guardian review threads

## Verdict

Both are not applicable as Go architecture mismatches. Go's runtime agent
controller and agent registry already separate controller handles from runtime
state. Synchronous Guardian reviews run as model sessions and do not create
persisted review threads, so there is no review-thread metadata row to compact.
