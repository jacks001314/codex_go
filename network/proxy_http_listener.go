package network

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"sync"

	"github.com/valyala/fasthttp"
)

type proxyRawRequestContextKey struct{}

type proxyRawRequestState struct {
	validationErr error
}

type proxyHTTPValidationListener struct {
	net.Listener
}

func (l proxyHTTPValidationListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &proxyHTTPValidationConn{Conn: conn, state: &proxyRawRequestState{}}, nil
}

type proxyHTTPValidationConn struct {
	net.Conn
	state  *proxyRawRequestState
	once   sync.Once
	replay bytes.Reader
}

func (c *proxyHTTPValidationConn) Read(buffer []byte) (int, error) {
	c.once.Do(c.readAndValidateHeader)
	if c.replay.Len() > 0 {
		return c.replay.Read(buffer)
	}
	return c.Conn.Read(buffer)
}

func (c *proxyHTTPValidationConn) readAndValidateHeader() {
	var captured bytes.Buffer
	reader := bufio.NewReader(io.TeeReader(c.Conn, &captured))
	var header fasthttp.RequestHeader
	if err := header.Read(reader); err == nil {
		if !bytes.EqualFold(header.Method(), []byte("CONNECT")) {
			c.state.validationErr = validateAbsoluteFormTarget(string(header.RequestURI()), string(header.Host()))
		}
	}
	c.replay.Reset(captured.Bytes())
}

func proxyHTTPConnContext(ctx context.Context, conn net.Conn) context.Context {
	if validating, ok := conn.(*proxyHTTPValidationConn); ok {
		return context.WithValue(ctx, proxyRawRequestContextKey{}, validating.state)
	}
	return ctx
}

func proxyRawRequestValidationError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(proxyRawRequestContextKey{}).(*proxyRawRequestState)
	if state == nil {
		return nil
	}
	return state.validationErr
}
