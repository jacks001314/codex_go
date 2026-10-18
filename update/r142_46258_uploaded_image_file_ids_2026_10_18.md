# #46258 Uploaded image file IDs in user message display history

## Upstream change

Image preparation runs before user message events are emitted, and the prepared
uploaded file IDs are copied into the live and persisted display history while
preserving text spans and image detail. Position tracking prevents failed reads,
omitted inputs, and duplicate URLs from shifting later images.

## Go status

Complete by equivalent implementation.

Go carries uploaded image references through `TurnUserInput.FileID`,
`session.ContentPart.FileID`, and the v2 `fileId` display shape.
`preparedUserMessageInputItem` prepares structured user images before the turn
emits its user message, caches the prepared blocks for the turn, and
`runtimeUserPromptSessionItemWithPreparedContent` uses those blocks for both
session history and display content. Text elements come from the original
blocks, while image detail and file references come from the prepared blocks.

The existing regression
`TestUploadedImageFileIDsReachUserMessageDisplayHistoryLikeRust` verifies the
live input item, persisted session content, display `fileId`, and preserved
text spans.

## Verification

- `go test ./appserver -run 'UploadedImageFileIDs|UserInputImageReferences|SessionPartFileReference|TurnUserInputFileReference' -count=1`
