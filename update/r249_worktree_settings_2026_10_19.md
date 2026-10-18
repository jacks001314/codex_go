# Worktree settings parser

Rust upstream: `f832b2fe7b6e04baa8ea80ff377367a8cc5c90d5` -
`Add worktree settings parser (#40624)`.

## Alignment

Go's `worktree.FromDesktopConfig` resolves the existing `[desktop]` keys:

- `git-worktree-root` defaults to `<codexHome>/worktrees`;
- a configured root must be an absolute path;
- `worktree-auto-cleanup-enabled` defaults to true and must be boolean;
- `worktree-keep-count` defaults to 15 and must be a positive integer.

No separate worktree configuration format is introduced.

## Verification

```text
go test ./worktree -run 'FromDesktopConfig' -count=1
go test ./parity -count=1
```
