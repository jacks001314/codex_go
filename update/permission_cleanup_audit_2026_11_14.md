# Rust permission API cleanup audit: #61144 and #61194

Fetched exact public commit patches from github.com/openai/codex/commit/ce3031018ef130e653ed4d9c5bb971652de97161.patch and f242b49c8adda8f7bb1e5ff225df22295ac8e07b.patch. File inventories and SHA256 are in permission_cleanup_patch_manifest_2026_11_14.json.

#61144 changes 9 files: config/mod.rs removes Config::set_legacy_sandbox_policy and replaces duplicated permission construction with from_legacy_sandbox_policy_for_cwd; eight test/setup files now explicitly set profiles and roots. No protocol surface changes. Go has no corresponding Config setter to remove.

#61194 changes 4 files: models.rs removes a redundant non-CWD constructor and inlines its equivalent construction in template conversion; permissions.rs deletes two wrappers for legacy root materialization; two test files remove wrapper-specific cases and move extra-root metadata checks to the workspace-write constructor. No new wire field. Go uses its existing profile constructors, CWD-aware config resolver and workspace enforcement rather than those Rust wrapper APIs.

Existing Go coverage includes TestPermissionProfileLegacySandboxPolicy, TestWorkspaceWritableRootsIncludeCWDAndProtectMetadata, TestWorkspaceWritableRootsListAWSMetadata and TestResolveSandboxPermissionProfileWithWorkspaceRootsAnchorsProjectRootsLikeRust. Full config tests passed 1.142s; sandbox passed 0.106s; go vet ./config ./sandbox exited 0.

Disposition: these exact internal cleanup deltas need no matching Go API deletion. This does not certify general permission behavior, frozen-profile retargeting, missing-root semantics on every platform, or #61134/#61195; those remain separate unfinished work. Overall synchronization incomplete; appserver session 16306 remains running, no commit/push.
