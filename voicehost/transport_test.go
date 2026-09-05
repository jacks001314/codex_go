package voicehost

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAnswerCandidates(t *testing.T) {
	header := "v=0\r\no=- 1 1 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n"
	media := "m=application 0 UDP/DTLS/SCTP webrtc-datachannel\r\n"
	validCandidate := "a=candidate:0 1 udp 1 192.0.2.1 9 typ host\r\n"

	candidates, err := validateAnswerCandidates(header + media + validCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Candidate != "candidate:0 1 udp 1 192.0.2.1 9 typ host" {
		t.Fatalf("candidates = %#v", candidates)
	}

	componentTwo := "a=candidate:0 2 udp 1 192.0.2.1 9 typ host\r\n"
	candidates, err = validateAnswerCandidates(header + media + componentTwo)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("component two candidates = %#v, %v", candidates, err)
	}

	tooMany := header + media + strings.Repeat(validCandidate, maxRemoteCandidates+1)
	if _, err := validateAnswerCandidates(tooMany); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("excess candidates = %v", err)
	}

	invalid := header + media + "a=candidate:not-an-ice-candidate\r\n"
	if _, err := validateAnswerCandidates(invalid); !errors.Is(err, ErrInvalidVoiceCandidate) {
		t.Fatalf("invalid candidate = %v", err)
	}
	if _, err := validateAnswerCandidates("not valid sdp"); !errors.Is(err, ErrInvalidVoiceAnswer) {
		t.Fatalf("invalid answer = %v", err)
	}
}

func TestTransportCanCloseWithoutNegotiation(t *testing.T) {
	transport, err := NewTransport()
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
