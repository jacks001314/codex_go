package provisionservice

import (
	"errors"
	"io"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"
)

type testClock struct {
	current time.Time
}

func (c *testClock) now() time.Time { return c.current }

func (c *testClock) sleep(duration time.Duration) { c.current = c.current.Add(duration) }

type scriptedConn struct {
	pid       uint32
	exchange  func(frame *Frame) (Response, error)
	exchanges int
	closed    bool
}

func (c *scriptedConn) ServerPID() uint32 { return c.pid }

func (c *scriptedConn) Exchange(frame *Frame, deadline time.Time) (Response, error) {
	c.exchanges++
	return c.exchange(frame)
}

func (c *scriptedConn) Close() error {
	c.closed = true
	return nil
}

type scriptedTransport struct {
	connect      func(deadline time.Time) (Conn, error)
	afterRestart func(previousPID uint32, deadline time.Time) (Conn, error)
	connects     int
	restarts     int
}

func (t *scriptedTransport) Connect(deadline time.Time) (Conn, error) {
	t.connects++
	if t.connect == nil {
		return nil, ErrServiceUnavailable
	}
	return t.connect(deadline)
}

func (t *scriptedTransport) ConnectAfterRestart(previousPID uint32, deadline time.Time) (Conn, error) {
	t.restarts++
	if t.afterRestart == nil {
		return nil, ErrServiceUnavailable
	}
	return t.afterRestart(previousPID, deadline)
}

func newTestClient(transport Transport, registeredCore bool) (*Client, *testClock) {
	clock := &testClock{current: time.Unix(1_700_000_000, 0)}
	client := &Client{
		Transport:                transport,
		RegisteredCore:           func() bool { return registeredCore },
		WorkloadIdentitySelected: func() bool { return false },
		Now:                      clock.now,
		Sleep:                    clock.sleep,
	}
	return client, clock
}

func okConn() *scriptedConn {
	return &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) { return Response{Kind: ResponseOK}, nil }}
}

func TestProvisionReportsUnavailableWithoutService(t *testing.T) {
	transport := &scriptedTransport{}
	client, _ := newTestClient(transport, false)
	outcome, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
	if err != nil || outcome != Unavailable {
		t.Fatalf("ProvisionWindowsSandbox() = %v, %v", outcome, err)
	}
	if transport.connects != 1 {
		t.Fatalf("connects = %d", transport.connects)
	}
}

func TestProvisionReturnsServiceErrorMessage(t *testing.T) {
	conn := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseError, Message: "refused"}, nil
	}}
	client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, false)
	if _, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{}); err == nil || err.Error() != "refused" {
		t.Fatalf("error = %v", err)
	}
	if !conn.closed {
		t.Fatal("connection must be closed")
	}
}

func TestProvisionUnavailableResponseAllowsFallback(t *testing.T) {
	conn := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseUnavailable}, nil
	}}
	client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, false)
	outcome, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
	if err != nil || outcome != Unavailable {
		t.Fatalf("ProvisionWindowsSandbox() = %v, %v", outcome, err)
	}
}

func TestRegisteredCoreRefusesHelperFallback(t *testing.T) {
	client, _ := newTestClient(&scriptedTransport{}, true)
	_, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
	if err == nil || err.Error() != "app runtime provisioning service is unavailable; refusing helper fallback" {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkloadIdentitySelectsHelperFallbackExceptForRegisteredCore(t *testing.T) {
	for _, registeredCore := range []bool{false, true} {
		transport := &scriptedTransport{connect: func(time.Time) (Conn, error) { return okConn(), nil }}
		client, _ := newTestClient(transport, registeredCore)
		client.WorkloadIdentitySelected = func() bool { return true }
		outcome, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
		if transport.connects != 0 {
			t.Fatalf("registered=%v: workload identity must not contact the service", registeredCore)
		}
		if registeredCore {
			if err == nil || err.Error() != "app runtime provisioning service is unavailable; refusing helper fallback" {
				t.Fatalf("registered: error = %v", err)
			}
			continue
		}
		if err != nil || outcome != Unavailable {
			t.Fatalf("unregistered: outcome = %v, %v", outcome, err)
		}
	}
}

func TestWorkloadIdentityDoesNotBlockRegisteredRefresh(t *testing.T) {
	transport := &scriptedTransport{connect: func(time.Time) (Conn, error) { return okConn(), nil }}
	client, _ := newTestClient(transport, true)
	client.WorkloadIdentitySelected = func() bool { return true }
	outcome, err := client.RefreshRegisteredCore("/home", Settings{}, Listeners{})
	if err != nil || outcome != Provisioned {
		t.Fatalf("RefreshRegisteredCore() = %v, %v", outcome, err)
	}
	if transport.connects != 1 {
		t.Fatalf("connects = %d", transport.connects)
	}
}

func TestRefreshRegisteredCoreRequiresGatedRuntime(t *testing.T) {
	transport := &scriptedTransport{}
	client, _ := newTestClient(transport, false)
	_, err := client.RefreshRegisteredCore("/home", Settings{}, Listeners{})
	if err == nil || err.Error() != "registered Core is not enabled" {
		t.Fatalf("error = %v", err)
	}
	if transport.connects != 0 {
		t.Fatalf("connects = %d", transport.connects)
	}
}

func TestRefreshRequestCarriesRoutingFlags(t *testing.T) {
	var seen []*ProvisionRequest
	conn := &scriptedConn{pid: 10, exchange: func(frame *Frame) (Response, error) {
		seen = append(seen, frame.Message.Request)
		return Response{Kind: ResponseOK}, nil
	}}
	client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, true)
	if outcome, err := client.RefreshRegisteredCore("/home", Settings{ProxyPorts: []uint16{1}}, Listeners{}); err != nil || outcome != Provisioned {
		t.Fatalf("RefreshRegisteredCore() = %v, %v", outcome, err)
	}
	if len(seen) != 1 || seen[0] == nil || !seen[0].RegisteredCore || !seen[0].RefreshOnly {
		t.Fatalf("request = %#v", seen)
	}
}

func TestGroupChangeRetryReconnectsOnceAndSucceeds(t *testing.T) {
	first := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseError, Message: GroupChanged}, nil
	}}
	second := okConn()
	transport := &scriptedTransport{}
	calls := 0
	transport.connect = func(time.Time) (Conn, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}
	client, _ := newTestClient(transport, false)
	outcome, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
	if err != nil || outcome != Provisioned {
		t.Fatalf("ProvisionWindowsSandbox() = %v, %v", outcome, err)
	}
	if transport.connects != 2 || !first.closed || !second.closed {
		t.Fatalf("connects = %d, closed = %v/%v", transport.connects, first.closed, second.closed)
	}
}

func TestGroupChangeRetryRefusesWhenPipeStaysUnavailable(t *testing.T) {
	first := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseError, Message: GroupChanged}, nil
	}}
	transport := &scriptedTransport{}
	calls := 0
	transport.connect = func(time.Time) (Conn, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, ErrServiceUnavailable
	}
	client, _ := newTestClient(transport, false)
	_, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
	if err == nil || err.Error() != "refreshed sandbox service pipe is unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestGroupChangeRetryPropagatesTransportErrors(t *testing.T) {
	first := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseError, Message: GroupChanged}, nil
	}}
	transport := &scriptedTransport{}
	calls := 0
	transport.connect = func(time.Time) (Conn, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return nil, errors.New("boom")
	}
	client, _ := newTestClient(transport, false)
	if _, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{}); err == nil || err.Error() != "boom" {
		t.Fatalf("error = %v", err)
	}
}

func TestGroupChangeRetryKeepsRetryTransportErrors(t *testing.T) {
	first := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{Kind: ResponseError, Message: GroupChanged}, nil
	}}
	second := &scriptedConn{pid: 11, exchange: func(*Frame) (Response, error) {
		return Response{}, io.ErrUnexpectedEOF
	}}
	transport := &scriptedTransport{}
	calls := 0
	transport.connect = func(time.Time) (Conn, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}
	client, _ := newTestClient(transport, false)
	if _, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
}

func TestResponseDisconnectDuringNormalExchangeFallsBack(t *testing.T) {
	for _, disconnect := range []error{io.ErrUnexpectedEOF, errnoBrokenPipe, errnoNoData, errnoPipeNotConnected, os.ErrDeadlineExceeded} {
		conn := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
			return Response{}, disconnect
		}}
		client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, false)
		outcome, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
		if err != nil || outcome != Unavailable {
			t.Fatalf("%v: outcome = %v, %v", disconnect, outcome, err)
		}
	}
}

func TestPermissionErrorIsNotFoldedIntoFallback(t *testing.T) {
	conn := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{}, os.ErrPermission
	}}
	client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, false)
	if _, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
}

func TestRestartRetryResumesRegisteredRefresh(t *testing.T) {
	first := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
		return Response{}, io.ErrUnexpectedEOF
	}}
	second := okConn()
	transport := &scriptedTransport{connect: func(time.Time) (Conn, error) { return first, nil }}
	transport.afterRestart = func(previousPID uint32, deadline time.Time) (Conn, error) {
		if previousPID != 10 {
			t.Fatalf("previous pid = %d", previousPID)
		}
		return second, nil
	}
	client, _ := newTestClient(transport, true)
	outcome, err := client.RefreshRegisteredCore("/home", Settings{}, Listeners{})
	if err != nil || outcome != Provisioned {
		t.Fatalf("RefreshRegisteredCore() = %v, %v", outcome, err)
	}
	if transport.restarts != 1 || !first.closed || !second.closed {
		t.Fatalf("restarts = %d, closed = %v/%v", transport.restarts, first.closed, second.closed)
	}
}

func TestRestartRetryStopsAfterTwoResponseDisconnects(t *testing.T) {
	makeConn := func() *scriptedConn {
		return &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
			return Response{}, io.ErrUnexpectedEOF
		}}
	}
	first := makeConn()
	transport := &scriptedTransport{connect: func(time.Time) (Conn, error) { return first, nil }}
	transport.afterRestart = func(uint32, time.Time) (Conn, error) { return makeConn(), nil }
	client, _ := newTestClient(transport, true)
	_, err := client.RefreshRegisteredCore("/home", Settings{}, Listeners{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
	if transport.restarts != MaxServiceRestarts {
		t.Fatalf("restarts = %d", transport.restarts)
	}
}

func TestTakeRestartAllowsTwoResponseDisconnectsButNotAThird(t *testing.T) {
	client, clock := newTestClient(&scriptedTransport{}, false)
	deadline := clock.now().Add(time.Minute)
	remaining := MaxServiceRestarts
	decisions := make([]bool, 0, 3)
	for index := 0; index < 3; index++ {
		decisions = append(decisions, client.takeRestart(io.ErrUnexpectedEOF, &remaining, deadline))
	}
	if !reflect.DeepEqual(decisions, []bool{true, true, false}) || remaining != 0 {
		t.Fatalf("decisions = %v, remaining = %d", decisions, remaining)
	}
}

func TestTakeRestartAcceptsOnlyResponseDisconnectErrors(t *testing.T) {
	client, clock := newTestClient(&scriptedTransport{}, false)
	deadline := clock.now().Add(time.Minute)
	for _, disconnect := range []error{
		io.ErrUnexpectedEOF,
		syscall.EPIPE,
		errnoBrokenPipe,
		errnoNoData,
		errnoPipeNotConnected,
	} {
		remaining := MaxServiceRestarts
		if !client.takeRestart(disconnect, &remaining, deadline) || remaining != 1 {
			t.Fatalf("%v: remaining = %d", disconnect, remaining)
		}
	}
}

func TestTakeRestartIgnoresExplicitReplies(t *testing.T) {
	client, clock := newTestClient(&scriptedTransport{}, false)
	deadline := clock.now().Add(time.Minute)
	remaining := MaxServiceRestarts
	if client.takeRestart(nil, &remaining, deadline) || remaining != MaxServiceRestarts {
		t.Fatalf("remaining = %d", remaining)
	}
}

func TestTakeRestartIgnoresTimeoutPermissionAndProtocolErrors(t *testing.T) {
	client, clock := newTestClient(&scriptedTransport{}, false)
	deadline := clock.now().Add(time.Minute)
	for _, failure := range []error{
		os.ErrDeadlineExceeded,
		os.ErrPermission,
		errors.New("unexpected sandbox provisioning response message"),
		errors.New("not JSON"),
	} {
		remaining := MaxServiceRestarts
		if client.takeRestart(failure, &remaining, deadline) || remaining != MaxServiceRestarts {
			t.Fatalf("%v: remaining = %d", failure, remaining)
		}
	}
}

func TestTakeRestartRejectsDisconnectAfterDeadline(t *testing.T) {
	client, clock := newTestClient(&scriptedTransport{}, false)
	deadline := clock.now()
	remaining := MaxServiceRestarts
	if client.takeRestart(io.ErrUnexpectedEOF, &remaining, deadline) || remaining != MaxServiceRestarts {
		t.Fatalf("remaining = %d", remaining)
	}
	if err := client.remainingTime(deadline); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("remainingTime() error = %v", err)
	}
}

func TestRegisterDesktopInstallationOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		connect func(time.Time) (Conn, error)
		wantErr string
	}{
		{name: "ok", connect: func(time.Time) (Conn, error) { return okConn(), nil }},
		{name: "unavailable response", connect: func(time.Time) (Conn, error) {
			return &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
				return Response{Kind: ResponseUnavailable}, nil
			}}, nil
		}, wantErr: "desktop uninstall registration service is unavailable"},
		{name: "service error", connect: func(time.Time) (Conn, error) {
			return &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) {
				return Response{Kind: ResponseError, Message: "denied"}, nil
			}}, nil
		}, wantErr: "denied"},
		{name: "absent service", connect: func(time.Time) (Conn, error) { return nil, ErrServiceUnavailable }, wantErr: "desktop uninstall registration service is unavailable"},
	}
	for _, testCase := range cases {
		client, _ := newTestClient(&scriptedTransport{connect: testCase.connect}, false)
		err := client.RegisterDesktopInstallation("/home")
		if testCase.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: error = %v", testCase.name, err)
			}
			continue
		}
		if err == nil || err.Error() != testCase.wantErr {
			t.Fatalf("%s: error = %v", testCase.name, err)
		}
	}
}
