package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/auth"
	"codex_go/codexapi"
)

const BedrockAPIKeyUnsupportedMessage = "Bedrock API key auth is only supported by the Amazon Bedrock model provider"
const defaultProviderAuthCommandTimeoutMS uint64 = DefaultProviderAuthTimeoutMS
const maxProviderAuthCommandErrorOutput = 512

type AuthHeaders struct {
	Headers                http.Header
	SignRequest            RequestSigner
	AgentIdentityTelemetry *codexapi.AgentIdentityTelemetry
}

type RequestSigner func(ctx context.Context, request *http.Request, body []byte) (*SignedRequest, error)

type SignedRequest struct {
	Body []byte
}

func (h *AuthHeaders) Apply(ctx context.Context, request *http.Request, body []byte) error {
	_, err := h.ApplyRequest(ctx, request, body)
	return err
}

func (h *AuthHeaders) ApplyRequest(ctx context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
	if h == nil || request == nil {
		return &SignedRequest{Body: body}, nil
	}
	addHeaders(request.Header, h.Headers)
	if h.SignRequest != nil {
		signed, err := h.SignRequest(ctx, request, body)
		if err != nil {
			return nil, err
		}
		if signed == nil {
			signed = &SignedRequest{Body: body}
		}
		if signed.Body == nil {
			signed.Body = body
		}
		applySignedRequestBody(request, signed.Body)
		return signed, nil
	}
	return &SignedRequest{Body: body}, nil
}

func applySignedRequestBody(request *http.Request, body []byte) {
	if request == nil || body == nil {
		return
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	request.ContentLength = int64(len(body))
}

func ResolveProviderAuth(snapshot *auth.AuthDotJSON, provider ProviderInfo) (AuthHeaders, error) {
	if snapshot != nil && snapshot.Mode() == "bedrock-api-key" {
		return AuthHeaders{}, errors.New(BedrockAPIKeyUnsupportedMessage)
	}
	if provider.Auth != nil {
		return ResolveProviderCommandAuth(context.Background(), provider.Auth)
	}
	if bearer, err := bearerTokenForProvider(provider); err != nil {
		return AuthHeaders{}, err
	} else if bearer != "" {
		return BearerAuthHeaders(bearer, "", false), nil
	}
	if snapshot == nil {
		return AuthHeaders{Headers: http.Header{}}, nil
	}
	return AuthHeadersFromAuth(*snapshot)
}

func AuthHeadersFromAuth(snapshot auth.AuthDotJSON) (AuthHeaders, error) {
	switch (&snapshot).Mode() {
	case "api-key":
		return BearerAuthHeaders(snapshot.OpenAIAPIKey, "", false), nil
	case "personal-access-token":
		return BearerAuthHeaders(snapshot.PersonalAccessToken, accountIDFromMap(snapshot.Tokens), fedrampFromMap(snapshot.Tokens)), nil
	case "agent-identity":
		return agentIdentityAuthHeaders(&snapshot)
	case "chatgpt", "chatgptAuthTokens":
		token := tokenFromMap(snapshot.Tokens)
		return BearerAuthHeaders(token, accountIDFromMap(snapshot.Tokens), fedrampFromMap(snapshot.Tokens)), nil
	case "bedrock-api-key":
		return AuthHeaders{}, errors.New(BedrockAPIKeyUnsupportedMessage)
	default:
		return AuthHeaders{Headers: http.Header{}}, nil
	}
}

func agentIdentityAuthHeaders(snapshot *auth.AuthDotJSON) (AuthHeaders, error) {
	record := auth.AgentIdentityRecordFromAuth(snapshot)
	if record == nil {
		return AuthHeaders{Headers: http.Header{}}, errors.New("agent identity auth does not expose a bearer token")
	}
	if record.TaskID == nil || strings.TrimSpace(*record.TaskID) == "" {
		return AuthHeaders{Headers: http.Header{}}, errors.New("agent identity auth is missing task id")
	}
	key := &agent.IdentityKey{
		AgentRuntimeID:        record.AgentRuntimeID,
		PrivateKeyPKCS8Base64: record.AgentPrivateKey,
	}
	headers := http.Header{}
	if strings.TrimSpace(record.AccountID) != "" {
		headers.Set("ChatGPT-Account-ID", strings.TrimSpace(record.AccountID))
	}
	if record.ChatGPTAccountFedRAMP {
		headers.Set("X-OpenAI-Fedramp", "true")
	}
	taskID := strings.TrimSpace(*record.TaskID)
	return AuthHeaders{
		Headers: headers,
		AgentIdentityTelemetry: &codexapi.AgentIdentityTelemetry{
			AgentID: strings.TrimSpace(record.AgentRuntimeID),
			TaskID:  taskID,
		},
		SignRequest: func(_ context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
			headerValue, err := agent.AuthorizationHeaderForAgentTask(key, taskID, time.Time{})
			if err != nil {
				return nil, err
			}
			if request != nil {
				request.Header.Set("Authorization", headerValue)
			}
			return &SignedRequest{Body: body}, nil
		},
	}, nil
}

func BearerAuthHeaders(token, accountID string, fedramp bool) AuthHeaders {
	headers := http.Header{}
	if strings.TrimSpace(token) != "" {
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if strings.TrimSpace(accountID) != "" {
		headers.Set("ChatGPT-Account-ID", strings.TrimSpace(accountID))
	}
	if fedramp {
		headers.Set("X-OpenAI-Fedramp", "true")
	}
	return AuthHeaders{Headers: headers}
}

func ResolveProviderCommandAuth(ctx context.Context, info *ProviderAuthInfo) (AuthHeaders, error) {
	if info == nil {
		return AuthHeaders{Headers: http.Header{}}, nil
	}
	command := strings.TrimSpace(info.Command)
	if command == "" {
		return AuthHeaders{}, errors.New("provider auth.command must not be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := providerAuthCommandTimeout(info)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, resolveProviderAuthProgram(command, info.CWD), info.Args...)
	if cwd := strings.TrimSpace(info.CWD); cwd != "" {
		cmd.Dir = cwd
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if execCtx.Err() != nil {
		return AuthHeaders{}, execCtx.Err()
	}
	if err != nil {
		return AuthHeaders{}, providerAuthCommandError(err, stderr.String())
	}
	token := tokenFromProviderAuthOutput(stdout.Bytes())
	if token == "" {
		return AuthHeaders{}, errors.New("provider auth command did not return a bearer token")
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	return BearerAuthHeaders(token, "", false), nil
}

func providerAuthCommandError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	message := "provider auth command failed: " + err.Error()
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return errors.New(message)
	}
	if len(stderr) > maxProviderAuthCommandErrorOutput {
		stderr = stderr[:maxProviderAuthCommandErrorOutput] + "..."
	}
	return errors.New(message + ": " + stderr)
}

func providerAuthCommandTimeout(info *ProviderAuthInfo) time.Duration {
	if info == nil || info.TimeoutMS == 0 {
		return time.Duration(defaultProviderAuthCommandTimeoutMS) * time.Millisecond
	}
	return time.Duration(info.TimeoutMS) * time.Millisecond
}

func resolveProviderAuthProgram(command string, cwd string) string {
	command = strings.TrimSpace(command)
	if command == "" || filepath.IsAbs(command) || !strings.ContainsAny(command, `/\`) {
		return command
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return filepath.Clean(command)
	}
	return filepath.Clean(filepath.Join(cwd, command))
}

func tokenFromProviderAuthOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	if token := tokenFromProviderAuthJSON(text); token != "" {
		return token
	}
	if strings.Contains(text, `\"`) {
		if token := tokenFromProviderAuthJSON(strings.ReplaceAll(text, `\"`, `"`)); token != "" {
			return token
		}
	}
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(line)
}

func tokenFromProviderAuthJSON(text string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return ""
	}
	for _, key := range []string{"authorization", "token", "access_token", "bearer_token"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func bearerTokenForProvider(provider ProviderInfo) (string, error) {
	if key, err := (&provider).APIKey(); err != nil || key != "" {
		return key, err
	}
	if strings.TrimSpace(provider.ExperimentalBearerToken) != "" {
		return strings.TrimSpace(provider.ExperimentalBearerToken), nil
	}
	return "", nil
}

func tokenFromMap(values map[string]any) string {
	for _, key := range []string{"access_token", "id_token", "token"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func accountIDFromMap(values map[string]any) string {
	for _, key := range []string{"account_id", "chatgpt_account_id", "accountId", "chatgptAccountId"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func fedrampFromMap(values map[string]any) bool {
	for _, key := range []string{"is_fedramp_account", "chatgpt_account_is_fedramp", "chatgptAccountIsFedramp", "fedramp"} {
		if value, ok := values[key].(bool); ok {
			return value
		}
	}
	return false
}
