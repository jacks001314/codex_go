# #41365 Restrict async user messages to questions

## Upstream change

The `send_user_message_async` parameter becomes a concise question, and its
description reserves the tool for missing information, preferences,
constraints, clarification, or approval with user/task-appropriate abstraction.

## Go carrier

- `tool/send_user_message_async.go` carries the revised description and message
  parameter text.
- `TestSendUserMessageAsyncHandlerRestrictsToQuestionsLikeRust` pins the
  question-only guidance.
- The separate legacy `send_message_to_user_async` handler retains its own
  compatibility description.

## Verification

- `go test ./tool ./appserver -run 'Async|RequestUserInputAsync|SendMessageToUserAsync' -count=1`
