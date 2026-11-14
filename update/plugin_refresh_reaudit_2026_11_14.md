# #61129 refresh re-audit

Retrieved the exact public upstream patch https://github.com/openai/codex/commit/1d529e0266b0ed42753f94564b61204370ee1445.patch. Saved as upstream_61129.patch, SHA256 6BA053623C2A0B06CEE733EB55DD9349C9B434636BC1E902275228D595FDEA8D.

The upstream observable regression publishes two plugins in one batch: same-version changed content stays cached; a new version replaces content. Go cache_refresh.go already checks versions before replacement, derives pending keys from ID+source, validates current configuration before activation, coalesces forced requests, and emits a batch callback after processing. RuntimeRouter.handlePluginList starts non-curated or configured-source refresh for local/full inventories.

Added TestCacheRefreshBatchUpdatesOnlyNewVersions with two installed plugin trees, published same/new versions, background worker completion synchronization, both file-content assertions and no-repeat scheduling. It passes with the existing production implementation. The materializer is a deterministic local fixture; it is not a claim of the upstream real-Git first-list end-to-end test passing.

Full plugin tests passed 2.308s; go vet ./plugin exited 0. Existing tests also cover source change before activation, unloaded configured installations, forced refresh coalescing and failed-download preservation. Plan now records these verified facts instead of generic unaudited status. Full appserver session 64036 remains running. Overall synchronization incomplete; no commit/push.
