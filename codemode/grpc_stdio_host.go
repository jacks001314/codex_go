package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
)

// stdioConn adapts a duplex pipe pair to net.Conn so gRPC can run over a host
// process's stdin/stdout (Rust #52723's grpc+stdio transport).
type stdioConn struct {
	reader io.Reader
	writer io.Writer

	closeOnce sync.Once
	closeErr  error
}

func newStdioConn(reader io.Reader, writer io.Writer) *stdioConn {
	return &stdioConn{reader: reader, writer: writer}
}

func (c *stdioConn) Read(buffer []byte) (int, error)  { return c.reader.Read(buffer) }
func (c *stdioConn) Write(buffer []byte) (int, error) { return c.writer.Write(buffer) }

func (c *stdioConn) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if closer, ok := c.writer.(io.Closer); ok {
			c.closeErr = closer.Close()
		}
		if closer, ok := c.reader.(io.Closer); ok {
			if err := closer.Close(); c.closeErr == nil {
				c.closeErr = err
			}
		}
	})
	return c.closeErr
}

type stdioAddr string

func (a stdioAddr) Network() string { return "stdio" }
func (a stdioAddr) String() string  { return string(a) }

func (c *stdioConn) LocalAddr() net.Addr              { return stdioAddr("stdio-local") }
func (c *stdioConn) RemoteAddr() net.Addr             { return stdioAddr("stdio-remote") }
func (c *stdioConn) SetDeadline(time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(time.Time) error { return nil }

// singleConnListener yields one accepted connection (the stdio pipe) to the
// gRPC server.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var accepted net.Conn
	l.once.Do(func() { accepted = l.conn })
	if accepted != nil {
		return accepted, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	if l == nil {
		return nil
	}
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return stdioAddr("stdio-listener") }

// RunGrpcStdioHost serves the code-mode host gRPC Transport over stdin/stdout.
func RunGrpcStdioHost(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ensureGrpcZstdRegistered()
	listener := &singleConnListener{done: make(chan struct{})}
	// Rust #52723: the host exits when its stdio connection closes. Closing the
	// accepted conn unblocks Accept so Serve returns instead of waiting forever
	// for a replacement connection.
	listener.conn = &connectionCloseNotifier{
		Conn: newStdioConn(stdin, stdout),
		notify: func() {
			_ = listener.Close()
		},
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(ProtocolMaxFrameBytes),
		grpc.MaxSendMsgSize(ProtocolMaxFrameBytes),
	)
	codemodev1.RegisterCodeModeHostServer(server, &grpcStdioHostServer{ctx: ctx})
	go func() {
		<-ctx.Done()
		server.Stop()
		_ = listener.Close()
	}()
	err := server.Serve(listener)
	select {
	case <-listener.done:
		if errors.Is(err, net.ErrClosed) || errors.Is(err, grpc.ErrServerStopped) || err == nil {
			return nil
		}
	default:
	}
	return err
}

// connectionCloseNotifier closes the single-connection listener when gRPC
// releases the stdio connection.
type connectionCloseNotifier struct {
	net.Conn
	once   sync.Once
	notify func()
}

func (c *connectionCloseNotifier) Close() error {
	err := c.Conn.Close()
	if c.notify != nil {
		c.once.Do(c.notify)
	}
	return err
}

type grpcStdioHostServer struct {
	codemodev1.UnimplementedCodeModeHostServer
	ctx context.Context
}

// Transport runs the shared code-mode host loop over one framed gRPC stream.
func (s *grpcStdioHostServer) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
	// Rust #57212: compress responses for clients that advertise zstd and keep
	// identity responses for clients that do not.
	_ = grpc.SetSendCompressor(stream.Context(), grpcZstdEncodingName)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	inbound := make(chan ClientToHost, 16)
	readErr := make(chan error, 1)
	go func() {
		defer close(inbound)
		for {
			message, err := stream.Recv()
			if err != nil {
				readErr <- err
				return
			}
			var client ClientToHost
			if err := json.Unmarshal(message.Payload, &client); err != nil {
				readErr <- err
				return
			}
			select {
			case inbound <- client:
			case <-ctx.Done():
				return
			}
		}
	}()
	read := func(message *ClientToHost) (bool, error) {
		select {
		case client, ok := <-inbound:
			if !ok {
				select {
				case err := <-readErr:
					if errors.Is(err, io.EOF) {
						return false, nil
					}
					return false, err
				default:
					return false, nil
				}
			}
			*message = client
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	var writeMu sync.Mutex
	write := func(message any) error {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return stream.Send(&codemodev1.FramedMessage{Payload: data})
	}
	return runCodemodeHost(ctx, read, write)
}
