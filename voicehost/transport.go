package voicehost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
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

// Ready reports whether the ordered event channel is open. A transport that
// never opened, or whose channel closed after opening, reports false.
func (t *Transport) Ready() bool {
	if t == nil {
		return false
	}
	return t.opened.Load() && !t.closed.Load()
}

// Transport owns one WebRTC peer and its locally created ordered event channel.
// It mirrors the Rust voice helper transport: candidate admission is bounded
// before the answer can mutate the peer, remotely opened channels are rejected,
// and readiness means only that the oai-events channel opened.
type Transport struct {
	peer      *webrtc.PeerConnection
	channel   *webrtc.DataChannel
	ready     chan struct{}
	opened    atomic.Bool
	closed    atomic.Bool
	readyOnce sync.Once
	failed    chan error
	closeOnce sync.Once
	sender    *rtpAudioSender
}

var _ VoiceTransport = (*Transport)(nil)

// RTPPacketSink receives one inbound Opus payload with its source identity and
// arrival time.
type RTPPacketSink func(payload []byte, ssrc uint32, at time.Time)

// rtpAudioSender adapts the local Opus track to the helper's media contract. A
// nil payload writes a header-only packet that advances the peer's clock.
type rtpAudioSender struct {
	track *webrtc.TrackLocalStaticSample
}

// SendFrame writes one encoded frame. The track owns RTP packetization, so the
// header only documents the contract the helper is responsible for.
func (s *rtpAudioSender) SendFrame(payload []byte, header voiceRTPHeader, at time.Time) error {
	if s == nil || s.track == nil {
		return errors.New("voice audio track is unavailable")
	}
	return s.track.WriteSample(media.Sample{Data: payload, Duration: voiceFrameDuration})
}

// EnableAudio attaches the local Opus track and the inbound RTP sink. It must
// be called before Offer so the track appears in the SDP.
func (t *Transport) EnableAudio(sink RTPPacketSink) error {
	if t == nil || t.peer == nil {
		return ErrVoiceTransportClosed
	}
	if t.sender != nil {
		return errors.New("voice audio is already enabled")
	}
	codec := webrtc.RTPCodecCapability{
		MimeType:     webrtc.MimeTypeOpus,
		ClockRate:    opusClockRate,
		Channels:     2,
		SDPFmtpLine:  "minptime=10;useinbandfec=1",
		RTCPFeedback: nil,
	}
	track, err := webrtc.NewTrackLocalStaticSample(codec, "microphone", "realtime")
	if err != nil {
		return fmt.Errorf("create voice audio track: %w", err)
	}
	sender, err := t.peer.AddTrack(track)
	if err != nil {
		return fmt.Errorf("attach voice audio track: %w", err)
	}
	t.sender = &rtpAudioSender{track: track}
	// Drain RTCP so the sender's interceptors keep running for the session's
	// lifetime.
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, readErr := sender.Read(buffer); readErr != nil {
				return
			}
		}
	}()
	if sink != nil {
		t.peer.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			for {
				packet, _, readErr := remote.ReadRTP()
				if readErr != nil {
					return
				}
				if packet == nil || uint8(packet.PayloadType) != opusPayloadType {
					continue
				}
				sink(append([]byte(nil), packet.Payload...), uint32(packet.SSRC), time.Now())
			}
		})
	}
	return nil
}

// AudioSender returns the enabled outbound track, if any.
func (t *Transport) AudioSender() voiceSender {
	if t == nil || t.sender == nil {
		return nil
	}
	return t.sender
}

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
		transport.opened.Store(true)
		transport.readyOnce.Do(func() { close(transport.ready) })
	})
	channel.OnClose(func() {
		transport.closed.Store(true)
		transport.signalFailure(ErrVoiceTransportClosed)
	})
	channel.OnError(func(err error) {
		transport.closed.Store(true)
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
		t.closed.Store(true)
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
