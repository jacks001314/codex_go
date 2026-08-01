package network

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/elazarl/goproxy"
	vhost "github.com/inconshreveable/go-vhost"
	socks5 "github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
	"golang.org/x/net/http2"
	"inet.af/tcpproxy"
)

type ProxyServer struct {
	ctx           context.Context
	httpListener  net.Listener
	socksListener net.Listener
	httpServer    *http.Server
	httpProxy     *goproxy.ProxyHttpServer
	mitm          *proxyMITMRuntime
	policy        atomic.Pointer[proxyRuntimePolicy]
	policyDecider ProxyPolicyDecider
	blocked       ProxyBlockedRequestObserver
	blockedMu     sync.Mutex
	blockedEvents []ProxyBlockedRequest
	blockedTotal  uint64
	auditSink     ProxyPolicyAuditSink
	auditMetadata ProxyAuditMetadata
	auditProvider ProxyAuditMetadataProvider
	environmentID string
	cancel        context.CancelFunc
	wait          sync.WaitGroup
	closeOnce     sync.Once
}

type proxyRuntimePolicy struct {
	settings     ProxySettings
	allowMatcher *ProxyDomainMatcher
	denyMatcher  *ProxyDomainMatcher
	mitmHooks    ProxyMITMHooksByHost
	broker       *ProxyCredentialBroker
}

func StartProxyManagedNetwork(ctx context.Context, config ProxyConfig, baseEnv map[string]string) (*PreparedProxyManagedNetwork, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeConfig, err := ResolveProxyRuntime(config)
	if err != nil {
		return nil, err
	}
	server, err := startProxyServer(ctx, config, runtimeConfig, baseEnv)
	if err != nil {
		return nil, err
	}
	httpAddr := *server.httpListener.Addr().(*net.TCPAddr)
	socksAddr := httpAddr
	if server.socksListener != nil {
		socksAddr = *server.socksListener.Addr().(*net.TCPAddr)
	}
	prepared := PrepareProxyManagedNetwork(baseEnv, httpAddr, socksAddr, server.socksListener != nil, config.Network.AllowLocalBinding)
	server.mitm.ApplyChildEnv(prepared.Env)
	server.runtimePolicy().broker.VirtualizeChildEnv(prepared.Env)
	prepared.server = server
	prepared.baseEnv = cloneProxyEnv(baseEnv)
	prepared.config = config
	prepared.httpAddr = httpAddr
	prepared.socksAddr = socksAddr
	prepared.socksEnabled = server.socksListener != nil
	prepared.environments = map[string]*PreparedProxyManagedNetwork{}
	return &prepared, nil
}

func cloneProxyEnv(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func startProxyServer(parent context.Context, config ProxyConfig, runtimeConfig ProxyRuntimeConfig, baseEnv map[string]string) (*ProxyServer, error) {
	settings := config.Network
	ctx, cancel := context.WithCancel(parent)
	policy, err := buildProxyRuntimePolicy(settings)
	if err != nil {
		cancel()
		return nil, err
	}
	var mitmRuntime *proxyMITMRuntime
	if settings.MITM {
		mitmRuntime, err = newProxyMITMRuntime(baseEnv)
		if err != nil {
			cancel()
			return nil, err
		}
	}
	httpListener, err := net.ListenTCP("tcp", &runtimeConfig.HTTPAddr)
	if err != nil {
		if mitmRuntime != nil {
			mitmRuntime.Close()
		}
		cancel()
		return nil, fmt.Errorf("start HTTP network proxy: %w", err)
	}
	server := &ProxyServer{
		ctx:           ctx,
		httpListener:  httpListener,
		cancel:        cancel,
		mitm:          mitmRuntime,
		policyDecider: config.PolicyDecider,
		blocked:       config.BlockedObserver,
		auditSink:     config.AuditSink,
		auditMetadata: config.AuditMetadata,
		auditProvider: config.AuditMetadataProvider,
		environmentID: config.EnvironmentID,
	}
	server.policy.Store(policy)
	server.httpProxy, err = server.newHTTPProxy(baseEnv)
	if err != nil {
		_ = httpListener.Close()
		if mitmRuntime != nil {
			mitmRuntime.Close()
		}
		cancel()
		return nil, err
	}
	server.httpServer = &http.Server{
		Handler:           http.HandlerFunc(server.serveHTTPProxy),
		ReadHeaderTimeout: 30 * time.Second,
		ConnContext:       proxyHTTPConnContext,
	}
	if settings.EnableSocks5 {
		socksListener, listenErr := net.ListenTCP("tcp", &runtimeConfig.SocksAddr)
		if listenErr != nil {
			_ = httpListener.Close()
			if mitmRuntime != nil {
				mitmRuntime.Close()
			}
			cancel()
			return nil, fmt.Errorf("start SOCKS5 network proxy: %w", listenErr)
		}
		server.socksListener = socksListener
	}
	server.wait.Add(1)
	go func() {
		defer server.wait.Done()
		_ = server.httpServer.Serve(proxyHTTPValidationListener{Listener: server.httpListener})
	}()
	if server.socksListener != nil {
		server.wait.Add(1)
		go func() {
			defer server.wait.Done()
			server.serveSOCKS5()
		}()
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server, nil
}

func (s *ProxyServer) runtimePolicy() *proxyRuntimePolicy {
	if s == nil {
		return &proxyRuntimePolicy{broker: NewProxyCredentialBroker(false)}
	}
	if policy := s.policy.Load(); policy != nil {
		return policy
	}
	return &proxyRuntimePolicy{broker: NewProxyCredentialBroker(false)}
}

func (s *ProxyServer) ReloadConfig(config ProxyConfig) error {
	if s == nil {
		return fmt.Errorf("network proxy is unavailable")
	}
	if _, err := ResolveProxyRuntime(config); err != nil {
		return err
	}
	settings := config.Network
	if settings.EnableSocks5 != (s.socksListener != nil) {
		return fmt.Errorf("changing enable_socks5 requires restarting the network proxy")
	}
	if settings.EnableSocks5UDP != s.runtimePolicy().settings.EnableSocks5UDP {
		return fmt.Errorf("changing enable_socks5_udp requires restarting the network proxy")
	}
	if settings.MITM && s.mitm == nil {
		return fmt.Errorf("enabling MITM requires restarting the network proxy")
	}
	policy, err := buildProxyRuntimePolicy(settings)
	if err != nil {
		return err
	}
	s.policy.Store(policy)
	return nil
}

func buildProxyRuntimePolicy(settings ProxySettings) (*proxyRuntimePolicy, error) {
	hooks, err := CompileProxyMITMHooks(ProxyConfig{Network: settings})
	if err != nil {
		return nil, err
	}
	allowMatcher, err := CompileProxyDomainMatcher(settings.AllowedDomains(), false)
	if err != nil {
		return nil, fmt.Errorf("compile network.allowed_domains: %w", err)
	}
	denyMatcher, err := CompileProxyDomainMatcher(settings.DeniedDomains(), true)
	if err != nil {
		return nil, fmt.Errorf("compile network.denied_domains: %w", err)
	}
	return &proxyRuntimePolicy{
		settings:     settings,
		allowMatcher: allowMatcher,
		denyMatcher:  denyMatcher,
		mitmHooks:    hooks,
		broker:       NewProxyCredentialBroker(settings.CredentialBroker),
	}, nil
}

func (s *ProxyServer) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				closeErr = err
			}
		}
		if s.socksListener != nil {
			if err := s.socksListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
				closeErr = err
			}
		}
		s.wait.Wait()
		if s.mitm != nil {
			s.mitm.Close()
		}
	})
	return closeErr
}

func (s *ProxyServer) BlockedSnapshot() []ProxyBlockedRequest {
	if s == nil {
		return nil
	}
	s.blockedMu.Lock()
	defer s.blockedMu.Unlock()
	return append([]ProxyBlockedRequest(nil), s.blockedEvents...)
}

func (s *ProxyServer) DrainBlocked() []ProxyBlockedRequest {
	if s == nil {
		return nil
	}
	s.blockedMu.Lock()
	defer s.blockedMu.Unlock()
	out := append([]ProxyBlockedRequest(nil), s.blockedEvents...)
	s.blockedEvents = nil
	return out
}

func (s *ProxyServer) BlockedTotal() uint64 {
	if s == nil {
		return 0
	}
	s.blockedMu.Lock()
	defer s.blockedMu.Unlock()
	return s.blockedTotal
}

func (s *ProxyServer) serveHTTPProxy(w http.ResponseWriter, request *http.Request) {
	if err := proxyRawRequestValidationError(request.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateAbsoluteFormHostHeader(request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if socketPath := request.Header.Get("X-Unix-Socket"); socketPath != "" {
		s.serveHTTPUnixSocket(w, request, socketPath)
		return
	}
	host, port, err := proxyRequestDestination(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	protocol := ProxyProtocolHTTP
	if request.Method == http.MethodConnect {
		protocol = ProxyProtocolHTTPSConnect
	}
	if reason := s.blockReasonFor(request.Context(), request.Method, protocol, host, port, request.RemoteAddr); reason != "" {
		writeProxyBlockedResponse(w, reason)
		return
	}
	if request.Method == http.MethodConnect {
		if reason := s.connectBlockReason(host, port); reason != "" {
			writeProxyBlockedResponse(w, reason)
			return
		}
	} else {
		request = request.WithContext(context.WithValue(request.Context(), proxyHTTPPolicyContextKey{}, proxyHTTPPolicyContext{host: host, port: port}))
		// The raw-host validation state belongs to the first request on this connection.
		// Closing plain HTTP connections prevents a later request from bypassing that check.
		request.Close = true
	}
	s.httpProxy.ServeHTTP(w, request)
}

func (s *ProxyServer) serveHTTPUnixSocket(w http.ResponseWriter, request *http.Request, socketPath string) {
	policy := s.runtimePolicy()
	if !utf8.ValidString(socketPath) {
		http.Error(w, "invalid x-unix-socket header", http.StatusBadRequest)
		return
	}
	if !policy.settings.Enabled {
		writeProxyBlockedResponse(w, ProxyReasonProxyDisabled)
		return
	}
	if !(&policy.settings.Mode).AllowsMethod(request.Method) {
		writeProxyBlockedResponse(w, ProxyReasonMethodNotAllowed)
		return
	}
	if !proxyUnixSocketSupported() {
		http.Error(w, "unix sockets unsupported", http.StatusNotImplemented)
		return
	}
	allowed := policy.settings.DangerouslyAllowAllUnixSockets
	if !allowed {
		for _, path := range policy.settings.AllowUnixSockets() {
			if path == socketPath {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		writeProxyBlockedResponse(w, ProxyReasonNotAllowed)
		return
	}
	outbound := request.Clone(request.Context())
	outbound.RequestURI = ""
	outbound.URL.Scheme = "http"
	if outbound.URL.Host == "" {
		outbound.URL.Host = "unix-socket"
	}
	outbound.Header.Del("X-Unix-Socket")
	removeProxyHopByHopRequestHeaders(outbound.Header)
	response, err := proxyUnixSocketRoundTrip(outbound, socketPath)
	if err != nil {
		http.Error(w, "unix socket proxy failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (s *ProxyServer) newHTTPProxy(baseEnv map[string]string) (*goproxy.ProxyHttpServer, error) {
	rootCAs, err := proxyUpstreamRootCAs(baseEnv)
	if err != nil {
		return nil, err
	}
	proxy := goproxy.NewProxyHttpServer()
	upstreamProxy := proxyUpstreamFunc(true, baseEnv)
	proxy.Tr = &http.Transport{
		Proxy: func(request *http.Request) (*url.URL, error) {
			if !s.runtimePolicy().settings.AllowUpstreamProxy || proxyRequestTargetsNonPublic(request) {
				return nil, nil
			}
			return upstreamProxy(request)
		},
		DialContext:         s.dialCheckedTarget,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 30 * time.Second,
	}
	var upstreamConnectDial func(network, address string) (net.Conn, error)
	if upstream := ProxyAddressForConnect(baseEnv); upstream != nil {
		upstreamConnectDial = proxy.NewConnectDialToProxy(upstream.Raw)
	}
	proxy.ConnectDialWithReq = func(request *http.Request, networkName, address string) (net.Conn, error) {
		if s.runtimePolicy().settings.AllowUpstreamProxy && upstreamConnectDial != nil && !proxyAddressTargetsNonPublic(address) {
			return upstreamConnectDial(networkName, address)
		}
		return s.dialCheckedTarget(request.Context(), networkName, address)
	}
	proxy.OnRequest().HandleConnectFunc(s.handleHTTPConnect)
	proxy.OnRequest().DoFunc(s.handleHTTPRequest)
	return proxy, nil
}

func proxyRequestTargetsNonPublic(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	return proxyAddressTargetsNonPublic(request.URL.Host)
}

func proxyAddressTargetsNonPublic(address string) bool {
	host := address
	if parsed, _, err := net.SplitHostPort(address); err == nil {
		host = parsed
	}
	normalized := NormalizeProxyHost(host)
	parsed, _ := ParseProxyHost(normalized)
	if IsLoopbackProxyHost(parsed) || IsNonPublicProxyIP(proxyIPLiteral(normalized)) {
		return true
	}
	return false
}

func proxyUpstreamRootCAs(env map[string]string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	paths := map[string]bool{}
	for _, path := range ProxyStartupCAFileEnvValues(env) {
		paths[path] = true
	}
	if value := env[ProxySSLCertDirEnvKey]; value != "" {
		for _, directory := range filepath.SplitList(value) {
			entries, readErr := os.ReadDir(directory)
			if readErr != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					paths[filepath.Join(directory, entry.Name())] = true
				}
			}
		}
	}
	for path := range paths {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read startup CA bundle %s: %w", path, readErr)
		}
		if !pool.AppendCertsFromPEM(contents) {
			return nil, fmt.Errorf("startup CA bundle %s did not contain a certificate", path)
		}
	}
	return pool, nil
}

type proxyMITMRequestContext struct {
	host string
	port uint16
}

type proxyMITMContextKey struct{}

type proxyHTTPPolicyContext struct {
	host string
	port uint16
}

type proxyHTTPPolicyContextKey struct{}

func (s *ProxyServer) handleHTTPConnect(hostPort string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
	parts, err := ParseProxyHostPort(hostPort, 443)
	if err != nil {
		return goproxy.OkConnect, hostPort
	}
	mode := s.httpMITMMode(parts.Host)
	if mode == proxySOCKS5MITMDisabled {
		return goproxy.OkConnect, hostPort
	}
	if mode == proxySOCKS5MITMDetectTLS {
		return &goproxy.ConnectAction{
			Action: goproxy.ConnectHijack,
			Hijack: func(request *http.Request, client net.Conn, _ *goproxy.ProxyCtx) {
				s.handleHTTPDetectTLS(request, client, parts.Host, parts.Port)
			},
		}, hostPort
	}
	return &goproxy.ConnectAction{
		Action: goproxy.ConnectHijack,
		Hijack: func(_ *http.Request, client net.Conn, _ *goproxy.ProxyCtx) {
			s.handleHTTPMITM(client, parts.Host, parts.Port)
		},
	}, hostPort
}

func (s *ProxyServer) httpMITMMode(host string) proxySOCKS5MITMMode {
	policy := s.runtimePolicy()
	normalized := NormalizeProxyHost(host)
	if policy.settings.Mode == ProxyModeLimited || len(policy.mitmHooks[normalized]) > 0 {
		return proxySOCKS5MITMRequired
	}
	if policy.broker.HostRequiresMITM(normalized) {
		return proxySOCKS5MITMDetectTLS
	}
	return proxySOCKS5MITMDisabled
}

func (s *ProxyServer) handleHTTPDetectTLS(_ *http.Request, client net.Conn, host string, port uint16) {
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	first, err := reader.Peek(1)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil || first[0] != 0x16 {
		s.proxyOpaqueTCP(client, reader, host, port)
		return
	}
	prefix, err := reader.Peek(3)
	if err != nil || prefix[1] != 0x03 || prefix[2] > 0x04 {
		s.proxyOpaqueTCP(client, reader, host, port)
		return
	}
	parsed, err := vhost.TLS(&proxyBufferedConn{Conn: client, reader: reader})
	if err != nil {
		return
	}
	_ = s.serveMITMHTTP(parsed, host, port)
}

func (s *ProxyServer) handleHTTPMITM(client net.Conn, host string, port uint16) {
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = s.serveMITMHTTP(client, host, port)
}

func (s *ProxyServer) handleHTTPRequest(request *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	policy := s.runtimePolicy()
	host := proxyHTTPDestinationHost(request)
	requestPort := uint16(80)
	policyChecked := false
	isMITM := false
	var mitmMetadata proxyMITMRequestContext
	if metadata, ok := ctx.UserData.(proxyMITMRequestContext); ok {
		isMITM = true
		mitmMetadata = metadata
		requestPort = metadata.port
		if metadata.host != "" {
			host = metadata.host
		}
	}
	if metadata, ok := request.Context().Value(proxyMITMContextKey{}).(proxyMITMRequestContext); ok {
		isMITM = true
		mitmMetadata = metadata
		requestPort = metadata.port
		if metadata.host != "" {
			host = metadata.host
		}
	}
	if metadata, ok := request.Context().Value(proxyHTTPPolicyContextKey{}).(proxyHTTPPolicyContext); ok {
		policyChecked = true
		requestPort = metadata.port
		if metadata.host != "" {
			host = metadata.host
		}
	}
	if isMITM && !proxyRequestHostMatchesTarget(request.Host, mitmMetadata.host, mitmMetadata.port) {
		return request, proxyBadRequestResponse(request, "host mismatch")
	}
	if !policyChecked {
		if reason := s.blockReasonFor(request.Context(), request.Method, ProxyProtocolHTTP, host, requestPort, request.RemoteAddr); reason != "" {
			return request, proxyBlockedHTTPResponse(request, reason)
		}
	}
	if isMITM {
		evaluation := EvaluateProxyMITMHooks(policy.mitmHooks, host, NewProxyHTTPRequestFromHTTP(request))
		switch evaluation.Kind {
		case ProxyHookEvaluationHookedNoMatch:
			return request, proxyBlockedHTTPResponse(request, ProxyReasonMITMHookDenied)
		case ProxyHookEvaluationMatched:
			applyProxyMITMHookActions(request.Header, evaluation.Actions)
		}
	}
	if isMITM || policy.settings.DangerouslyAllowPlaintextCredentialInjection {
		policy.broker.InjectRequestHeaders(host, request.Header)
	}
	removeProxyHopByHopRequestHeaders(request.Header)
	return request, nil
}

func (s *ProxyServer) connectBlockReason(host string, port uint16) string {
	_ = port
	if s.shouldMITM(host) && s.mitm == nil {
		return ProxyReasonMITMRequired
	}
	return ""
}

func (s *ProxyServer) shouldMITM(host string) bool {
	policy := s.runtimePolicy()
	normalized := NormalizeProxyHost(host)
	return policy.settings.Mode == ProxyModeLimited || len(policy.mitmHooks[normalized]) > 0 || policy.broker.HostRequiresMITM(normalized)
}

func proxyHTTPDestinationHost(request *http.Request) string {
	if request == nil {
		return ""
	}
	if request.URL != nil && request.URL.Hostname() != "" {
		return request.URL.Hostname()
	}
	parts, err := ParseProxyHostPort(request.Host, 80)
	if err != nil {
		return request.Host
	}
	return parts.Host
}

func proxyBlockedHTTPResponse(request *http.Request, reason string) *http.Response {
	blocked := ProxyBlockedTextResponse(reason)
	response := goproxy.NewResponse(request, goproxy.ContentTypeText, blocked.Status, blocked.Body)
	for key, value := range blocked.Headers {
		response.Header.Set(key, value)
	}
	return response
}

func proxyBadRequestResponse(request *http.Request, message string) *http.Response {
	return goproxy.NewResponse(request, goproxy.ContentTypeText, http.StatusBadRequest, message)
}

func applyProxyMITMHookActions(headers http.Header, actions ProxyMITMHookActions) {
	for _, name := range actions.StripRequestHeaders {
		headers.Del(name)
	}
	for _, header := range actions.InjectRequestHeaders {
		headers.Set(header.Name, header.Value)
	}
}

func removeProxyHopByHopRequestHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if name := strings.TrimSpace(token); name != "" {
				headers.Del(name)
			}
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authorization", "Trailer", "Transfer-Encoding", "Upgrade", "Te"} {
		headers.Del(name)
	}
}

func validateAbsoluteFormHostHeader(request *http.Request) error {
	if request == nil || request.Method == http.MethodConnect || request.URL == nil || request.URL.Scheme == "" || request.Host == "" {
		return nil
	}
	return validateAbsoluteFormTarget(request.URL.String(), request.Host)
}

func validateAbsoluteFormTarget(rawURI string, rawHost string) error {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || rawHost == "" {
		return err
	}
	defaultPort := uint16(80)
	if strings.EqualFold(parsed.Scheme, "https") {
		defaultPort = 443
	}
	target, err := ParseProxyHostPort(parsed.Host, defaultPort)
	if err != nil {
		return err
	}
	header, err := ParseProxyHostPort(rawHost, defaultPort)
	if err != nil {
		return fmt.Errorf("invalid Host header")
	}
	if NormalizeProxyHost(header.Host) != NormalizeProxyHost(target.Host) || header.Port != target.Port {
		return fmt.Errorf("Host header does not match request target")
	}
	if !hostHasExplicitPort(rawHost) && !isDefaultProxyPort(parsed.Scheme, target.Port) {
		return fmt.Errorf("Host header does not match request target")
	}
	return nil
}

func proxyRequestHostMatchesTarget(requestHost string, targetHost string, targetPort uint16) bool {
	if requestHost == "" {
		return true
	}
	parts, err := ParseProxyHostPort(requestHost, 443)
	if err != nil {
		return false
	}
	return NormalizeProxyHost(parts.Host) == NormalizeProxyHost(targetHost) && parts.Port == targetPort
}

func hostHasExplicitPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		_, port, err := net.SplitHostPort(host)
		return err == nil && port != ""
	}
	_, port, err := net.SplitHostPort(host)
	return err == nil && port != ""
}

func isDefaultProxyPort(scheme string, port uint16) bool {
	return strings.EqualFold(scheme, "http") && port == 80 || strings.EqualFold(scheme, "https") && port == 443
}

func proxyUpstreamFunc(enabled bool, env map[string]string) func(*http.Request) (*url.URL, error) {
	if !enabled {
		return nil
	}
	config := ProxyUpstreamConfigFromEnv(env)
	return func(request *http.Request) (*url.URL, error) {
		secure := request != nil && request.URL != nil && request.URL.Scheme == "https"
		address := (&config).ProxyForProtocol(secure)
		if address == nil {
			return nil, nil
		}
		value := address.Raw
		if value != "" && !hasURLScheme(value) {
			value = "http://" + value
		}
		return url.Parse(value)
	}
}

func hasURLScheme(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != ""
}

func (s *ProxyServer) serveSOCKS5() {
	policy := s.runtimePolicy()
	options := []socks5.Option{
		socks5.WithRule(proxySOCKS5Rule{server: s}),
		socks5.WithResolver(proxySOCKS5Resolver{}),
		socks5.WithDial(s.dialSOCKS5Target),
		socks5.WithConnectMiddleware(s.handleSOCKS5MITM),
	}
	if !policy.settings.EnableSocks5UDP {
		options = append(options, socks5.WithAssociateHandle(func(_ context.Context, writer io.Writer, _ *socks5.Request) error {
			return socks5.SendReply(writer, statute.RepCommandNotSupported, nil)
		}))
	}
	_ = socks5.NewServer(options...).Serve(s.socksListener)
}

type proxySOCKS5Resolver struct{}

func (proxySOCKS5Resolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	// Keep the original FQDN through policy and MITM selection. The outbound dialer
	// resolves direct TCP/UDP targets only after those checks have passed.
	return ctx, nil, nil
}

type proxySOCKS5Rule struct {
	server *ProxyServer
}

func (r proxySOCKS5Rule) Allow(ctx context.Context, request *socks5.Request) (context.Context, bool) {
	if r.server == nil || request == nil {
		return ctx, false
	}
	switch request.Command {
	case statute.CommandConnect:
		policy := r.server.runtimePolicy()
		host, port := socks5Destination(request.RawDestAddr)
		if policy.settings.Mode == ProxyModeLimited && port != 443 {
			return ctx, false
		}
		if len(policy.mitmHooks[NormalizeProxyHost(host)]) > 0 && port != 443 {
			return ctx, false
		}
		if r.server.shouldMITM(host) && r.server.mitm == nil {
			return ctx, false
		}
		return ctx, r.server.blockReasonFor(ctx, "CONNECT", ProxyProtocolSocks5TCP, host, port, proxyAddrString(request.RemoteAddr)) == ""
	case statute.CommandAssociate:
		// When UDP is disabled the command handler returns CommandNotSupported. In limited
		// mode Rust still accepts ASSOCIATE and rejects every relayed datagram.
		return ctx, true
	default:
		return ctx, false
	}
}

var errSOCKS5MITMHandled = errors.New("SOCKS5 MITM connection handled")

func (s *ProxyServer) handleSOCKS5MITM(_ context.Context, writer io.Writer, request *socks5.Request) error {
	host, port := socks5Destination(request.RawDestAddr)
	mode := s.socks5MITMMode(host)
	if mode == proxySOCKS5MITMDisabled {
		return nil
	}
	client, ok := writer.(net.Conn)
	if !ok || s.mitm == nil {
		return fmt.Errorf("SOCKS5 MITM unavailable")
	}
	if err := socks5.SendReply(client, statute.RepSuccess, client.LocalAddr()); err != nil {
		return err
	}
	var source net.Conn = &proxyBufferedConn{Conn: client, reader: request.Reader}
	if mode == proxySOCKS5MITMDetectTLS {
		reader := bufio.NewReader(source)
		_ = source.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		first, err := reader.Peek(1)
		_ = source.SetReadDeadline(time.Time{})
		if err != nil || first[0] != 0x16 {
			s.proxySOCKS5Opaque(client, reader, host, port)
			return errSOCKS5MITMHandled
		}
		prefix, err := reader.Peek(3)
		if err != nil || prefix[1] != 0x03 || prefix[2] > 0x04 {
			s.proxySOCKS5Opaque(client, reader, host, port)
			return errSOCKS5MITMHandled
		}
		parsed, err := vhost.TLS(&proxyBufferedConn{Conn: source, reader: reader})
		if err != nil {
			return fmt.Errorf("parse SOCKS5 TLS ClientHello: %w", err)
		}
		source = parsed
	}
	err := s.serveMITMHTTP(source, host, port)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return errSOCKS5MITMHandled
}

type proxySOCKS5MITMMode uint8

const (
	proxySOCKS5MITMDisabled proxySOCKS5MITMMode = iota
	proxySOCKS5MITMRequired
	proxySOCKS5MITMDetectTLS
)

func (s *ProxyServer) socks5MITMMode(host string) proxySOCKS5MITMMode {
	policy := s.runtimePolicy()
	normalized := NormalizeProxyHost(host)
	if policy.settings.Mode == ProxyModeLimited || len(policy.mitmHooks[normalized]) > 0 {
		return proxySOCKS5MITMRequired
	}
	if policy.broker.HostRequiresMITM(normalized) {
		return proxySOCKS5MITMDetectTLS
	}
	return proxySOCKS5MITMDisabled
}

func (s *ProxyServer) proxySOCKS5Opaque(client net.Conn, reader io.Reader, host string, port uint16) {
	s.proxyOpaqueTCP(client, reader, host, port)
}

func (s *ProxyServer) proxyOpaqueTCP(client net.Conn, reader io.Reader, host string, port uint16) {
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	proxy := tcpproxy.To(target)
	proxy.DialTimeout = 30 * time.Second
	proxy.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return s.dialCheckedTarget(ctx, network, address)
	}
	var peeked []byte
	if buffered, ok := reader.(*bufio.Reader); ok && buffered.Buffered() > 0 {
		peeked = make([]byte, buffered.Buffered())
		_, _ = io.ReadFull(buffered, peeked)
	}
	if validating, ok := client.(*proxyHTTPValidationConn); ok {
		client = validating.Conn
	}
	proxy.HandleConn(&tcpproxy.Conn{Conn: client, Peeked: peeked})
}

func (s *ProxyServer) serveMITMHTTP(source net.Conn, host string, port uint16) error {
	tlsConfig := s.mitm.config.TLSForHost(host).Clone()
	tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	tlsClient := tls.Server(source, tlsConfig)
	if err := tlsClient.HandshakeContext(s.ctx); err != nil {
		return fmt.Errorf("MITM TLS handshake: %w", err)
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	handler := http.HandlerFunc(func(w http.ResponseWriter, inner *http.Request) {
		metadata := proxyMITMRequestContext{host: NormalizeProxyHost(host), port: port}
		outbound := inner.Clone(context.WithValue(inner.Context(), proxyMITMContextKey{}, metadata))
		outbound.URL.Scheme = "https"
		outbound.URL.Host = target
		s.httpProxy.ServeHTTP(w, outbound)
	})
	listener := newProxySingleConnListener(tlsClient)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second}
	if tlsClient.ConnectionState().NegotiatedProtocol == "h2" {
		(&http2.Server{}).ServeConn(tlsClient, &http2.ServeConnOpts{Context: s.ctx, BaseConfig: server, Handler: handler})
		return nil
	}
	return server.Serve(listener)
}

type proxyBufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *proxyBufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}

type proxySingleConnListener struct {
	conn      net.Conn
	accepted  bool
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
}

func newProxySingleConnListener(conn net.Conn) *proxySingleConnListener {
	return &proxySingleConnListener{conn: conn, done: make(chan struct{})}
}

func (l *proxySingleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		conn := &proxyCloseNotifyConn{Conn: l.conn, notify: l.signalDone}
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, io.EOF
}

func (l *proxySingleConnListener) Close() error {
	l.signalDone()
	if l.conn == nil {
		return nil
	}
	return l.conn.Close()
}

func (l *proxySingleConnListener) Addr() net.Addr {
	if l.conn == nil {
		return &net.TCPAddr{}
	}
	return l.conn.LocalAddr()
}

func (l *proxySingleConnListener) signalDone() {
	l.closeOnce.Do(func() { close(l.done) })
}

type proxyCloseNotifyConn struct {
	net.Conn
	notify func()
}

func (c *proxyCloseNotifyConn) Close() error {
	c.notify()
	return c.Conn.Close()
}

func (s *ProxyServer) dialSOCKS5Target(ctx context.Context, network string, address string) (net.Conn, error) {
	if s.ctx != nil {
		ctx = s.ctx
	}
	if network == "udp" {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		port, err := net.LookupPort("udp", portText)
		if err != nil {
			return nil, err
		}
		conn, err := s.dialCheckedTarget(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &proxyPolicyUDPConn{Conn: conn, server: s, host: host, port: uint16(port)}, nil
	}
	return s.dialCheckedTarget(ctx, network, address)
}

type proxyPolicyUDPConn struct {
	net.Conn
	server *ProxyServer
	host   string
	port   uint16
}

func (c *proxyPolicyUDPConn) Write(payload []byte) (int, error) {
	if c.server == nil {
		return 0, fmt.Errorf("SOCKS5 UDP policy unavailable")
	}
	request := ProxyPolicyRequest{
		Protocol:      ProxyProtocolSocks5UDP,
		Host:          NormalizeProxyHost(c.host),
		Port:          c.port,
		EnvironmentID: c.server.environmentID,
		Method:        "ASSOCIATE",
	}
	if c.server.runtimePolicy().settings.Mode == ProxyModeLimited {
		decision := DenyProxyDecisionWithSource(ProxyReasonMethodNotAllowed, ProxyDecisionSourceModeGuard)
		c.server.emitProxyPolicyAudit(request, decision, false)
		return 0, fmt.Errorf("SOCKS5 UDP blocked: %s", decision.Reason)
	}
	decision := c.server.evaluateProxyPolicy(context.Background(), request)
	if !decision.Allow {
		return 0, fmt.Errorf("SOCKS5 UDP blocked: %s", decision.Reason)
	}
	return c.Conn.Write(payload)
}

func (s *ProxyServer) dialCheckedTarget(ctx context.Context, network string, address string) (net.Conn, error) {
	settings := s.runtimePolicy().settings
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if ip := proxyIPLiteral(host); ip != nil {
		if !settings.AllowLocalBinding && IsNonPublicProxyIP(ip) {
			return nil, fmt.Errorf("network target rejected by policy")
		}
		return dialer.DialContext(ctx, network, address)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, resolved := range addresses {
		if !settings.AllowLocalBinding && IsNonPublicProxyIP(resolved.IP) {
			lastErr = fmt.Errorf("network target rejected by policy")
			continue
		}
		resolvedHost := resolved.IP.String()
		if resolved.Zone != "" {
			resolvedHost += "%" + resolved.Zone
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolvedHost, port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses resolved for %s", host)
	}
	return nil, lastErr
}

func socks5Destination(address *statute.AddrSpec) (string, uint16) {
	if address == nil {
		return "", 0
	}
	host := address.FQDN
	if host == "" {
		host = address.IP.String()
	}
	return host, uint16(address.Port)
}

func (s *ProxyServer) blockReason(method string, protocol ProxyProtocol, host string, port uint16) string {
	return s.blockReasonFor(context.Background(), method, protocol, host, port, "")
}

func (s *ProxyServer) blockReasonFor(ctx context.Context, method string, protocol ProxyProtocol, host string, port uint16, clientAddr string) string {
	decision := s.evaluateProxyPolicy(ctx, ProxyPolicyRequest{
		Protocol:      protocol,
		Host:          NormalizeProxyHost(host),
		Port:          port,
		EnvironmentID: s.environmentID,
		ClientAddr:    clientAddr,
		Method:        method,
	})
	if decision.Allow {
		return ""
	}
	return decision.Reason
}

func (s *ProxyServer) evaluateProxyPolicy(ctx context.Context, request ProxyPolicyRequest) ProxyDecision {
	policy := s.runtimePolicy()
	settings := policy.settings
	deny := func(reason string, source ProxyDecisionSource) ProxyDecision {
		decision := DenyProxyDecisionWithSource(reason, source)
		s.emitProxyPolicyAudit(request, decision, false)
		s.recordBlockedRequest(ctx, request, decision, settings.Mode)
		return decision
	}
	if !settings.Enabled {
		return deny(ProxyReasonProxyDisabled, ProxyDecisionSourceProxyState)
	}
	if request.Protocol == ProxyProtocolHTTP && !(&settings.Mode).AllowsMethod(request.Method) {
		return deny(ProxyReasonMethodNotAllowed, ProxyDecisionSourceModeGuard)
	}
	denyMatcher := policy.denyMatcher
	if denyMatcher == nil {
		denyMatcher, _ = CompileProxyDomainMatcher(settings.DeniedDomains(), true)
	}
	if denyMatcher.Match(request.Host) {
		return deny(ProxyReasonDenied, ProxyDecisionSourceBaselinePolicy)
	}
	allowlist := settings.AllowedDomains()
	allowMatcher := policy.allowMatcher
	if allowMatcher == nil {
		allowMatcher, _ = CompileProxyDomainMatcher(allowlist, false)
	}
	allowed := allowMatcher.Match(request.Host)
	if !settings.AllowLocalBinding {
		normalizedHost := request.Host
		parsedHost, _ := ParseProxyHost(normalizedHost)
		if ip := proxyIPLiteral(normalizedHost); IsNonPublicProxyIP(ip) || IsLoopbackProxyHost(parsedHost) {
			if !explicitLocalProxyAllowlisted(allowlist, normalizedHost) {
				return deny(ProxyReasonNotAllowedLocal, ProxyDecisionSourceBaselinePolicy)
			}
		} else if (allowed || s.policyDecider != nil) && proxyHostResolvesToNonPublicIP(normalizedHost, request.Port) {
			return deny(ProxyReasonNotAllowedLocal, ProxyDecisionSourceBaselinePolicy)
		}
	}
	if len(allowlist) > 0 && allowed {
		decision := AllowProxyDecision()
		decision.Source = ProxyDecisionSourceBaselinePolicy
		decision.Reason = "allow"
		s.emitProxyPolicyAudit(request, decision, false)
		s.recordBlockedRequest(ctx, request, decision, settings.Mode)
		return decision
	}
	if s.policyDecider != nil {
		decision := s.policyDecider.Decide(ctx, request)
		decision.Source = ProxyDecisionSourceDecider
		if decision.Allow {
			decision.Reason = ProxyReasonNotAllowed
			s.emitProxyPolicyAudit(request, decision, true)
			return decision
		}
		if decision.Reason == "" {
			decision.Reason = ProxyReasonNotAllowed
		}
		if decision.Decision == "" {
			decision.Decision = ProxyPolicyDecisionDeny
		}
		s.emitProxyPolicyAudit(request, decision, false)
		return decision
	}
	return deny(ProxyReasonNotAllowed, ProxyDecisionSourceBaselinePolicy)
}

const maxProxyBlockedEvents = 200

func (s *ProxyServer) recordBlockedRequest(ctx context.Context, request ProxyPolicyRequest, decision ProxyDecision, mode ProxyMode) {
	if s == nil || decision.Allow {
		return
	}
	port := request.Port
	entry := ProxyBlockedRequest{
		Host:      request.Host,
		Reason:    decision.Reason,
		Client:    request.ClientAddr,
		Method:    request.Method,
		Mode:      &mode,
		Protocol:  string(request.Protocol),
		Decision:  string(decision.Decision),
		Source:    decision.Source,
		Port:      &port,
		Timestamp: time.Now().Unix(),
	}
	s.blockedMu.Lock()
	s.blockedEvents = append(s.blockedEvents, entry)
	s.blockedTotal++
	if len(s.blockedEvents) > maxProxyBlockedEvents {
		s.blockedEvents = append([]ProxyBlockedRequest(nil), s.blockedEvents[len(s.blockedEvents)-maxProxyBlockedEvents:]...)
	}
	total := s.blockedTotal
	buffered := len(s.blockedEvents)
	s.blockedMu.Unlock()
	if encoded, err := json.Marshal(entry); err == nil {
		slog.Debug("CODEX_NETWORK_POLICY_VIOLATION " + string(encoded))
	}
	slog.Debug("recorded blocked request telemetry", "total", total, "host", entry.Host, "reason", entry.Reason, "decision", entry.Decision, "source", entry.Source, "protocol", entry.Protocol, "port", port, "buffered", buffered)
	if s.blocked != nil {
		s.blocked.OnBlockedRequest(ctx, entry)
	}
}

func (s *ProxyServer) emitProxyPolicyAudit(request ProxyPolicyRequest, decision ProxyDecision, policyOverride bool) {
	if s.auditSink == nil {
		return
	}
	value := "allow"
	if !decision.Allow {
		value = string(decision.Decision)
		if value == "" {
			value = string(ProxyPolicyDecisionDeny)
		}
	}
	metadata := s.auditMetadata
	if s.auditProvider != nil {
		metadata = s.auditProvider(request)
	}
	s.auditSink(ProxyPolicyAuditEvent{
		Request:        request,
		Decision:       value,
		Source:         decision.Source,
		Reason:         decision.Reason,
		PolicyOverride: policyOverride,
		Metadata:       metadata,
	})
}

func proxyAddrString(address net.Addr) string {
	if address == nil {
		return ""
	}
	return address.String()
}

func explicitLocalProxyAllowlisted(allowlist []string, host string) bool {
	unscoped := unscopedIPLiteral(host)
	for _, pattern := range allowlist {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "*" || strings.ContainsAny(trimmed, "*?") {
			continue
		}
		normalized := NormalizeProxyHost(trimmed)
		if normalized == host || unscoped != "" && normalized == unscoped {
			return true
		}
	}
	return false
}

func proxyHostResolvesToNonPublicIP(host string, _ uint16) bool {
	if host == "" || proxyIPLiteral(host) != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return true
	}
	if len(addresses) == 0 {
		return true
	}
	for _, address := range addresses {
		if IsNonPublicProxyIP(address.IP) {
			return true
		}
	}
	return false
}

func proxyIPLiteral(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	return net.ParseIP(unscopedIPLiteral(host))
}

func proxyRequestDestination(request *http.Request) (string, uint16, error) {
	value := request.Host
	defaultPort := uint16(80)
	if request.Method == http.MethodConnect {
		value = request.RequestURI
		defaultPort = 443
	} else if request.URL != nil && request.URL.Host != "" {
		value = request.URL.Host
		if request.URL.Scheme == "https" {
			defaultPort = 443
		}
	}
	parts, err := ParseProxyHostPort(value, defaultPort)
	if err != nil {
		return "", 0, err
	}
	return parts.Host, parts.Port, nil
}

func writeProxyBlockedResponse(w http.ResponseWriter, reason string) {
	response := ProxyBlockedTextResponse(reason)
	for key, value := range response.Headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(response.Status)
	_, _ = io.WriteString(w, response.Body)
}
