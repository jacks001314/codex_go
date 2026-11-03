package model

import (
	"context"
	"testing"
	"time"

	"codex_go/auth"
)

func testAuthSnapshotWithToken(token string) *auth.AuthDotJSON {
	return &auth.AuthDotJSON{Tokens: map[string]any{"access_token": token}}
}

func TestWebsocketReauthTracksLastConnectedCredential(t *testing.T) {
	session := &responsesWebsocketSession{}
	first := testAuthSnapshotWithToken("token-a")
	if session.websocketReauth(first) {
		t.Fatal("first handshake reported reauth")
	}
	session.markConnectedAuth(first)
	if session.websocketReauth(first) {
		t.Fatal("same credential reported reauth")
	}
	if !session.websocketReauth(testAuthSnapshotWithToken("token-b")) {
		t.Fatal("changed credential did not report reauth")
	}
	session.markConnectedAuth(&auth.AuthDotJSON{})
	if session.websocketReauth(&auth.AuthDotJSON{}) {
		t.Fatal("two unmanaged-auth handshakes reported reauth")
	}
	if !session.websocketReauth(first) {
		t.Fatal("managed auth after unmanaged auth did not report reauth")
	}
}

func TestRecordWebsocketConnectEmitsMetricTags(t *testing.T) {
	metrics := &recordingMetricsSink{}
	telemetry := &recordingTelemetrySink{}
	runner := &ResponsesAgentRunner{Telemetry: telemetry, Metrics: metrics}
	runner.recordWebsocketConnectRecord(context.Background(), nil, nil, nil, nil, nil, 25*time.Millisecond, "", "", true)
	if len(telemetry.websocketConnects) != 1 || !telemetry.websocketConnects[0].Reauth {
		t.Fatalf("connect records = %#v", telemetry.websocketConnects)
	}
	foundCounter, foundDuration := false, false
	for _, sample := range metrics.counters {
		if sample.name == websocketConnectCountMetric {
			foundCounter = true
			if sample.tags["success"] != "true" || sample.tags["reauth"] != "true" {
				t.Fatalf("connect counter tags = %#v", sample.tags)
			}
		}
	}
	for _, sample := range metrics.durations {
		if sample.name == websocketConnectDurationMetric {
			foundDuration = true
			if sample.tags["success"] != "true" || sample.tags["reauth"] != "true" {
				t.Fatalf("connect duration tags = %#v", sample.tags)
			}
		}
	}
	if !foundCounter || !foundDuration {
		t.Fatalf("connect metrics missing: counters=%#v durations=%#v", metrics.counters, metrics.durations)
	}
}
