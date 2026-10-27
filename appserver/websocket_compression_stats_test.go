package appserver

import (
	"strings"
	"testing"
)

// Mirrors Rust #57220's paired-codec statistics assertions: transmit and
// receive totals share the connection and agree after a successful round trip.
func TestWebsocketZstdCodecPairSharesCompressionStatsLikeRust(t *testing.T) {
	text := strings.Repeat("individual message body ", 8)
	encoder, decoder, err := NewWebsocketZstdCodecPair(
		MaxWebsocketDecompressedMessageSize,
		WebsocketCompressionConnection{ID: 42, Kind: WebsocketCompressionConnectionListener},
	)
	if err != nil {
		t.Fatalf("NewWebsocketZstdCodecPair() error = %v", err)
	}
	defer encoder.Close()
	defer decoder.Close()

	if encoder.stats == nil || decoder.stats == nil {
		t.Fatal("paired codecs did not record statistics")
	}
	if got := encoder.stats.savedPercent(); got != nil {
		t.Fatalf("savedPercent() before traffic = %v, want nil", *got)
	}
	first, err := encoder.Encode(text)
	if err != nil {
		t.Fatalf("first Encode() error = %v", err)
	}
	second, err := encoder.Encode(text)
	if err != nil {
		t.Fatalf("second Encode() error = %v", err)
	}
	for index, payload := range [][]byte{first, second} {
		decoded, err := decoder.Decode(payload)
		if err != nil {
			t.Fatalf("Decode(%d) error = %v", index, err)
		}
		if decoded != text {
			t.Fatalf("Decode(%d) = %q, want %q", index, decoded, text)
		}
	}

	tx := encoder.stats
	rx := decoder.stats
	if tx.connectionID != 42 || rx.connectionID != 42 {
		t.Fatalf("connection ids = (%d, %d), want (42, 42)", tx.connectionID, rx.connectionID)
	}
	if tx.connectionKind != WebsocketCompressionConnectionListener || rx.connectionKind != WebsocketCompressionConnectionListener {
		t.Fatalf("connection kinds = (%q, %q)", tx.connectionKind, rx.connectionKind)
	}
	wantUncompressed := uint64(len(text) * 2)
	wantCompressed := uint64(len(first) + len(second))
	if tx.uncompressedBytes != wantUncompressed || tx.compressedBytes != wantCompressed || tx.messages != 2 {
		t.Fatalf("tx stats = (%d, %d, %d), want (%d, %d, 2)",
			tx.uncompressedBytes, tx.compressedBytes, tx.messages, wantUncompressed, wantCompressed)
	}
	if rx.uncompressedBytes != tx.uncompressedBytes || rx.compressedBytes != tx.compressedBytes || rx.messages != tx.messages {
		t.Fatalf("rx stats = (%d, %d, %d), tx stats = (%d, %d, %d)",
			rx.uncompressedBytes, rx.compressedBytes, rx.messages,
			tx.uncompressedBytes, tx.compressedBytes, tx.messages)
	}
	wantSaved := 100.0 * (1.0 - float64(wantCompressed)/float64(wantUncompressed))
	saved := tx.savedPercent()
	if saved == nil || *saved != wantSaved {
		t.Fatalf("savedPercent() = %v, want %v", saved, wantSaved)
	}
	if len(second) >= len(first) {
		t.Fatalf("retained stream did not reuse history: first=%d second=%d", len(first), len(second))
	}
}

// Mirrors the Rust oversize case: a rejected receive contributes nothing.
func TestWebsocketZstdCodecPairExcludesRejectedDecodeLikeRust(t *testing.T) {
	text := strings.Repeat("abcdefgh", 16)
	encoder, decoder, err := NewWebsocketZstdCodecPair(
		64,
		WebsocketCompressionConnection{ID: NextNativeWebsocketCompressionConnectionID(), Kind: WebsocketCompressionConnectionNativeClient},
	)
	if err != nil {
		t.Fatalf("NewWebsocketZstdCodecPair() error = %v", err)
	}
	defer encoder.Close()
	defer decoder.Close()
	payload, err := encoder.Encode(text)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if _, err := decoder.Decode(payload); err == nil {
		t.Fatal("oversized decompressed message succeeded")
	}
	if got := decoder.stats; got.uncompressedBytes != 0 || got.compressedBytes != 0 || got.messages != 0 {
		t.Fatalf("rejected decode stats = (%d, %d, %d), want zeros",
			got.uncompressedBytes, got.compressedBytes, got.messages)
	}
}

func TestNextNativeWebsocketCompressionConnectionIDIsUnique(t *testing.T) {
	first := NextNativeWebsocketCompressionConnectionID()
	second := NextNativeWebsocketCompressionConnectionID()
	if first == 0 || second == 0 || first == second {
		t.Fatalf("native connection ids = (%d, %d), want distinct non-zero ids", first, second)
	}
}
