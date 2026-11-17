package tcptunnel

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func portalHeaders() http.Header {
	headers := http.Header{}
	headers.Set("x-portals-auth-mode", "oauth")
	headers.Set("x-portals-environment-id", "env_fixture")
	headers.Set("x-portals-port", "22")
	return headers
}

// TestHTTP2AdmitsConnectAndForwardsLikeRust mirrors Rust's
// `forwards_upload_before_response_and_rejects_before_upload` success half: the
// CONNECT carries the Portals headers, and 192 KiB travels both ways.
func TestHTTP2AdmitsConnectAndForwardsLikeRust(t *testing.T) {
	recorder := &connectRecorder{}
	gateway := startHTTP2Gateway(t, connectEchoHandler(recorder, 0))
	defer gateway.close()
	target := targetForProvider(t, gateway.address)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	connection, err := connectH2(ctx, target, clientRoots(gateway.roots))
	if err != nil {
		t.Fatalf("connectH2() error = %v", err)
	}
	defer connection.close()
	tunnel, err := connection.admit(ctx, requestSpec{headers: portalHeaders(), bearer: "Bearer fixture"})
	if err != nil {
		t.Fatalf("admit() error = %v", err)
	}

	upload := bytes.Repeat([]byte{42}, 192*1024)
	var download bytes.Buffer
	relayDone := make(chan error, 1)
	go func() { relayDone <- tunnel.relay(bytes.NewReader(upload), &download) }()
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatalf("relay() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("relay() did not finish")
	}
	if !bytes.Equal(download.Bytes(), upload) {
		t.Fatalf("download length = %d, want %d", download.Len(), len(upload))
	}
	method, host, header, reads := recorder.snapshot()
	if method != http.MethodConnect || host != "127.0.0.1:22" {
		t.Fatalf("CONNECT = %s %s", method, host)
	}
	if got := header.Get("Authorization"); got != "Bearer fixture" {
		t.Fatalf("authorization = %q", got)
	}
	if got := header.Get("x-portals-environment-id"); got != "env_fixture" {
		t.Fatalf("environment header = %q", got)
	}
	if reads != len(upload) {
		t.Fatalf("gateway read %d bytes, want %d", reads, len(upload))
	}
}

// TestHTTP2RejectsBeforeUploadLikeRust keeps the upload off a rejected stream.
func TestHTTP2RejectsBeforeUploadLikeRust(t *testing.T) {
	recorder := &connectRecorder{}
	gateway := startHTTP2Gateway(t, connectEchoHandler(recorder, http.StatusForbidden))
	defer gateway.close()
	target := targetForProvider(t, gateway.address)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connection, err := connectH2(ctx, target, clientRoots(gateway.roots))
	if err != nil {
		t.Fatalf("connectH2() error = %v", err)
	}
	defer connection.close()
	_, err = connection.admit(ctx, requestSpec{headers: portalHeaders(), bearer: "Bearer fixture"})
	if err == nil || !isRejection(err) {
		t.Fatalf("admit() error = %v, want a rejection", err)
	}
	if got := err.Error(); got != "CONNECT rejected with status 403" {
		t.Fatalf("admit() error = %q", got)
	}
	time.Sleep(100 * time.Millisecond)
	_, _, _, reads := recorder.snapshot()
	if reads != 0 {
		t.Fatalf("gateway read %d upload bytes on a rejected CONNECT", reads)
	}
}

// TestHTTP2CertificateFailureIsNamedLikeRust reports an untrusted gateway as a
// certificate failure rather than a generic connection failure.
func TestHTTP2CertificateFailureIsNamedLikeRust(t *testing.T) {
	gateway := startHTTP2Gateway(t, connectEchoHandler(nil, 0))
	defer gateway.close()
	_, otherRoots := selfSignedCertificate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := connectH2(ctx, targetForProvider(t, gateway.address), clientRoots(otherRoots))
	if err == nil || err.Error() != "HTTP/2 proxy certificate verification failed" {
		t.Fatalf("connectH2() error = %v", err)
	}
}

// TestProxyOnlyEgressKeepsCredentialsInsideTLSLikeRust mirrors Rust's
// `proxy_only_egress_keeps_gateway_credentials_inside_tls`.
func TestProxyOnlyEgressKeepsCredentialsInsideTLSLikeRust(t *testing.T) {
	recorder := &connectRecorder{}
	gateway := startHTTP2Gateway(t, connectEchoHandler(recorder, 0))
	defer gateway.close()

	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer proxyListener.Close()
	proxySaw := make(chan string, 1)
	go func() {
		socket, err := proxyListener.Accept()
		if err != nil {
			return
		}
		defer socket.Close()
		reader := bufio.NewReader(socket)
		header := ""
		for !strings.HasSuffix(header, "\r\n\r\n") {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			header += line
		}
		proxySaw <- header
		upstream, err := net.Dial("tcp", gateway.address)
		if err != nil {
			return
		}
		defer upstream.Close()
		if _, err := io.WriteString(socket, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{}, 2)
		go func() {
			_, _ = io.Copy(upstream, reader)
			done <- struct{}{}
		}()
		go func() {
			_, _ = io.Copy(socket, upstream)
			done <- struct{}{}
		}()
		<-done
		<-done
	}()

	proxyURL, err := url.Parse("http://" + proxyListener.Addr().String())
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	target := targetForProvider(t, gateway.address)
	connection, err := connectThroughProxy(ctx, target, clientRoots(gateway.roots), &proxyIntercept{uri: proxyURL})
	if err != nil {
		t.Fatalf("connectThroughProxy() error = %v", err)
	}
	defer connection.close()
	tunnel, err := connection.admit(ctx, requestSpec{headers: portalHeaders(), bearer: "Bearer fixture"})
	if err != nil {
		t.Fatalf("admit() error = %v", err)
	}
	payload := bytes.Repeat([]byte{7}, 32*1024)
	var echo bytes.Buffer
	relayDone := make(chan error, 1)
	go func() { relayDone <- tunnel.relay(bytes.NewReader(payload), &echo) }()
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatalf("relay() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("relay() did not finish")
	}
	if !bytes.Equal(echo.Bytes(), payload) {
		t.Fatalf("echo length = %d, want %d", echo.Len(), len(payload))
	}
	select {
	case header := <-proxySaw:
		lower := strings.ToLower(header)
		if !strings.HasPrefix(header, "CONNECT "+gateway.address+" HTTP/1.1") {
			t.Fatalf("proxy CONNECT line = %q", strings.SplitN(header, "\r\n", 2)[0])
		}
		if strings.Contains(lower, "authorization: bearer") {
			t.Fatalf("proxy saw the gateway credential: %q", header)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("proxy never received the CONNECT")
	}
}

// TestSystemProxySelectionLikeRust pins the environment precedence and the
// schemes this tunnel can speak.
func TestSystemProxySelectionLikeRust(t *testing.T) {
	target := &ProxyTarget{Host: "127.0.0.1", Port: 8443, Authority: "127.0.0.1:22"}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(name, "")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	intercept, err := systemProxy(target)
	if err != nil || intercept == nil || intercept.uri.Host != "127.0.0.1:9" {
		t.Fatalf("systemProxy(HTTPS_PROXY) = %#v, %v", intercept, err)
	}
	t.Setenv("NO_PROXY", "127.0.0.1")
	if intercept, err := systemProxy(target); err != nil || intercept != nil {
		t.Fatalf("systemProxy(NO_PROXY) = %#v, %v", intercept, err)
	}

	t.Setenv("NO_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "127.0.0.1:9")
	intercept, err = systemProxy(target)
	if err != nil || intercept == nil || intercept.uri.Scheme != "http" {
		t.Fatalf("systemProxy(ALL_PROXY) = %#v, %v", intercept, err)
	}
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")
	if intercept, err := systemProxy(target); err != nil || intercept != nil {
		t.Fatalf("systemProxy(socks5) = %#v, %v", intercept, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("REQUEST_METHOD", "GET")
	if intercept, err := systemProxy(target); err != nil || intercept != nil {
		t.Fatalf("systemProxy(CGI) = %#v, %v", intercept, err)
	}
}
