# Function call output notification media

Rust upstream: `2008d27e98d7b46170d2d464b36dbf97008611b8` -
`Filter media from function call output notifications (#41427)`.

## Alignment

Rust strips inline image and audio content from `FunctionCallOutput` thread
items in app-server notifications while retaining text and encrypted content.
Text-only outputs remain unchanged.

Go's `withoutNotificationMedia` already routes item notifications through the
recursive `withoutNotificationItemMedia` filter. It removes `input_image` and
`input_audio` content items from function call output arrays and preserves all
other entries, including `input_text` and `encrypted_content`. The commit's
additional auth-recovery notification variants need no media branch because Go
leaves notification types outside the three media-bearing methods unchanged.

## Verification

```text
go test ./appserver -run 'WithoutNotificationMediaOmitItemMediaLikeRust' -count=1
go test ./parity -count=1
```
