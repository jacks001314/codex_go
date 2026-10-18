# Round 761: managed Amazon Bedrock AWS access keys (#40481)

## Changes

- `LoginAccountParams` supports experimental `amazonBedrockAccessKeys` with
  access key id, secret, optional session token, and region.
- `AuthDotJSON` stores `bedrock_access_keys` with mode
  `bedrock-access-keys`; both secrets are redacted from derived formatting.
- Account/auth-mode reporting maps the managed login to Amazon Bedrock with
  `UsesCodexManagedCredentials = true` and wire mode `bedrockAccessKeys`.
- App-server login configures `model_provider = amazon-bedrock` and the selected
  AWS region, guarded by the existing Bedrock provider/credential-export
  conflict checks.
- The Bedrock provider builds a static AWS credential provider and signs
  requests with SigV4, including `X-Amz-Security-Token`.
- Logout clears managed `model_provider` and `model_providers.amazon-bedrock.aws`
  region/profile settings while leaving external AWS credentials and `.env`
  untouched.

## Verification

- `go test ./auth ./model ./appserver -run 'BedrockAccessKeys|Bedrock' -count=1`
- `go vet ./auth ./model ./appserver`
- `go test ./parity -count=1`
