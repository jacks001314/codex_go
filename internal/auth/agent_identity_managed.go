package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"codex_go/internal/agent"
)

const maxAgentIdentityBootstrapAttempts = 3
const agentIdentityBootstrapFailureCooldown = time.Hour

var agentIdentityBootstrapCooldown agentIdentityBootstrapCooldownState

type agentIdentityBootstrapCooldownState struct {
	mu      sync.Mutex
	failure *cachedAgentIdentityBootstrapFailure
}

type cachedAgentIdentityBootstrapFailure struct {
	accountID      string
	authAPIBaseURL string
	retryAt        time.Time
	err            *AgentIdentityBootstrapUnavailableError
}

type AgentIdentityAuthPolicy string

const (
	AgentIdentityAuthPolicyJWTOnly     AgentIdentityAuthPolicy = "jwt-only"
	AgentIdentityAuthPolicyChatGPTAuth AgentIdentityAuthPolicy = "chatgpt-auth"
)

type AgentIdentityBootstrapOptions struct {
	CodexHome                 string
	AuthSnapshot              *AuthDotJSON
	StoreOptions              *StoreOptions
	HTTPClient                *http.Client
	ChatGPTBaseURL            string
	AgentIdentityAuthAPIURL   string
	ForcedChatGPTWorkspaceIDs []string
	SessionSource             string
	AgentVersion              string
}

type AgentIdentityBootstrapUnavailableError struct {
	Operation string
	Attempts  int
	Message   string
}

func (e *AgentIdentityBootstrapUnavailableError) Error() string {
	if e == nil {
		return ""
	}
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "agent identity bootstrap"
	}
	attempts := e.Attempts
	if attempts <= 0 {
		attempts = maxAgentIdentityBootstrapAttempts
	}
	return fmt.Sprintf("agent identity bootstrap unavailable after %d attempts during %s: %s", attempts, operation, strings.TrimSpace(e.Message))
}

func BootstrapManagedAgentIdentity(ctx context.Context, opts *AgentIdentityBootstrapOptions) (*AuthDotJSON, error) {
	if opts == nil {
		opts = &AgentIdentityBootstrapOptions{}
	}
	snapshot := cloneAuthDotJSON(opts.AuthSnapshot)
	if snapshot == nil {
		loaded, err := NewStoreWithOptions(opts.CodexHome, opts.StoreOptions).Load()
		if err != nil {
			return nil, err
		}
		snapshot = loaded
	}
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return nil, nil
	}
	binding := managedAgentIdentityBindingFromAuth(snapshot, opts.ForcedChatGPTWorkspaceIDs)
	if binding == nil {
		return nil, errors.New("ChatGPT auth is unavailable")
	}
	baseURL, err := agentIdentityAuthAPIBaseURL(opts.ChatGPTBaseURL, opts.AgentIdentityAuthAPIURL)
	if err != nil {
		return nil, err
	}
	if cached := agentIdentityBootstrapCooldownError(binding.AccountID, baseURL, time.Now()); cached != nil {
		return nil, cached
	}
	resolved, err := bootstrapManagedAgentIdentityWithBinding(ctx, opts, snapshot, binding, baseURL)
	if err != nil {
		if unavailable := agentIdentityBootstrapUnavailableFromError(err); unavailable != nil {
			recordAgentIdentityBootstrapCooldown(binding.AccountID, baseURL, unavailable, time.Now())
		} else {
			clearAgentIdentityBootstrapCooldown()
		}
		return nil, err
	}
	clearAgentIdentityBootstrapCooldown()
	return resolved, nil
}

func bootstrapManagedAgentIdentityWithBinding(ctx context.Context, opts *AgentIdentityBootstrapOptions, snapshot *AuthDotJSON, binding *managedAgentIdentityBinding, baseURL string) (*AuthDotJSON, error) {
	if record := agentIdentityRecordFromAny(snapshot.AgentIdentity); record != nil && recordMatchesManagedAgentIdentityBinding(record, binding) {
		updated, err := ensureAgentIdentityTask(ctx, opts.HTTPClient, baseURL, record)
		if err != nil {
			return nil, classifyAgentIdentityBootstrapError("agent task registration", err)
		}
		if updated {
			return persistManagedAgentIdentityRecord(opts, snapshot, record)
		}
		return FromAgentIdentityRecord(record), nil
	}
	record, err := registerManagedAgentIdentity(ctx, opts, baseURL, binding)
	if err != nil {
		return nil, err
	}
	return persistManagedAgentIdentityRecord(opts, snapshot, record)
}

func FromAgentIdentityRecord(record *AgentIdentityAuthRecord) *AuthDotJSON {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Email = cloneStringPtr(record.Email)
	clone.TaskID = cloneStringPtr(record.TaskID)
	return &AuthDotJSON{
		AuthMode:      "agent-identity",
		AgentIdentity: &clone,
	}
}

type managedAgentIdentityBinding struct {
	AccountID             string
	ChatGPTUserID         string
	Email                 *string
	PlanType              PlanType
	ChatGPTAccountFedRAMP bool
	AccessToken           string
}

func managedAgentIdentityBindingFromAuth(snapshot *AuthDotJSON, forcedWorkspaceIDs []string) *managedAgentIdentityBinding {
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return nil
	}
	accessToken := strings.TrimSpace(stringFromAny(snapshot.Tokens, "access_token"))
	if accessToken == "" {
		return nil
	}
	claims := ChatGPTClaimsFromJWT(accessToken)
	idClaims := ChatGPTClaimsFromJWT(stringFromAny(snapshot.Tokens, "id_token"))
	accountID := forcedWorkspaceID(forcedWorkspaceIDs)
	if accountID == "" {
		accountID = firstNonEmptyAccount(
			stringFromAny(snapshot.Tokens, "account_id"),
			stringFromAny(snapshot.Tokens, "chatgpt_account_id"),
			claims.AccountID,
			idClaims.AccountID,
		)
	}
	userID := firstNonEmptyAccount(
		stringFromAny(snapshot.Tokens, "chatgpt_user_id"),
		stringFromAny(snapshot.Tokens, "user_id"),
		claimStringFromJWT(accessToken, "chatgpt_user_id"),
		claimStringFromJWT(stringFromAny(snapshot.Tokens, "id_token"), "chatgpt_user_id"),
	)
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(userID) == "" {
		return nil
	}
	email := firstNonEmptyAccount(stringFromAny(snapshot.Tokens, "email"), claims.Email, idClaims.Email)
	plan := firstNonEmptyAccount(stringFromAny(snapshot.Tokens, "plan_type"), stringFromAny(snapshot.Tokens, "chatgpt_plan_type"), claims.PlanType, idClaims.PlanType)
	return &managedAgentIdentityBinding{
		AccountID:             strings.TrimSpace(accountID),
		ChatGPTUserID:         strings.TrimSpace(userID),
		Email:                 stringPtrIfNotEmpty(email),
		PlanType:              planFromString(plan),
		ChatGPTAccountFedRAMP: boolFromAny(snapshot.Tokens, "is_fedramp_account") || boolFromAny(snapshot.Tokens, "chatgpt_account_is_fedramp") || claims.FedRAMP || idClaims.FedRAMP,
		AccessToken:           accessToken,
	}
}

func forcedWorkspaceID(values []string) string {
	normalized := normalizedWorkspaceList(values)
	if len(normalized) == 1 {
		return normalized[0]
	}
	return ""
}

func claimStringFromJWT(jwt string, key string) string {
	claims := rawClaimsFromJWT(jwt)
	for _, valueMap := range claims {
		if value, ok := valueMap[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func rawClaimsFromJWT(jwt string) []map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	out := []map[string]any{}
	if nested, ok := raw[chatGPTAuthClaimNamespace].(map[string]any); ok {
		out = append(out, nested)
	}
	out = append(out, raw)
	return out
}

func recordMatchesManagedAgentIdentityBinding(record *AgentIdentityAuthRecord, binding *managedAgentIdentityBinding) bool {
	if record == nil || binding == nil {
		return false
	}
	if strings.TrimSpace(record.AccountID) != strings.TrimSpace(binding.AccountID) || strings.TrimSpace(record.ChatGPTUserID) != strings.TrimSpace(binding.ChatGPTUserID) {
		return false
	}
	_, err := agent.PublicKeySSHFromPrivateKeyPKCS8Base64(record.AgentPrivateKey)
	return err == nil
}

func ensureAgentIdentityTask(ctx context.Context, client *http.Client, baseURL string, record *AgentIdentityAuthRecord) (bool, error) {
	if record == nil {
		return false, errors.New("agent identity record is nil")
	}
	if record.TaskID != nil && strings.TrimSpace(*record.TaskID) != "" {
		return false, nil
	}
	taskID, err := retryAgentIdentityRegistration(func() (string, error) {
		return agent.RegisterAgentTask(ctx, client, baseURL, &agent.IdentityKey{
			AgentRuntimeID:        record.AgentRuntimeID,
			PrivateKeyPKCS8Base64: record.AgentPrivateKey,
		}, timeNowZero())
	})
	if err != nil {
		return false, err
	}
	record.TaskID = stringPtrIfNotEmpty(taskID)
	return true, nil
}

func registerManagedAgentIdentity(ctx context.Context, opts *AgentIdentityBootstrapOptions, baseURL string, binding *managedAgentIdentityBinding) (*AgentIdentityAuthRecord, error) {
	keyMaterial, err := agent.GenerateAgentKeyMaterial()
	if err != nil {
		return nil, err
	}
	runtimeID, err := retryAgentIdentityRegistration(func() (string, error) {
		return agent.RegisterAgentIdentity(ctx, opts.HTTPClient, baseURL, binding.AccessToken, binding.ChatGPTAccountFedRAMP, keyMaterial, agentIdentityABOM(opts), []string{"responsesapi"})
	})
	if err != nil {
		return nil, classifyAgentIdentityBootstrapError("agent identity registration", err)
	}
	record := &AgentIdentityAuthRecord{
		AgentRuntimeID:        runtimeID,
		AgentPrivateKey:       keyMaterial.PrivateKeyPKCS8Base64,
		AccountID:             binding.AccountID,
		ChatGPTUserID:         binding.ChatGPTUserID,
		Email:                 cloneStringPtr(binding.Email),
		PlanType:              binding.PlanType,
		ChatGPTAccountFedRAMP: binding.ChatGPTAccountFedRAMP,
	}
	if _, err := ensureAgentIdentityTask(ctx, opts.HTTPClient, baseURL, record); err != nil {
		return nil, classifyAgentIdentityBootstrapError("agent task registration", err)
	}
	return record, nil
}

func retryAgentIdentityRegistration(operation func() (string, error)) (string, error) {
	var last error
	for attempt := 1; attempt <= maxAgentIdentityBootstrapAttempts; attempt++ {
		value, err := operation()
		if err == nil {
			return value, nil
		}
		last = err
		if !agent.IsRetryableRegistrationError(err) {
			return "", err
		}
	}
	return "", last
}

func classifyAgentIdentityBootstrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if agent.IsRetryableRegistrationError(err) {
		return &AgentIdentityBootstrapUnavailableError{
			Operation: operation,
			Attempts:  maxAgentIdentityBootstrapAttempts,
			Message:   err.Error(),
		}
	}
	return err
}

func agentIdentityBootstrapCooldownError(accountID string, authAPIBaseURL string, now time.Time) *AgentIdentityBootstrapUnavailableError {
	if now.IsZero() {
		now = time.Now()
	}
	accountID = strings.TrimSpace(accountID)
	authAPIBaseURL = strings.TrimRight(strings.TrimSpace(authAPIBaseURL), "/")
	agentIdentityBootstrapCooldown.mu.Lock()
	defer agentIdentityBootstrapCooldown.mu.Unlock()
	failure := agentIdentityBootstrapCooldown.failure
	if failure != nil && failure.accountID == accountID && failure.authAPIBaseURL == authAPIBaseURL && failure.retryAt.After(now) {
		return cloneAgentIdentityBootstrapUnavailableError(failure.err)
	}
	agentIdentityBootstrapCooldown.failure = nil
	return nil
}

func recordAgentIdentityBootstrapCooldown(accountID string, authAPIBaseURL string, err *AgentIdentityBootstrapUnavailableError, now time.Time) {
	if err == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	agentIdentityBootstrapCooldown.mu.Lock()
	defer agentIdentityBootstrapCooldown.mu.Unlock()
	agentIdentityBootstrapCooldown.failure = &cachedAgentIdentityBootstrapFailure{
		accountID:      strings.TrimSpace(accountID),
		authAPIBaseURL: strings.TrimRight(strings.TrimSpace(authAPIBaseURL), "/"),
		retryAt:        now.Add(agentIdentityBootstrapFailureCooldown),
		err:            cloneAgentIdentityBootstrapUnavailableError(err),
	}
}

func clearAgentIdentityBootstrapCooldown() {
	agentIdentityBootstrapCooldown.mu.Lock()
	defer agentIdentityBootstrapCooldown.mu.Unlock()
	agentIdentityBootstrapCooldown.failure = nil
}

func agentIdentityBootstrapUnavailableFromError(err error) *AgentIdentityBootstrapUnavailableError {
	var unavailable *AgentIdentityBootstrapUnavailableError
	if errors.As(err, &unavailable) {
		return cloneAgentIdentityBootstrapUnavailableError(unavailable)
	}
	return nil
}

func cloneAgentIdentityBootstrapUnavailableError(err *AgentIdentityBootstrapUnavailableError) *AgentIdentityBootstrapUnavailableError {
	if err == nil {
		return nil
	}
	clone := *err
	return &clone
}

func persistManagedAgentIdentityRecord(opts *AgentIdentityBootstrapOptions, snapshot *AuthDotJSON, record *AgentIdentityAuthRecord) (*AuthDotJSON, error) {
	next := cloneAuthDotJSON(snapshot)
	if next == nil {
		next = &AuthDotJSON{AuthMode: "chatgpt"}
	}
	next.AgentIdentity = cloneAgentIdentityRecord(record)
	if strings.TrimSpace(opts.CodexHome) != "" {
		if err := NewStoreWithOptions(opts.CodexHome, opts.StoreOptions).Save(*next); err != nil {
			return nil, err
		}
	}
	return FromAgentIdentityRecord(record), nil
}

func cloneAgentIdentityRecord(record *AgentIdentityAuthRecord) *AgentIdentityAuthRecord {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Email = cloneStringPtr(record.Email)
	clone.TaskID = cloneStringPtr(record.TaskID)
	return &clone
}

func agentIdentityAuthAPIBaseURL(chatGPTBaseURL string, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimRight(strings.TrimSpace(explicit), "/"), nil
	}
	if strings.TrimSpace(chatGPTBaseURL) == "" {
		environment := agent.ChatGPTProduction
		return (&environment).AuthAPIBaseURL(), nil
	}
	environment, err := agent.EnvironmentFromChatGPTBaseURL(chatGPTBaseURL)
	if err != nil {
		return "", err
	}
	return (&environment).AuthAPIBaseURL(), nil
}

func agentIdentityABOM(opts *AgentIdentityBootstrapOptions) *agent.BillOfMaterials {
	version := ""
	source := "cli"
	if opts != nil {
		version = strings.TrimSpace(opts.AgentVersion)
		if strings.TrimSpace(opts.SessionSource) != "" {
			source = strings.TrimSpace(opts.SessionSource)
		}
	}
	abom := agent.BuildABOM(version, source, runtime.GOOS)
	return &abom
}

func timeNowZero() time.Time {
	return time.Time{}
}
