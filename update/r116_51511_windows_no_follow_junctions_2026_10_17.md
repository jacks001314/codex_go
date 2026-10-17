# Round 116 · #51511 Windows 10 drive-letter no-follow opens

Rust source: `9545947c6d`.

## Landed

- The no-follow component walk now checks the Windows reparse attribute instead
  of relying only on `os.ModeSymlink`. Go reports a directory junction as an
  irregular file (not a symlink), so junctions in any path component were
  silently traversed.
- Added platform helpers: `noFollowComponentIsReparse` uses
  `windows.GetFileAttributes` + `FILE_ATTRIBUTE_REPARSE_POINT` on Windows and
  is a no-op elsewhere. Symlinks keep their existing `ModeSymlink` check.
- Ordinary drive-letter paths and the DOS drive root remain accepted; the
  drive root has no reparse attribute. The Rust change needed a volume-root
  fallback because it performed strict NT opens on the `\??\C:\` alias; Go uses
  Win32 `CreateFile` on the plain DOS path with
  `FILE_FLAG_OPEN_REPARSE_POINT`, so no volume fallback is required.

## Verification

```text
go test ./execserver -run 'TestDriveLetterPathsAreNotReparseRejectedAndJunctionsStayBlocked' -count=1  # ok (was failing before)
go test ./execserver -run 'NoFollow|RegularFile|Junction|DriveLetter|FSRead|FSWrite|FSOpen' -count=1  # ok
go test ./execserver -count=1
# only the known host-shell baseline failure remains
go vet ./execserver  # ok
go build ./...  # ok
```

The regression test covers drive-letter reads, the drive root, rejection of
junction traversal for reads and opens, and rejection of creating files through
a junction.
