package voicehost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	transportWait       = 15 * time.Second
	maxRemoteCandidates = 32
	iceProbeInterval    = 200 * time.Millisecond
)

var (
	// ErrTooManyCandidates indicates the remote answer exceeded the candidate
	// admission budget.
	ErrTooManyCandidates = errors.New("too many voice candidates")
	// ErrInvalidVoiceAnswer indicates malformed or non-answer SDP.
	ErrInvalidVoiceAnswer = errors.New("invalid voice answer")
	// ErrInvalidVoiceCandidate indicates a candidate line failed ICE parsing.
	ErrInvalidVoiceCandidate = errors.New("invalid voice candidate")
	// ErrVoiceTransportClosed indicates the ordered event channel closed before
	// the transport became ready.
	ErrVoiceTransportClosed = errors.New("voice event channel closed")
	// ErrVoiceTransportTimeout indicates negotiation exceeded its deadline.
	ErrVoiceTransportTimeout = errors.New("timed out connecting voice peer")
)

// VoiceTransport is the WebRTC negotiation surface used by the host control
// loop. It is separate from the concrete Transport so the loop can be tested
// without binding real sockets.
type VoiceTransport interface {
	Offer(ctx context.Context) (string, error)
	ApplyAnswer(ctx context.Context, sdp string) error
	Close() error
}

// Transport owns one WebRTC peer and its locally created ordered event channel.
// It mirrors the Rust voice helper transport: candidate admission is bounded
// before the answer can mutate the peer, remotely opened channels are rejected,
// and readiness means only that the oai-events channel opened.
type Transport struct {
	peer      *webrtc.PeerConnection
	channel   *webrtc.DataChannel
	ready     chan struct{}
	readyOnce sync.Once
	failed    chan error
	closeOnce sync.Once
}

var _ VoiceTransport = (*Transport)(nil)

// NewTransport creates a WebRTC peer with the ordered oai-events data channel.
// It does not start a session or establish connectivity.
func NewTransport() (*Transport, error) {
	settings := webrtc.SettingEngine{}
	settings.SetNetworkTypes([]webrtc.NetworkType{
		webrtc.NetworkTypeUDP4,
		webrtc.NetworkTypeUDP6,
		webrtc.NetworkTypeTCP4,
		webrtc.NetworkTypeTCP6,
	})
	// Rust probes throughout the negotiation deadline with a 200ms check
	// interval. Set a generous binding budget and keep-alive cadence.
	settings.SetICEMaxBindingRequests(75)
	settings.SetICETimeouts(5*time.Second, 10*time.Second, iceProbeInterval)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("create voice peer: %w", err)
	}
	channel, err := peer.CreateDataChannel("oai-events", nil)
	if err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("create voice event channel: %w", err)
	}
	transport := &Transport{
		peer:    peer,
		channel: channel,
		ready:   make(chan struct{}),
		failed:  make(chan error, 1),
	}
	channel.OnOpen(func() {
		transport.readyOnce.Do(func() { close(transport.ready) })
	})
	channel.OnClose(func() {
		transport.signalFailure(ErrVoiceTransportClosed)
	})
	channel.OnError(func(err error) {
		transport.signalFailure(fmt.Errorf("voice event channel: %w", err))
	})
	peer.OnDataChannel(func(remote *webrtc.DataChannel) {
		// Only the locally created channel is used; reject remote channels.
		go func() { _ = remote.Close() }()
	})
	return transport, nil
}

// Offer creates and sets a local offer, waits for ICE gathering, and returns
// the gathered SDP.
func (t *Transport) Offer(ctx context.Context) (string, error) {
	if t == nil {
		return "", ErrVoiceTransportClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	offer, err := t.peer.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create voice offer: %w", err)
	}
	if err := t.peer.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set voice offer: %w", err)
	}
	select {
	case <-webrtc.GatheringCompletePromise(t.peer):
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(transportWait):
		return "", ErrVoiceTransportTimeout
	}
	description := t.peer.LocalDescription()
	if description == nil {
		return "", errors.New("missing voice offer")
	}
	return description.SDP, nil
}

// ApplyAnswer validates the answer, installs it, and returns only when the
// ordered event channel opens. Candidate admission happens before the peer is
// mutated.
func (t *Transport) ApplyAnswer(ctx context.Context, sdpValue string) error {
	if t == nil {
		return ErrVoiceTransportClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	candidates, err := validateAnswerCandidates(sdpValue)
	if err != nil {
		return err
	}
	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdpValue}
	if err := t.peer.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidVoiceAnswer, err)
	}
	for _, candidate := range candidates {
		if err := t.peer.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidVoiceCandidate, err)
		}
	}
	select {
	case <-t.ready:
		return nil
	case err := <-t.failed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(transportWait):
		return ErrVoiceTransportTimeout
	}
}

// Close tears down the peer and its channels.
func (t *Transport) Close() error {
	if t == nil {
		return nil
	}
	var err error
	t.closeOnce.Do(func() {
		err = t.peer.Close()
	})
	return err
}

func (t *Transport) signalFailure(err error) {
	if t == nil || err == nil {
		return
	}
	select {
	case t.failed <- err:
	default:
	}
}

func validateAnswerCandidates(sdpValue string) ([]webrtc.ICECandidateInit, error) {
	var parsed sdp.SessionDescription
	if err := parsed.UnmarshalString(sdpValue); err != nil {
		return nil, ErrInvalidVoiceAnswer
	}
	seen := make(map[string]struct{})
	candidates := make([]webrtc.ICECandidateInit, 0, maxRemoteCandidates)
	total := 0
	for _, media := range parsed.MediaDescriptions {
		for _, attribute := range media.Attributes {
			if !attribute.IsICECandidate() {
				continue
			}
			total++
			if total > maxRemoteCandidates {
				return nil, ErrTooManyCandidates
			}
			if attribute.Value == "" {
				return nil, ErrInvalidVoiceCandidate
			}
			parsedCandidate, err := ice.UnmarshalCandidate(attribute.Value)
			if err != nil {
				return nil, ErrInvalidVoiceCandidate
			}
			if parsedCandidate.Component() != 1 {
				continue
			}
			if _, ok := seen[attribute.Value]; ok {
				continue
			}
			seen[attribute.Value] = struct{}{}
			candidates = append(candidates, webrtc.ICECandidateInit{
				Candidate: "candidate:" + attribute.Value,
			})
		}
	}
	return candidates, nil
}
