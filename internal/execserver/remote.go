package execserver

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	RemoteSecurityProfile = "noise_hybrid_ik_v1"

	noiseChannelSuite         = "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256"
	mlkem768PublicKeyBytes    = 1184
	errorBodyPreviewMaxBytes  = 4096
	defaultRemoteBackoff      = time.Second
	defaultRemoteMaxBackoff   = 30 * time.Second
	defaultRemoteHTTPTimeout  = 30 * time.Second
	defaultRemoteDialTimeout  = 10 * time.Second
	defaultRemoteCloseTimeout = time.Second
)

type RemoteEnvironmentConfig struct {
	BaseURL       string
	EnvironmentID string
	Name          string
	AuthHeaders   http.Header
	HTTPClient    *http.Client
	Dial          func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
	Backoff       time.Duration
	MaxBackoff    time.Duration
}

type RemotePublicKey struct {
	Suite             string `json:"suite"`
	X25519PublicKey   string `json:"x25519_public_key"`
	MLKEM768PublicKey string `json:"mlkem768_public_key"`
}

type remoteRegistrationRequest struct {
	SecurityProfile   string          `json:"security_profile"`
	ExecutorPublicKey RemotePublicKey `json:"executor_public_key"`
}

type remoteRegistrationResponse struct {
	EnvironmentID          string `json:"environment_id"`
	URL                    string `json:"url"`
	SecurityProfile        string `json:"security_profile"`
	ExecutorRegistrationID string `json:"executor_registration_id"`
}

type registryErrorBody struct {
	Error *registryError `json:"error"`
}

type registryError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func RunRemoteEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return errors.New("environment registry base URL is required")
	}
	cfg.EnvironmentID = strings.TrimSpace(cfg.EnvironmentID)
	if cfg.EnvironmentID == "" {
		return errors.New("environment id is required for remote exec-server registration")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = remoteHTTPClient()
	}
	if cfg.Dial == nil {
		cfg.Dial = websocket.Dial
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultRemoteBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultRemoteMaxBackoff
	}

	key, err := generateRemotePublicKey()
	if err != nil {
		return fmt.Errorf("failed to generate Noise relay identity: %w", err)
	}

	server := NewServer()
	backoff := cfg.Backoff
	registration, err := registerRemoteEnvironment(ctx, cfg, key)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		conn, response, err := cfg.Dial(ctx, registration.URL, &websocket.DialOptions{})
		if err == nil {
			backoff = cfg.Backoff
			serveErr := server.serveWebSocketConnection(ctx, conn)
			if ctx.Err() != nil {
				return nil
			}
			if serveErr != nil {
				err = serveErr
			}
		}
		if response != nil && response.StatusCode >= 400 && response.StatusCode < 500 {
			registration, err = registerRemoteEnvironment(ctx, cfg, key)
			if err != nil {
				return err
			}
		}
		if err := sleepContext(ctx, backoff); err != nil {
			return nil
		}
		backoff *= 2
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}
}

func registerRemoteEnvironment(ctx context.Context, cfg RemoteEnvironmentConfig, key RemotePublicKey) (*remoteRegistrationResponse, error) {
	body, err := json.Marshal(remoteRegistrationRequest{
		SecurityProfile:   RemoteSecurityProfile,
		ExecutorPublicKey: key,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, remoteEndpointURL(cfg.BaseURL, "/cloud/environment/"+cfg.EnvironmentID+"/register"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, values := range cfg.AuthHeaders {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := cfg.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteRegistryStatusError(response)
	}
	var decoded remoteRegistrationResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode environment registry response: %w", err)
	}
	if decoded.EnvironmentID != cfg.EnvironmentID {
		return nil, errors.New("exec-server protocol error: environment registry returned a different environment id")
	}
	if decoded.SecurityProfile != RemoteSecurityProfile {
		return nil, fmt.Errorf("exec-server protocol error: environment registry returned unsupported security profile `%s`", decoded.SecurityProfile)
	}
	if strings.TrimSpace(decoded.URL) == "" || strings.TrimSpace(decoded.ExecutorRegistrationID) == "" {
		return nil, errors.New("exec-server protocol error: environment registry returned incomplete Noise registration data")
	}
	return &decoded, nil
}

func remoteRegistryStatusError(response *http.Response) error {
	status := response.Status
	bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyPreviewMaxBytes+1))
	body := string(bodyBytes)
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("environment registry authentication error: environment registry authentication failed (%s): %s", status, registryErrorMessage(body))
	}
	code, message := registryHTTPErrorMessage(body)
	codeSuffix := ""
	if code != "" {
		codeSuffix = ", " + code
	}
	return fmt.Errorf("environment registry request failed (%s%s): %s", status, codeSuffix, message)
}

func registryHTTPErrorMessage(body string) (string, string) {
	var decoded registryErrorBody
	if err := json.Unmarshal([]byte(body), &decoded); err == nil && decoded.Error != nil {
		message := strings.TrimSpace(decoded.Error.Message)
		if message == "" {
			message = previewErrorBody(body)
		}
		if message == "" {
			message = "empty error body"
		}
		return strings.TrimSpace(decoded.Error.Code), message
	}
	message := previewErrorBody(body)
	if message == "" {
		message = "empty or malformed error body"
	}
	return "", message
}

func registryErrorMessage(body string) string {
	var decoded registryErrorBody
	if err := json.Unmarshal([]byte(body), &decoded); err == nil && decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return strings.TrimSpace(decoded.Error.Message)
	}
	if preview := previewErrorBody(body); preview != "" {
		return preview
	}
	return "empty error body"
}

func previewErrorBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if len(trimmed) > errorBodyPreviewMaxBytes {
		trimmed = trimmed[:errorBodyPreviewMaxBytes]
	}
	return trimmed
}

func remoteEndpointURL(baseURL string, path string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func generateRemotePublicKey() (RemotePublicKey, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return RemotePublicKey{}, err
	}
	mlkemPublicKey := make([]byte, mlkem768PublicKeyBytes)
	if _, err := rand.Read(mlkemPublicKey); err != nil {
		return RemotePublicKey{}, err
	}
	return RemotePublicKey{
		Suite:             noiseChannelSuite,
		X25519PublicKey:   base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		MLKEM768PublicKey: base64.StdEncoding.EncodeToString(mlkemPublicKey),
	}, nil
}

func remoteHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultRemoteHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
