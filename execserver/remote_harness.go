package execserver

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	clatter "github.com/shurlinet/go-clatter"

	"codex_go/envutil"
)

const (
	CodexExecServerNoiseRegistryURLEnvVar      = "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL"
	CodexExecServerNoiseEnvironmentIDEnvVar    = "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID"
	CodexExecServerNoiseChatGPTAccountIDEnvVar = "CODEX_EXEC_SERVER_NOISE_CHATGPT_ACCOUNT_ID"
)

// CodexExecServerNoiseAuthTokenEnvVar is the shared execution-server credential
// constant (single definition across the exec server and the environment
// scrubber, mirroring Rust #38941).
const CodexExecServerNoiseAuthTokenEnvVar = envutil.CodexExecServerNoiseAuthTokenEnvVar

type NoiseRendezvousConnectBundle struct {
	WebSocketURL            string
	EnvironmentID           string
	ExecutorRegistrationID  string
	ExecutorPublicKey       RemotePublicKey
	HarnessKeyAuthorization string
}

type NoiseRendezvousConnectProvider interface {
	ConnectBundle(context.Context, RemotePublicKey) (*NoiseRendezvousConnectBundle, error)
}

type NoiseRendezvousConnectProviderFunc func(context.Context, RemotePublicKey) (*NoiseRendezvousConnectBundle, error)

func (f NoiseRendezvousConnectProviderFunc) ConnectBundle(ctx context.Context, key RemotePublicKey) (*NoiseRendezvousConnectBundle, error) {
	return f(ctx, key)
}

type registryNoiseRendezvousConnectProvider struct {
	config RemoteEnvironmentConfig
}

type remoteConnectRequest struct {
	HarnessPublicKey RemotePublicKey `json:"harness_public_key"`
}

type remoteConnectResponse struct {
	EnvironmentID           string          `json:"environment_id"`
	URL                     string          `json:"url"`
	SecurityProfile         string          `json:"security_profile"`
	ExecutorRegistrationID  string          `json:"executor_registration_id"`
	ExecutorPublicKey       RemotePublicKey `json:"executor_public_key"`
	HarnessKeyAuthorization string          `json:"harness_key_authorization"`
}

func NewRegistryNoiseRendezvousConnectProvider(config RemoteEnvironmentConfig) (NoiseRendezvousConnectProvider, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if config.BaseURL == "" {
		return nil, errors.New("environment registry base URL is required")
	}
	config.EnvironmentID = strings.TrimSpace(config.EnvironmentID)
	if config.EnvironmentID == "" {
		return nil, errors.New("environment id is required for remote exec-server registration")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = remoteHTTPClient()
	}
	return &registryNoiseRendezvousConnectProvider{config: config}, nil
}

func RegistryNoiseRendezvousConnectProviderFromEnv() (NoiseRendezvousConnectProvider, bool, error) {
	return registryNoiseRendezvousConnectProviderFromValues(
		optionalNoiseEnvironmentValue(CodexExecServerNoiseRegistryURLEnvVar),
		optionalNoiseEnvironmentValue(CodexExecServerNoiseEnvironmentIDEnvVar),
		optionalNoiseEnvironmentValue(CodexExecServerNoiseAuthTokenEnvVar),
		optionalNoiseEnvironmentValue(CodexExecServerNoiseChatGPTAccountIDEnvVar),
	)
}

func registryNoiseRendezvousConnectProviderFromValues(
	registryURL *string,
	environmentID *string,
	authToken *string,
	chatGPTAccountID *string,
) (NoiseRendezvousConnectProvider, bool, error) {
	if registryURL == nil && environmentID == nil && authToken == nil {
		return nil, false, nil
	}
	if registryURL == nil || environmentID == nil || authToken == nil {
		return nil, false, fmt.Errorf(
			"Noise environment requires %s, %s, and %s",
			CodexExecServerNoiseRegistryURLEnvVar,
			CodexExecServerNoiseEnvironmentIDEnvVar,
			CodexExecServerNoiseAuthTokenEnvVar,
		)
	}
	if !validRemoteHeaderValue(*authToken) {
		return nil, false, errors.New("environment registry bearer token is not a valid HTTP header")
	}
	headers := http.Header{"Authorization": []string{"Bearer " + *authToken}}
	if chatGPTAccountID != nil {
		if !validRemoteHeaderValue(*chatGPTAccountID) {
			return nil, false, errors.New("ChatGPT account id is not a valid HTTP header")
		}
		headers.Set("ChatGPT-Account-ID", *chatGPTAccountID)
	}
	provider, err := NewRegistryNoiseRendezvousConnectProvider(RemoteEnvironmentConfig{
		BaseURL:       *registryURL,
		EnvironmentID: *environmentID,
		AuthHeaders:   headers,
	})
	if err != nil {
		return nil, false, err
	}
	return provider, true, nil
}

func optionalNoiseEnvironmentValue(name string) *string {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func validRemoteHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if (value[i] < 0x20 && value[i] != '\t') || value[i] == 0x7f {
			return false
		}
	}
	return true
}

func (p *registryNoiseRendezvousConnectProvider) ConnectBundle(ctx context.Context, harnessPublicKey RemotePublicKey) (*NoiseRendezvousConnectBundle, error) {
	if p == nil {
		return nil, errors.New("Noise rendezvous connect provider is nil")
	}
	body, err := json.Marshal(remoteConnectRequest{HarnessPublicKey: harnessPublicKey})
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, defaultRemoteDialTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		connectCtx,
		http.MethodPost,
		remoteEndpointURL(p.config.BaseURL, "/cloud/environment/"+p.config.EnvironmentID+"/connect"),
		strings.NewReader(string(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, values := range p.config.AuthHeaders {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := p.config.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("environment registry request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, remoteRegistryStatusError(response)
	}
	var decoded remoteConnectResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode environment registry response: %w", err)
	}
	if decoded.EnvironmentID != p.config.EnvironmentID {
		return nil, errors.New("exec-server protocol error: environment registry returned a different environment id")
	}
	if decoded.SecurityProfile != RemoteSecurityProfile {
		return nil, fmt.Errorf("exec-server protocol error: environment registry returned unsupported security profile `%s`", decoded.SecurityProfile)
	}
	if strings.TrimSpace(decoded.URL) == "" || strings.TrimSpace(decoded.ExecutorRegistrationID) == "" || strings.TrimSpace(decoded.HarnessKeyAuthorization) == "" {
		return nil, errors.New("exec-server protocol error: environment registry returned incomplete Noise connection data")
	}
	return &NoiseRendezvousConnectBundle{
		WebSocketURL:            decoded.URL,
		EnvironmentID:           decoded.EnvironmentID,
		ExecutorRegistrationID:  decoded.ExecutorRegistrationID,
		ExecutorPublicKey:       decoded.ExecutorPublicKey,
		HarnessKeyAuthorization: decoded.HarnessKeyAuthorization,
	}, nil
}

func DialNoiseRendezvousClient(
	ctx context.Context,
	provider NoiseRendezvousConnectProvider,
	options DialClientOptions,
) (*Client, error) {
	if provider == nil {
		return nil, errors.New("Noise rendezvous connect provider is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	clientName := strings.TrimSpace(options.ClientName)
	if clientName == "" {
		clientName = "codex-go-unified-exec"
	}
	identity, err := generateRemoteNoiseIdentity()
	if err != nil {
		return nil, fmt.Errorf("failed to generate Noise harness identity: %w", err)
	}
	client := &Client{
		clientName:   clientName,
		nextID:       1,
		nextHTTPID:   1,
		pending:      map[int64]chan clientCallResult{},
		sessions:     map[string]*clientProcessSession{},
		httpStreams:  map[string]*HTTPBodyStream{},
		inboundIDs:   map[int64]struct{}{},
		inboundSlots: make(chan struct{}, MaxInFlightServerRequests),
		done:         make(chan struct{}),
		cleanup:      identity.Destroy,
	}
	client.open = func(ctx context.Context, resumeSessionID string, handleNotification func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, error) {
		bundle, err := provider.ConnectBundle(ctx, identity.PublicKey())
		if err != nil {
			return nil, nil, err
		}
		wire, err := dialNoiseHarnessConnection(ctx, bundle, identity, options.HTTPClient)
		if err != nil {
			return nil, nil, err
		}
		initialized, err := initializeClientConnection(ctx, wire, clientName, resumeSessionID, handleNotification)
		if err != nil {
			return nil, nil, err
		}
		return wire, initialized, nil
	}
	conn, initialized, err := client.open(ctx, options.ResumeSessionID, client.handleNotification)
	if isUnauthorizedNoiseWebSocketError(err) {
		conn, initialized, err = client.open(ctx, options.ResumeSessionID, client.handleNotification)
	}
	if err != nil {
		identity.Destroy()
		return nil, err
	}
	client.conn = conn
	client.sessionID = initialized.SessionID
	go client.readLoop(conn)
	return client, nil
}

type noiseWebSocketConnectError struct {
	url        string
	statusCode int
	err        error
}

func (e *noiseWebSocketConnectError) Error() string {
	return fmt.Sprintf("failed to connect to exec-server websocket `%s`: %v", e.url, e.err)
}

func (e *noiseWebSocketConnectError) Unwrap() error {
	return e.err
}

func isUnauthorizedNoiseWebSocketError(err error) bool {
	var connectError *noiseWebSocketConnectError
	return errors.As(err, &connectError) && connectError.statusCode == http.StatusUnauthorized
}

type noiseHarnessClientConnection struct {
	conn            *websocket.Conn
	streamID        string
	transport       *clatter.TransportState
	transportMu     sync.RWMutex
	writeMu         sync.Mutex
	nextSeq         uint32
	reorder         remoteOrderedCiphertexts
	decoder         remoteJSONRPCDecoder
	pending         [][]byte
	keepaliveCancel context.CancelFunc
	closeOnce       sync.Once
	closeErr        error
}

func dialNoiseHarnessConnection(
	ctx context.Context,
	bundle *NoiseRendezvousConnectBundle,
	identity *remoteNoiseIdentity,
	httpClient *http.Client,
) (clientConnection, error) {
	if bundle == nil || identity == nil {
		return nil, errors.New("Noise rendezvous connection is incomplete")
	}
	remoteDH, remoteKEM, err := decodeRemoteNoisePublicKey(bundle.ExecutorPublicKey)
	if err != nil {
		return nil, err
	}
	diagnosticURL := bundle.WebSocketURL
	if index := strings.IndexAny(diagnosticURL, "?#"); index >= 0 {
		diagnosticURL = diagnosticURL[:index]
	}
	conn, response, err := websocket.Dial(ctx, bundle.WebSocketURL, &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		statusCode := 0
		if response != nil {
			statusCode = response.StatusCode
		}
		return nil, &noiseWebSocketConnectError{url: diagnosticURL, statusCode: statusCode, err: err}
	}
	conn.SetReadLimit(256 * 1024)
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.CloseNow()
		}
	}()
	streamID := uuid.NewString()
	handshake, err := clatter.NewHybridHandshake(
		clatter.PatternHybridIK,
		true,
		remoteNoiseCipherSuite(),
		clatter.WithStaticKey(identity.dh),
		clatter.WithStaticKEMKey(identity.kem),
		clatter.WithRemoteStatic(remoteDH),
		clatter.WithRemoteStaticKEMKey(remoteKEM),
		clatter.WithPrologue(noiseChannelPrologue(bundle.EnvironmentID, bundle.ExecutorRegistrationID, streamID)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start Noise relay handshake: %w", err)
	}
	handshakeActive := true
	defer func() {
		if handshakeActive {
			handshake.Destroy()
		}
	}()
	request := make([]byte, clatter.MaxMessageLen)
	requestLen, err := handshake.WriteMessage([]byte(bundle.HarnessKeyAuthorization), request)
	if err != nil {
		return nil, fmt.Errorf("failed to start Noise relay handshake: %w", err)
	}
	for _, frame := range []relayMessageFrame{
		newRelayResumeFrame(streamID),
		newRelayHandshakeFrame(streamID, request[:requestLen]),
	} {
		encoded, err := encodeRelayMessageFrame(frame)
		if err != nil {
			return nil, err
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			return nil, err
		}
	}
	var transport *clatter.TransportState
	for transport == nil {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("Noise relay websocket ended during handshake: %w", err)
		}
		if messageType != websocket.MessageBinary {
			return nil, errors.New("Noise relay transport expects binary protobuf frames")
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Noise relay frame: %w", err)
		}
		if frame.StreamID != streamID {
			continue
		}
		switch frame.Kind {
		case relayFrameHandshake:
			responsePayload := make([]byte, clatter.MaxMessageLen)
			responseLen, err := handshake.ReadMessage(frame.HandshakeBytes, responsePayload)
			if err != nil {
				return nil, fmt.Errorf("Noise relay handshake failed: %w", err)
			}
			if responseLen != 0 {
				return nil, errors.New("Noise handshake response payload must be empty")
			}
			transport, err = handshake.Finalize()
			handshakeActive = false
			if err != nil {
				return nil, fmt.Errorf("Noise relay handshake failed: %w", err)
			}
		case relayFrameReset:
			return nil, errors.New("Noise relay stream reset")
		case relayFrameAck, relayFrameResume, relayFrameHeartbeat:
		case relayFrameData:
			return nil, errors.New("Noise relay received data before handshake completion")
		default:
			return nil, errors.New("Noise relay received data before handshake completion")
		}
	}
	closeOnError = false
	return &noiseHarnessClientConnection{
		conn: conn, streamID: streamID, transport: transport,
		keepaliveCancel: startClientWebSocketKeepalive(conn),
	}, nil
}

func decodeRemoteNoisePublicKey(key RemotePublicKey) ([]byte, []byte, error) {
	if key.Suite != noiseChannelSuite {
		return nil, nil, fmt.Errorf("unsupported Noise channel suite %q", key.Suite)
	}
	dh, err := base64.StdEncoding.DecodeString(key.X25519PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid X25519 public key: %w", err)
	}
	kem, err := base64.StdEncoding.DecodeString(key.MLKEM768PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid ML-KEM-768 public key: %w", err)
	}
	suite := remoteNoiseCipherSuite()
	if len(dh) != suite.DH.PubKeyLen() {
		return nil, nil, fmt.Errorf("invalid X25519 public key length %d", len(dh))
	}
	if err := suite.SKEM.ValidatePublicKey(kem); err != nil {
		return nil, nil, fmt.Errorf("invalid ML-KEM-768 public key: %w", err)
	}
	return dh, kem, nil
}

func (c *noiseHarnessClientConnection) Write(ctx context.Context, message []byte) error {
	if len(message) == 0 || len(message) > maxNoiseJSONRPCMessageLen || !json.Valid(message) {
		return errors.New("Noise relay JSON-RPC message is invalid")
	}
	framed := make([]byte, 4, len(message)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(message)))
	framed = append(framed, message...)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for len(framed) != 0 {
		if c.nextSeq == ^uint32(0) {
			return errors.New("Noise relay sequence number exhausted")
		}
		chunkLen := min(len(framed), noiseRecordPlaintextLen)
		ciphertext := make([]byte, chunkLen+clatter.TagLen)
		c.transportMu.RLock()
		ciphertextLen, err := c.transport.Send(framed[:chunkLen], ciphertext)
		c.transportMu.RUnlock()
		if err != nil {
			return fmt.Errorf("failed to encrypt JSON-RPC payload for Noise relay: %w", err)
		}
		encoded, err := encodeRelayMessageFrame(newRelayDataFrame(c.streamID, c.nextSeq, ciphertext[:ciphertextLen]))
		if err != nil {
			return err
		}
		if err := c.conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			return err
		}
		c.nextSeq++
		framed = framed[chunkLen:]
	}
	return nil
}

func (c *noiseHarnessClientConnection) Read(ctx context.Context) ([]byte, error) {
	if len(c.pending) != 0 {
		message := c.pending[0]
		c.pending = c.pending[1:]
		return message, nil
	}
	for {
		messageType, payload, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != websocket.MessageBinary {
			return nil, errors.New("Noise relay transport expects binary protobuf frames")
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			return nil, err
		}
		if frame.StreamID != c.streamID {
			continue
		}
		switch frame.Kind {
		case relayFrameData:
			ciphertexts, err := c.reorder.Push(frame.Data.Seq, frame.Data.Payload)
			if err != nil {
				return nil, err
			}
			for _, ciphertext := range ciphertexts {
				if len(ciphertext) < clatter.TagLen {
					return nil, errors.New("Noise relay ciphertext is too short")
				}
				plaintext := make([]byte, len(ciphertext)-clatter.TagLen)
				c.transportMu.RLock()
				plaintextLen, err := c.transport.Receive(ciphertext, plaintext)
				c.transportMu.RUnlock()
				if err != nil {
					return nil, fmt.Errorf("Noise relay decryption failed: %w", err)
				}
				messages, err := c.decoder.Push(plaintext[:plaintextLen])
				if err != nil {
					return nil, err
				}
				c.pending = append(c.pending, messages...)
			}
			if len(c.pending) != 0 {
				message := c.pending[0]
				c.pending = c.pending[1:]
				return message, nil
			}
		case relayFrameReset:
			return nil, errors.New("Noise relay stream reset")
		case relayFrameAck, relayFrameResume, relayFrameHeartbeat:
		case relayFrameHandshake:
			return nil, errors.New("Noise relay received invalid post-handshake frame")
		default:
			return nil, errors.New("Noise relay received invalid post-handshake frame")
		}
	}
}

func (c *noiseHarnessClientConnection) Close() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		c.transportMu.Lock()
		c.transport.Destroy()
		c.transportMu.Unlock()
	})
	return c.closeErr
}

func (c *noiseHarnessClientConnection) CloseNow() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.CloseNow()
		c.transportMu.Lock()
		c.transport.Destroy()
		c.transportMu.Unlock()
	})
	return c.closeErr
}

var _ clientConnection = (*noiseHarnessClientConnection)(nil)
