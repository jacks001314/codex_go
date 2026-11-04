# Voice when the desktop locks

The TUI ends a voice call when it detects that the OS session has locked,
including while the call is connecting or retrying. Voice stays on through a
screen saver until the OS reports a lock. After unlocking, run `/voice` to
start a new call.

Platform detection:

- Windows reads the session lock flag from `WTSQuerySessionInformationW`.
- macOS reads `CGSessionCopyCurrentDictionary` and only stops once the
  password grace period has elapsed (builds without cgo cannot observe locks).
- Linux follows logind's `LockedHint` through `loginctl`. KDE Plasma can
  report a lock while its screen saver can still be dismissed without a
  password, so voice may stop at the start of that grace period.

In a headless terminal or with a locker that never reports a lock, use
`/voice` to end the call before leaving.
