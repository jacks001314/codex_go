// Select the first authenticated CONNECT, preferring QUIC with a short head
// start, and honor system HTTPS proxies without attempting direct QUIC.

package tcptunnel

import (
	"context"
	"crypto/tls"
	"time"
)

// transportConfig holds the TLS client configuration used for every gateway
// and proxy leg.
type transportConfig struct {
	tls *tls.Config
}

func newTransportConfig(tlsConfig *tls.Config) *transportConfig {
	return &transportConfig{tls: tlsConfig}
}

// establishedConnection is a verified transport that can admit one CONNECT.
type establishedConnection interface {
	admit(ctx context.Context, spec requestSpec) (*streamTunnel, error)
	close()
}

type connectionResult struct {
	connection establishedConnection
	err        error
}

type admissionResult struct {
	tunnel *streamTunnel
	err    error
}

// open establishes a tunnel inside one ten-second deadline that covers
// transport setup, credential retrieval and admission. The request future is
// polled only after a gateway has passed TLS verification.
func (c *transportConfig) open(ctx context.Context, target *ProxyTarget, request func(context.Context) (requestSpec, error)) (*streamTunnel, error) {
	setupCtx, cancel := context.WithTimeout(ctx, proxyConnectTimeout)
	defer cancel()
	tunnel, err := c.openWithin(ctx, setupCtx, target, request)
	if err != nil && setupCtx.Err() == context.DeadlineExceeded {
		return nil, tunnelTimeout()
	}
	return tunnel, err
}

func (c *transportConfig) openWithin(ctx context.Context, setupCtx context.Context, target *ProxyTarget, request func(context.Context) (requestSpec, error)) (*streamTunnel, error) {
	intercept, err := systemProxy(target)
	if err != nil {
		return nil, err
	}
	if intercept != nil {
		// A configured proxy is the only route: never race direct QUIC past it.
		connection, err := connectThroughProxy(setupCtx, target, c.tls, intercept)
		if err != nil {
			return nil, err
		}
		return admitOne(ctx, setupCtx, connection, request)
	}

	h3Results := make(chan connectionResult, 1)
	h2Results := make(chan connectionResult, 1)
	go func() {
		connection, err := connectH3(setupCtx, target, c.tls)
		h3Results <- connectionResult{connection: connection, err: err}
	}()
	go func() {
		timer := time.NewTimer(quicHeadStart)
		defer timer.Stop()
		select {
		case <-setupCtx.Done():
			h2Results <- connectionResult{err: setupCtx.Err()}
			return
		case <-timer.C:
		}
		connection, err := connectH2(setupCtx, target, c.tls)
		h2Results <- connectionResult{connection: connection, err: err}
	}()

	var first connectionResult
	var second <-chan connectionResult
	select {
	case first = <-h3Results:
		second = h2Results
	default:
		select {
		case first = <-h3Results:
			second = h2Results
		case first = <-h2Results:
			second = h3Results
		case <-setupCtx.Done():
			return nil, setupCtx.Err()
		}
	}
	if first.err != nil {
		if isRejection(first.err) {
			return nil, first.err
		}
		select {
		case other := <-second:
			if other.err != nil {
				return nil, other.err
			}
			return admitOne(ctx, setupCtx, other.connection, request)
		case <-setupCtx.Done():
			return nil, setupCtx.Err()
		}
	}
	return c.raceAdmissions(ctx, setupCtx, first.connection, second, request)
}

// admitOne admits the only connection in play.
func admitOne(ctx context.Context, setupCtx context.Context, connection establishedConnection, request func(context.Context) (requestSpec, error)) (*streamTunnel, error) {
	spec, err := request(ctx)
	if err != nil {
		connection.close()
		return nil, err
	}
	results := make(chan admissionResult, 1)
	go admitAttempt(ctx, connection, spec, results)
	select {
	case result := <-results:
		if result.err != nil {
			return nil, result.err
		}
		return result.tunnel, nil
	case <-setupCtx.Done():
		go closeLateAdmission(results)
		return nil, setupCtx.Err()
	}
}

// raceAdmissions admits the preferred transport and, once the alternate is
// ready, admits the same request there too. The first accepted CONNECT wins; a
// rejection is terminal, and any other failure falls back to the other
// transport.
func (c *transportConfig) raceAdmissions(ctx context.Context, setupCtx context.Context, preferred establishedConnection, alternate <-chan connectionResult, request func(context.Context) (requestSpec, error)) (*streamTunnel, error) {
	spec, err := request(ctx)
	if err != nil {
		preferred.close()
		go closeLateConnection(alternate)
		return nil, err
	}
	preferredCtx, cancelPreferred := context.WithCancel(ctx)
	alternateCtx, cancelAlternate := context.WithCancel(ctx)
	preferredResults := make(chan admissionResult, 1)
	alternateResults := make(chan admissionResult, 1)
	go admitAttempt(preferredCtx, preferred, spec, preferredResults)
	go func() {
		select {
		case other := <-alternate:
			if other.err != nil {
				alternateResults <- admissionResult{err: other.err}
				return
			}
			admitAttempt(alternateCtx, other.connection, spec, alternateResults)
		case <-alternateCtx.Done():
			alternateResults <- admissionResult{err: alternateCtx.Err()}
		}
	}()

	var first admissionResult
	firstPreferred := true
	select {
	case first = <-preferredResults:
	case first = <-alternateResults:
		firstPreferred = false
	case <-setupCtx.Done():
		cancelPreferred()
		cancelAlternate()
		return nil, setupCtx.Err()
	}
	winnerCancel := cancelPreferred
	loserResults, loserCancel := alternateResults, cancelAlternate
	if !firstPreferred {
		winnerCancel = cancelAlternate
		loserResults, loserCancel = preferredResults, cancelPreferred
	}
	if first.err == nil {
		loserCancel()
		go closeLateAdmission(loserResults)
		return finishTunnel(first.tunnel, winnerCancel), nil
	}
	if isRejection(first.err) {
		loserCancel()
		winnerCancel()
		go closeLateAdmission(loserResults)
		return nil, first.err
	}
	var other admissionResult
	select {
	case other = <-loserResults:
	case <-setupCtx.Done():
		cancelPreferred()
		cancelAlternate()
		return nil, setupCtx.Err()
	}
	if other.err != nil {
		cancelPreferred()
		cancelAlternate()
		return nil, other.err
	}
	winnerCancel()
	return finishTunnel(other.tunnel, loserCancel), nil
}

func admitAttempt(ctx context.Context, connection establishedConnection, spec requestSpec, results chan<- admissionResult) {
	tunnel, err := connection.admit(ctx, spec)
	if err != nil {
		connection.close()
		results <- admissionResult{err: err}
		return
	}
	results <- admissionResult{tunnel: tunnel}
}

// finishTunnel keeps the winning stream alive while releasing the losing
// attempt as soon as the winner is closed.
func finishTunnel(tunnel *streamTunnel, cancels ...context.CancelFunc) *streamTunnel {
	if tunnel == nil {
		return nil
	}
	inner := tunnel.cleanup
	tunnel.cleanup = func() {
		if inner != nil {
			inner()
		}
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	}
	return tunnel
}

func closeLateConnection(results <-chan connectionResult) {
	select {
	case late := <-results:
		if late.connection != nil {
			late.connection.close()
		}
	case <-time.After(proxyConnectTimeout):
	}
}

func closeLateAdmission(results <-chan admissionResult) {
	select {
	case late := <-results:
		if late.tunnel != nil {
			late.tunnel.close()
		}
	case <-time.After(proxyConnectTimeout):
	}
}
