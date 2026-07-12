package execserver

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	clatter "github.com/shurlinet/go-clatter"
)

const (
	remoteRelayChannelCapacity             = 128
	maxActiveNoiseRelayStreams             = 128
	maxFailedNoiseHandshakes               = 8
	maxHarnessKeyAuthorizationBytes        = 4096
	maxPendingHandshakeValidations         = 32
	harnessKeyValidationTimeout            = 10 * time.Second
	remoteRelayKeepaliveInterval           = 30 * time.Second
	noiseRecordPlaintextLen                = 60 * 1024
	maxNoiseJSONRPCMessageLen              = 64 * 1024 * 1024
	maxNoiseRelayReorderDistance    uint32 = 64
	maxNoiseRelayPendingBytes              = 1024 * 1024
)

type remoteHarnessValidationRequest struct {
	ExecutorRegistrationID  string          `json:"executor_registration_id"`
	HarnessPublicKey        RemotePublicKey `json:"harness_public_key"`
	HarnessKeyAuthorization string          `json:"harness_key_authorization"`
}

type remoteHarnessValidationResponse struct {
	Valid bool `json:"valid"`
}

type remoteRelayIncoming struct {
	frame relayMessageFrame
	err   error
}

type remoteRelayValidationResult struct {
	streamID     string
	validationID uint64
	err          error
}

type pendingRemoteRelayHandshake struct {
	validationID uint64
	handshake    *pendingRemoteNoiseHandshake
}

type remoteRelayClosedStream struct {
	streamID   string
	instanceID uint64
}

func (s *Server) serveNoiseRelayConnection(
	ctx context.Context,
	conn *websocket.Conn,
	cfg RemoteEnvironmentConfig,
	registration *remoteRegistrationResponse,
	identity *remoteNoiseIdentity,
) error {
	if conn == nil || registration == nil || identity == nil {
		return errors.New("Noise relay connection is incomplete")
	}
	conn.SetReadLimit(256 * 1024)
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.CloseNow()

	outgoing := make(chan []byte, remoteRelayChannelCapacity)
	incoming := make(chan remoteRelayIncoming, remoteRelayChannelCapacity)
	writerDone := make(chan error, 1)
	validationDone := make(chan remoteRelayValidationResult, maxPendingHandshakeValidations)
	closedStreams := make(chan remoteRelayClosedStream, maxActiveNoiseRelayStreams)

	go runRemoteRelayWriter(connectionCtx, conn, outgoing, writerDone)
	go runRemoteRelayReader(connectionCtx, conn, incoming)

	streams := map[string]*remoteNoiseVirtualStream{}
	pending := map[string]pendingRemoteRelayHandshake{}
	defer func() {
		for _, stream := range streams {
			stream.Close()
		}
		for _, item := range pending {
			item.handshake.Destroy()
		}
	}()

	failedHandshakes := 0
	var nextValidationID uint64
	for {
		select {
		case <-connectionCtx.Done():
			return nil
		case err := <-writerDone:
			if err == nil || ctx.Err() != nil {
				return nil
			}
			return err
		case closed := <-closedStreams:
			if stream := streams[closed.streamID]; stream != nil && stream.instanceID == closed.instanceID {
				delete(streams, closed.streamID)
				stream.Close()
			}
		case result := <-validationDone:
			item, exists := pending[result.streamID]
			if !exists || item.validationID != result.validationID {
				continue
			}
			delete(pending, result.streamID)
			if result.err != nil {
				item.handshake.Destroy()
				queueRelayReset(outgoing, result.streamID)
				failedHandshakes++
				if failedHandshakes >= maxFailedNoiseHandshakes {
					return errors.New("closing Noise relay after repeated handshake failures")
				}
				continue
			}
			if len(streams) >= maxActiveNoiseRelayStreams {
				item.handshake.Destroy()
				queueRelayReset(outgoing, result.streamID)
				continue
			}
			transport, response, err := item.handshake.Complete()
			if err != nil {
				queueRelayReset(outgoing, result.streamID)
				failedHandshakes++
				if failedHandshakes >= maxFailedNoiseHandshakes {
					return errors.New("closing Noise relay after repeated handshake failures")
				}
				continue
			}
			if err := queueRelayFrame(outgoing, newRelayHandshakeFrame(result.streamID, response)); err != nil {
				transport.Destroy()
				return err
			}
			streams[result.streamID] = newRemoteNoiseVirtualStream(
				connectionCtx,
				s,
				result.streamID,
				result.validationID,
				transport,
				outgoing,
				closedStreams,
			)
		case event := <-incoming:
			if event.err != nil {
				if ctx.Err() != nil || websocket.CloseStatus(event.err) != -1 {
					return nil
				}
				return event.err
			}
			frame := event.frame
			switch frame.Kind {
			case relayFrameHandshake:
				if _, exists := streams[frame.StreamID]; exists {
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				if previous, exists := pending[frame.StreamID]; exists {
					delete(pending, frame.StreamID)
					previous.handshake.Destroy()
					queueRelayReset(outgoing, frame.StreamID)
					failedHandshakes++
					if failedHandshakes >= maxFailedNoiseHandshakes {
						return errors.New("closing Noise relay after repeated handshake failures")
					}
					continue
				}
				if len(streams) >= maxActiveNoiseRelayStreams || len(pending) >= maxPendingHandshakeValidations {
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				prologue := noiseChannelPrologue(registration.EnvironmentID, registration.ExecutorRegistrationID, frame.StreamID)
				handshake, authorization, err := readRemoteNoiseHandshake(identity, prologue, frame.HandshakeBytes)
				if err != nil || len(authorization) > maxHarnessKeyAuthorizationBytes || !utf8.ValidString(authorization) {
					if handshake != nil {
						handshake.Destroy()
					}
					queueRelayReset(outgoing, frame.StreamID)
					failedHandshakes++
					if failedHandshakes >= maxFailedNoiseHandshakes {
						return errors.New("closing Noise relay after repeated handshake failures")
					}
					continue
				}
				validationID := nextValidationID
				nextValidationID++
				pending[frame.StreamID] = pendingRemoteRelayHandshake{validationID: validationID, handshake: handshake}
				go func(streamID string, publicKey RemotePublicKey, authorization string, validationID uint64) {
					validationCtx, validationCancel := context.WithTimeout(connectionCtx, harnessKeyValidationTimeout)
					defer validationCancel()
					err := validateRemoteHarnessKey(validationCtx, cfg, registration, publicKey, authorization)
					select {
					case validationDone <- remoteRelayValidationResult{streamID: streamID, validationID: validationID, err: err}:
					case <-connectionCtx.Done():
					}
				}(frame.StreamID, handshake.publicKey, authorization, validationID)
			case relayFrameData:
				stream := streams[frame.StreamID]
				if stream == nil {
					if item, exists := pending[frame.StreamID]; exists {
						delete(pending, frame.StreamID)
						item.handshake.Destroy()
						failedHandshakes++
					}
					queueRelayReset(outgoing, frame.StreamID)
					if failedHandshakes >= maxFailedNoiseHandshakes {
						return errors.New("closing Noise relay after repeated handshake failures")
					}
					continue
				}
				if err := stream.Receive(frame.Data); err != nil {
					delete(streams, frame.StreamID)
					stream.Close()
					queueRelayReset(outgoing, frame.StreamID)
				}
			case relayFrameReset:
				if item, exists := pending[frame.StreamID]; exists {
					delete(pending, frame.StreamID)
					item.handshake.Destroy()
				}
				if stream := streams[frame.StreamID]; stream != nil {
					delete(streams, frame.StreamID)
					stream.Close()
				}
			case relayFrameAck, relayFrameResume, relayFrameHeartbeat:
			}
		}
	}
}

func runRemoteRelayWriter(ctx context.Context, conn *websocket.Conn, outgoing <-chan []byte, done chan<- error) {
	ticker := time.NewTicker(remoteRelayKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case payload := <-outgoing:
			if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
				done <- fmt.Errorf("Noise multiplexed environment websocket write failed: %w", err)
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				done <- fmt.Errorf("Noise multiplexed environment websocket keepalive failed: %w", err)
				return
			}
		}
	}
}

func runRemoteRelayReader(ctx context.Context, conn *websocket.Conn, incoming chan<- remoteRelayIncoming) {
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			select {
			case incoming <- remoteRelayIncoming{err: err}:
			case <-ctx.Done():
			}
			return
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			continue
		}
		select {
		case incoming <- remoteRelayIncoming{frame: frame}:
		case <-ctx.Done():
			return
		}
	}
}

func validateRemoteHarnessKey(
	ctx context.Context,
	cfg RemoteEnvironmentConfig,
	registration *remoteRegistrationResponse,
	publicKey RemotePublicKey,
	authorization string,
) error {
	body, err := json.Marshal(remoteHarnessValidationRequest{
		ExecutorRegistrationID:  registration.ExecutorRegistrationID,
		HarnessPublicKey:        publicKey,
		HarnessKeyAuthorization: authorization,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, remoteEndpointURL(cfg.BaseURL, "/cloud/environment/"+cfg.EnvironmentID+"/validate"), strings.NewReader(string(body)))
	if err != nil {
		return errors.New("environment registry harness key validation failed")
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
		return errors.New("environment registry harness key validation failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("environment registry harness key validation failed (%s)", response.Status)
	}
	var decoded remoteHarnessValidationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&decoded); err != nil || !decoded.Valid {
		return errors.New("environment registry rejected Noise relay harness key")
	}
	return nil
}

func noiseChannelPrologue(environmentID string, registrationID string, streamID string) []byte {
	parts := [][]byte{
		[]byte("codex-exec-server-relay-noise/v1"),
		[]byte(environmentID),
		[]byte(registrationID),
		[]byte(streamID),
	}
	var prologue []byte
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		prologue = append(prologue, length[:]...)
		prologue = append(prologue, part...)
	}
	return prologue
}

func queueRelayReset(outgoing chan<- []byte, streamID string) {
	encoded, err := encodeRelayMessageFrame(newRelayResetFrame(streamID))
	if err != nil {
		return
	}
	select {
	case outgoing <- encoded:
	default:
	}
}

func queueRelayFrame(outgoing chan<- []byte, frame relayMessageFrame) error {
	encoded, err := encodeRelayMessageFrame(frame)
	if err != nil {
		return err
	}
	select {
	case outgoing <- encoded:
		return nil
	default:
		return errors.New("Noise relay outgoing queue is full")
	}
}

type remoteNoiseVirtualStream struct {
	streamID    string
	instanceID  uint64
	transport   *clatter.TransportState
	transportMu sync.RWMutex
	conn        net.Conn
	incoming    chan []byte
	cancel      context.CancelFunc
	closeOnce   sync.Once
	reorder     remoteOrderedCiphertexts
	decoder     remoteJSONRPCDecoder
}

func newRemoteNoiseVirtualStream(
	ctx context.Context,
	server *Server,
	streamID string,
	instanceID uint64,
	transport *clatter.TransportState,
	outgoing chan<- []byte,
	closed chan<- remoteRelayClosedStream,
) *remoteNoiseVirtualStream {
	streamCtx, cancel := context.WithCancel(ctx)
	clientConn, serverConn := net.Pipe()
	stream := &remoteNoiseVirtualStream{
		streamID: streamID, instanceID: instanceID, transport: transport,
		conn: clientConn, incoming: make(chan []byte, remoteRelayChannelCapacity), cancel: cancel,
	}
	go func() {
		defer serverConn.Close()
		_ = server.serveConnectionStream(streamCtx, serverConn, serverConn)
	}()
	go stream.runInboundWriter(streamCtx)
	go stream.runOutboundReader(streamCtx, outgoing, closed)
	return stream
}

func (s *remoteNoiseVirtualStream) Receive(data relayData) error {
	frames, err := s.reorder.Push(data.Seq, data.Payload)
	if err != nil {
		return err
	}
	for _, ciphertext := range frames {
		if len(ciphertext) < clatter.TagLen {
			return errors.New("Noise relay ciphertext is too short")
		}
		plaintext := make([]byte, len(ciphertext)-clatter.TagLen)
		s.transportMu.RLock()
		length, err := s.transport.Receive(ciphertext, plaintext)
		s.transportMu.RUnlock()
		if err != nil {
			return fmt.Errorf("Noise relay decryption failed: %w", err)
		}
		messages, err := s.decoder.Push(plaintext[:length])
		if err != nil {
			return err
		}
		for _, message := range messages {
			select {
			case s.incoming <- message:
			default:
				return errors.New("Noise virtual stream inbound queue is full or closed")
			}
		}
	}
	return nil
}

func (s *remoteNoiseVirtualStream) runInboundWriter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-s.incoming:
			message = append(message, '\n')
			if _, err := s.conn.Write(message); err != nil {
				s.Close()
				return
			}
		}
	}
}

func (s *remoteNoiseVirtualStream) runOutboundReader(ctx context.Context, outgoing chan<- []byte, closed chan<- remoteRelayClosedStream) {
	defer func() {
		select {
		case closed <- remoteRelayClosedStream{streamID: s.streamID, instanceID: s.instanceID}:
		case <-ctx.Done():
		}
	}()
	reader := bufio.NewReader(s.conn)
	var nextSeq uint32
	for {
		message, err := readRemoteRelayJSONLine(reader)
		if err != nil {
			return
		}
		framed := make([]byte, 4, len(message)+4)
		binary.BigEndian.PutUint32(framed, uint32(len(message)))
		framed = append(framed, message...)
		for len(framed) > 0 {
			chunkLen := min(len(framed), noiseRecordPlaintextLen)
			ciphertext := make([]byte, chunkLen+clatter.TagLen)
			s.transportMu.RLock()
			length, err := s.transport.Send(framed[:chunkLen], ciphertext)
			s.transportMu.RUnlock()
			if err != nil {
				return
			}
			if nextSeq == ^uint32(0) {
				return
			}
			if err := queueRelayFrame(outgoing, newRelayDataFrame(s.streamID, nextSeq, ciphertext[:length])); err != nil {
				return
			}
			nextSeq++
			framed = framed[chunkLen:]
		}
	}
}

func (s *remoteNoiseVirtualStream) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.conn.Close()
		s.transportMu.Lock()
		s.transport.Destroy()
		s.transportMu.Unlock()
	})
}

func readRemoteRelayJSONLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		line = append(line, fragment...)
		if len(line) > maxNoiseJSONRPCMessageLen+1 {
			return nil, errors.New("Noise relay JSON-RPC message exceeds maximum length")
		}
		if err == nil {
			line = line[:len(line)-1]
			if len(line) == 0 || !json.Valid(line) {
				return nil, errors.New("Noise relay JSON-RPC message is invalid")
			}
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

type remoteJSONRPCDecoder struct {
	buffered []byte
}

func (d *remoteJSONRPCDecoder) Push(plaintext []byte) ([][]byte, error) {
	if len(plaintext) > noiseRecordPlaintextLen {
		return nil, errors.New("Noise relay plaintext record exceeds maximum length")
	}
	d.buffered = append(d.buffered, plaintext...)
	var messages [][]byte
	for len(d.buffered) >= 4 {
		messageLen := int(binary.BigEndian.Uint32(d.buffered[:4]))
		if messageLen == 0 || messageLen > maxNoiseJSONRPCMessageLen {
			return nil, errors.New("Noise relay JSON-RPC message has invalid length")
		}
		if len(d.buffered) < 4+messageLen {
			break
		}
		message := append([]byte(nil), d.buffered[4:4+messageLen]...)
		if !json.Valid(message) {
			return nil, errors.New("Noise relay JSON-RPC message is invalid")
		}
		messages = append(messages, message)
		d.buffered = d.buffered[4+messageLen:]
	}
	if len(d.buffered) > 4+maxNoiseJSONRPCMessageLen {
		return nil, errors.New("Noise relay JSON-RPC reassembly buffer exceeds maximum length")
	}
	return messages, nil
}

type remoteOrderedCiphertexts struct {
	nextSeq      uint32
	pending      map[uint32][]byte
	pendingBytes int
}

func (o *remoteOrderedCiphertexts) Push(seq uint32, payload []byte) ([][]byte, error) {
	if seq < o.nextSeq {
		return nil, nil
	}
	if o.pending == nil {
		o.pending = map[uint32][]byte{}
	}
	if _, exists := o.pending[seq]; exists {
		return nil, nil
	}
	if seq > o.nextSeq {
		if seq-o.nextSeq > maxNoiseRelayReorderDistance {
			return nil, errors.New("Noise relay ciphertext exceeds reorder window")
		}
		if o.pendingBytes+len(payload) > maxNoiseRelayPendingBytes {
			return nil, errors.New("Noise relay pending ciphertext buffer is full")
		}
		o.pending[seq] = append([]byte(nil), payload...)
		o.pendingBytes += len(payload)
		return nil, nil
	}
	ready := [][]byte{append([]byte(nil), payload...)}
	if o.nextSeq == ^uint32(0) {
		return nil, errors.New("Noise relay sequence number exhausted")
	}
	o.nextSeq++
	for {
		payload, exists := o.pending[o.nextSeq]
		if !exists {
			break
		}
		delete(o.pending, o.nextSeq)
		o.pendingBytes -= len(payload)
		ready = append(ready, payload)
		if o.nextSeq == ^uint32(0) {
			return nil, errors.New("Noise relay sequence number exhausted")
		}
		o.nextSeq++
	}
	return ready, nil
}
