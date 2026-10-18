# #41944 Emit turn cost telemetry for ChatGPT sessions

## Upstream change

Rust queries workspace-visible per-turn estimates for ChatGPT-authenticated
sessions through the Codex or ChatGPT endpoint, tracks every completed response
ID, and emits `codex.turn.cost_microusd` only when the estimate is nonnegative,
visible, and settled for all observed responses. Error bodies never reach
turn-cost worker logs.

## Go implementation

- `chatgptapi.CloudClient.GetTurnCosts` posts `threads` and
  `include_settled_response_ids: true` to
  `/api/codex/usage/thread-estimates/query` or
  `/wham/usage/thread-estimates/query`, matching the client's path style.
- The turn completion path records each `ResponsesStreamEventCompleted`
  response ID through `turnCostTracker`, with the same 4096-response cap as
  Rust.
- `notifyTurnCompletedOnce` starts one best-effort worker per completed turn.
  The worker polls immediately, retries up to five times at 150-second
  intervals, and uses a 15-second request timeout.
- Only ChatGPT auth modes query the endpoint. Missing/null/negative estimates,
  empty settled lists, and partial settlement leave the metric unset.
- The counter carries `turn.id`, `conversation.id`, `turn.interrupted`, and
  `auth_mode=Chatgpt`. Failure logs contain only the HTTP status code.

## Verification

- `go test ./chatgptapi ./appserver -run 'TurnCost|SettledTurnCost|TurnCompletionStartsOneCostQuery' -count=1`
