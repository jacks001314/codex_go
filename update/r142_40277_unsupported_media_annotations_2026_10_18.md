# Rust #40277: preserve annotations when omitting unsupported media

Upstream commit: `aec653daa9873bf44517a623fd033722737817a8`

## Result

Implemented.

The image placeholder path already tagged `images.unsupported`, but audio had
no corresponding strip path and no mixed-media regression covered annotation
alignment.

- Added `AudioOmittedPlaceholder` and `StripAudio`, rewriting `input_audio`
  parts to text with `audio.unsupported`.
- `ForPrompt` now accepts an optional audio-support flag while keeping the
  existing image-only call sites source-compatible.
- `TestForPromptTagsOmittedImageAndAudioWithoutShiftingKindsLikeRust` verifies
  surrounding `user.text` kinds stay in place while image and audio kinds become
  `images.unsupported` and `audio.unsupported`.

## Verification

```text
go test ./context -count=1
go vet ./context
```

Passed on 2026-10-18.
