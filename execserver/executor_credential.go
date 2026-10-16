package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// Rust #54434 (`16e578dd1d`, `cli/src/exec_server_credential.rs`) lets a trusted
// parent hand a scoped registry credential to the executor over stdin: the
// parent writes newline-delimited JSON frames with `version: 1`,
// `environment_id`, `token` and `expires_at` (Unix seconds). A frame including
// its newline must be at most 16 KiB, a valid first frame is required within 30
// seconds, and every valid frame replaces the current credential. A frame for
// another environment (or any other invalid input) fails the pipe, as does EOF.
// Only registry requests carry the credential, as an `Authorization` bearer.
const (
	// ExecutorCredentialMaxFrameBytes is Rust's `MAX_FRAME_BYTES`.
	ExecutorCredentialMaxFrameBytes = 16 * 1024
	// executorCredentialInitialTimeout is Rust's `INITIAL_CREDENTIAL_TIMEOUT`.
	executorCredentialInitialTimeout = 30 * time.Second
)

// Credential-pipe failures, with Rust's exact `CredentialError` display text.
var (
	ErrCredentialInvalidFrame     = errors.New("invalid exec-server executor credential frame")
	ErrCredentialFrameTooLarge    = errors.New("exec-server executor credential frame exceeds the size limit")
	ErrCredentialWrongEnvironment = errors.New("exec-server executor credential is for a different environment")
	ErrCredentialExpired          = errors.New("exec-server executor credential is expired")
	ErrCredentialMissing          = errors.New("exec-server executor credential is unavailable")
	ErrCredentialClosed           = errors.New("exec-server executor credential pipe closed")
	ErrCredentialRead             = errors.New("could not read exec-server executor credential pipe")
	ErrCredentialClock            = errors.New("could not check exec-server executor credential expiration")
	ErrCredentialInitialTimeout   = errors.New("timed out waiting for the initial exec-server executor credential")
)

type credentialStateKind int

const (
	credentialStatePending credentialStateKind = iota
	credentialStateActive
	credentialStateFailed
	credentialStateClosed
)

// credentialState mirrors Rust's `CredentialState`.
type credentialState struct {
	kind      credentialStateKind
	token     string
	expiresAt uint64
	failure   error
}

// ExecutorCredentialPipe mirrors Rust's `ExecutorCredentialPipe` plus
// `ExecutorCredentialProvider`: it reads rotating credentials from a parent
// stream and exposes them as the registry auth provider.
type ExecutorCredentialPipe struct {
	environmentID string

	mu      sync.Mutex
	state   credentialState
	updated chan struct{}
	closed  chan struct{}
}

// NewExecutorCredentialPipe starts reading credential frames from reader. A nil
// reader is treated as an already-closed pipe, exactly like Rust's stdin at EOF.
func NewExecutorCredentialPipe(environmentID string, reader io.Reader) *ExecutorCredentialPipe {
	pipe := &ExecutorCredentialPipe{
		environmentID: environmentID,
		state:         credentialState{kind: credentialStatePending},
		updated:       make(chan struct{}),
		closed:        make(chan struct{}),
	}
	go pipe.read(reader)
	return pipe
}

// ParentClosed is closed once the parent stream ended: Rust's
// `ParentLifetime::MonitoredStdin(parent_closed)`. EOF of the credential pipe
// means the parent went away, so the executor stops with it.
func (p *ExecutorCredentialPipe) ParentClosed() <-chan struct{} {
	if p == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return p.closed
}

// ResolveAuthHeaders is the Go counterpart of Rust's `AuthProvider for
// ExecutorCredentialProvider`: it returns the current credential as a single
// `Authorization: Bearer <token>` header, and fails when the credential is
// missing, expired, or the pipe failed.
func (p *ExecutorCredentialPipe) ResolveAuthHeaders(context.Context) (http.Header, error) {
	token, err := p.currentToken()
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	return headers, nil
}

// WaitForInitial mirrors Rust's `ExecutorCredentialProvider::wait_for_initial`:
// the executor must receive a usable credential within 30 seconds.
func (p *ExecutorCredentialPipe) WaitForInitial(ctx context.Context) error {
	return p.waitForInitialWithin(ctx, executorCredentialInitialTimeout)
}

func (p *ExecutorCredentialPipe) waitForInitialWithin(ctx context.Context, timeout time.Duration) error {
	if p == nil {
		return ErrCredentialClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		p.mu.Lock()
		state := p.state
		updated := p.updated
		p.mu.Unlock()
		if state.kind != credentialStatePending {
			_, err := state.tokenValue()
			return err
		}
		select {
		case <-updated:
		case <-deadline.C:
			return ErrCredentialInitialTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CheckPipeFailure mirrors Rust's `ExecutorCredentialProvider::check_pipe_failure`:
// a pipe that ended in a failure must surface that failure after the run.
func (p *ExecutorCredentialPipe) CheckPipeFailure() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.kind == credentialStateFailed {
		return p.state.failure
	}
	return nil
}

func (p *ExecutorCredentialPipe) currentToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state.tokenValue()
}

// tokenValue mirrors Rust's `CredentialState::header`, including its
// expiry check at the moment of use: a parent that misses a renewal makes the
// next registry request fail rather than send a stale credential.
func (s credentialState) tokenValue() (string, error) {
	switch s.kind {
	case credentialStateActive:
		now, err := unixTimestamp()
		if err != nil {
			return "", err
		}
		if s.expiresAt > now {
			return s.token, nil
		}
		return "", ErrCredentialExpired
	case credentialStateFailed:
		return "", s.failure
	case credentialStateClosed:
		return "", ErrCredentialClosed
	default:
		return "", ErrCredentialMissing
	}
}

func (p *ExecutorCredentialPipe) setState(state credentialState) {
	p.mu.Lock()
	p.state = state
	updated := p.updated
	p.updated = make(chan struct{})
	p.mu.Unlock()
	close(updated)
}

// read mirrors Rust's `read_frames`: bytes accumulate until a newline, the frame
// bound counts the newline, and only a complete line is parsed.
func (p *ExecutorCredentialPipe) read(reader io.Reader) {
	defer close(p.closed)
	result := p.readFrames(reader)
	if result != nil {
		p.setState(credentialState{kind: credentialStateFailed, failure: result})
		return
	}
	p.setState(credentialState{kind: credentialStateClosed})
}

func (p *ExecutorCredentialPipe) readFrames(reader io.Reader) error {
	if reader == nil {
		return nil
	}
	var frame []byte
	buffer := make([]byte, 4096)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			chunk := buffer[:count]
			for len(chunk) > 0 {
				consumed := len(chunk)
				if index := bytes.IndexByte(chunk, '\n'); index >= 0 {
					consumed = index + 1
				}
				if len(frame)+consumed > ExecutorCredentialMaxFrameBytes {
					return ErrCredentialFrameTooLarge
				}
				frame = append(frame, chunk[:consumed]...)
				chunk = chunk[consumed:]
				if frame[len(frame)-1] == '\n' {
					state, parseErr := parseCredentialFrame(frame, p.environmentID)
					if parseErr != nil {
						return parseErr
					}
					p.setState(state)
					frame = frame[:0]
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(frame) == 0 {
					return nil
				}
				return ErrCredentialInvalidFrame
			}
			return ErrCredentialRead
		}
	}
}

// credentialFrame mirrors Rust's `CredentialFrame` (`deny_unknown_fields`).
type credentialFrame struct {
	Version *uint8 `json:"version"`
	// EnvironmentID is a pointer because Rust's `CredentialFrame` declares it
	// as a required `String`: a frame without it is a serde error, so it must be
	// `InvalidFrame` and not `WrongEnvironment`.
	EnvironmentID *string `json:"environment_id"`
	Token         string  `json:"token"`
	ExpiresAt     *uint64 `json:"expires_at"`
}

// parseCredentialFrame mirrors Rust's `parse_frame`, including the ordering of
// its checks: frame shape, then the environment binding, then expiry.
func parseCredentialFrame(frame []byte, environmentID string) (credentialState, error) {
	var decoded credentialFrame
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return credentialState{}, ErrCredentialInvalidFrame
	}
	if decoded.Version == nil || decoded.EnvironmentID == nil || decoded.ExpiresAt == nil ||
		*decoded.Version != 1 || decoded.Token == "" || !isASCIIGraphicToken(decoded.Token) {
		return credentialState{}, ErrCredentialInvalidFrame
	}
	if environmentID == "" || *decoded.EnvironmentID != environmentID {
		return credentialState{}, ErrCredentialWrongEnvironment
	}
	now, err := unixTimestamp()
	if err != nil {
		return credentialState{}, err
	}
	if *decoded.ExpiresAt <= now {
		return credentialState{}, ErrCredentialExpired
	}
	return credentialState{kind: credentialStateActive, token: decoded.Token, expiresAt: *decoded.ExpiresAt}, nil
}

func unixTimestamp() (uint64, error) {
	seconds := time.Now().Unix()
	if seconds < 0 {
		return 0, ErrCredentialClock
	}
	return uint64(seconds), nil
}
