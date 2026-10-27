package appserver

import (
	"log/slog"
	"sync/atomic"
	"time"
)

const websocketCompressionStatsInterval = 60 * time.Second

// WebsocketCompressionConnectionKind identifies who owns a compressed
// WebSocket connection in the payload statistics log.
type WebsocketCompressionConnectionKind string

const (
	WebsocketCompressionConnectionListener     WebsocketCompressionConnectionKind = "listener"
	WebsocketCompressionConnectionNativeClient WebsocketCompressionConnectionKind = "native_client"
)

// WebsocketCompressionConnection ties transmit and receive statistics to the
// same connection, mirroring Rust's CompressionConnection.
type WebsocketCompressionConnection struct {
	ID   uint64
	Kind WebsocketCompressionConnectionKind
}

var nativeWebsocketCompressionConnectionCounter uint64

// NextNativeWebsocketCompressionConnectionID reserves the connection ID the
// remote app-server client reports alongside listener connections.
func NextNativeWebsocketCompressionConnectionID() uint64 {
	return atomic.AddUint64(&nativeWebsocketCompressionConnectionCounter, 1)
}

// NewWebsocketZstdCodecPair creates the encoder and decoder for one compressed
// connection. Both directions share the connection ID and kind in their final
// and periodic payload-statistics records.
func NewWebsocketZstdCodecPair(maxMessageSize int, connection WebsocketCompressionConnection) (*ZstdEncoder, *ZstdDecoder, error) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		return nil, nil, err
	}
	decoder, err := NewZstdDecoder(maxMessageSize)
	if err != nil {
		_ = encoder.Close()
		return nil, nil, err
	}
	encoder.stats = newWebsocketCompressionStats(connection, "tx")
	decoder.stats = newWebsocketCompressionStats(connection, "rx")
	return encoder, decoder, nil
}

type websocketCompressionStats struct {
	connectionID      uint64
	connectionKind    WebsocketCompressionConnectionKind
	direction         string
	uncompressedBytes uint64
	compressedBytes   uint64
	messages          uint64
	lastReport        time.Time
}

func newWebsocketCompressionStats(connection WebsocketCompressionConnection, direction string) *websocketCompressionStats {
	return &websocketCompressionStats{
		connectionID:   connection.ID,
		connectionKind: connection.Kind,
		direction:      direction,
		lastReport:     time.Now(),
	}
}

func (s *websocketCompressionStats) record(uncompressedBytes int, compressedBytes int) {
	if s == nil {
		return
	}
	if uncompressedBytes > 0 {
		s.uncompressedBytes += uint64(uncompressedBytes)
	}
	if compressedBytes > 0 {
		s.compressedBytes += uint64(compressedBytes)
	}
	s.messages++
	if time.Since(s.lastReport) >= websocketCompressionStatsInterval {
		s.report("periodic")
		s.lastReport = time.Now()
	}
}

func (s *websocketCompressionStats) savedPercent() *float64 {
	if s == nil || s.uncompressedBytes == 0 {
		return nil
	}
	saved := 100.0 * (1.0 - float64(s.compressedBytes)/float64(s.uncompressedBytes))
	return &saved
}

func (s *websocketCompressionStats) report(reason string) {
	if s == nil {
		return
	}
	var savedPercent any
	if percent := s.savedPercent(); percent != nil {
		savedPercent = *percent
	}
	slog.Info("app-server WebSocket compression payload totals",
		"connection_id", s.connectionID,
		"connection_kind", string(s.connectionKind),
		"direction", s.direction,
		"report", reason,
		"uncompressed_bytes", s.uncompressedBytes,
		"compressed_bytes", s.compressedBytes,
		"messages", s.messages,
		"saved_percent", savedPercent,
	)
}
