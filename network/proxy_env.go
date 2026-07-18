package network

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

var ProxyURLEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "WS_PROXY", "WSS_PROXY", "ALL_PROXY", "FTP_PROXY",
	"YARN_HTTP_PROXY", "YARN_HTTPS_PROXY", "NPM_CONFIG_HTTP_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NPM_CONFIG_PROXY",
	"BUNDLE_HTTP_PROXY", "BUNDLE_HTTPS_PROXY", "PIP_PROXY", "DOCKER_HTTP_PROXY", "DOCKER_HTTPS_PROXY",
}

var AllProxyEnvKeys = []string{"ALL_PROXY", "all_proxy"}

const (
	ProxyActiveEnvKey            = "CODEX_NETWORK_PROXY_ACTIVE"
	ProxyAllowLocalBindingEnvKey = "CODEX_NETWORK_ALLOW_LOCAL_BINDING"
	ProxyDefaultNoProxyValue     = "localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
)

var NoProxyEnvKeys = []string{"NO_PROXY", "no_proxy", "npm_config_noproxy", "NPM_CONFIG_NOPROXY", "YARN_NO_PROXY", "BUNDLE_NO_PROXY"}

func ProxyURLEnvValue(env map[string]string, canonicalKey string) (string, bool) {
	if value, ok := env[canonicalKey]; ok {
		return value, true
	}
	value, ok := env[strings.ToLower(canonicalKey)]
	return value, ok
}

func HasProxyURLEnvVars(env map[string]string) bool {
	for _, key := range ProxyURLEnvKeys {
		if value, ok := ProxyURLEnvValue(env, key); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func ApplyProxyEnvOverrides(env map[string]string, httpAddr net.TCPAddr, socksAddr net.TCPAddr, socksEnabled bool, allowLocalBinding bool) {
	httpURL := "http://" + httpAddr.String()
	socksURL := "socks5h://" + socksAddr.String()
	env[ProxyActiveEnvKey] = "1"
	if allowLocalBinding {
		env[ProxyAllowLocalBindingEnvKey] = "1"
	} else {
		env[ProxyAllowLocalBindingEnvKey] = "0"
	}
	setEnvKeys(env, []string{
		"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
		"YARN_HTTP_PROXY", "YARN_HTTPS_PROXY", "npm_config_http_proxy", "npm_config_https_proxy", "npm_config_proxy",
		"NPM_CONFIG_HTTP_PROXY", "NPM_CONFIG_HTTPS_PROXY", "NPM_CONFIG_PROXY",
		"BUNDLE_HTTP_PROXY", "BUNDLE_HTTPS_PROXY", "PIP_PROXY", "DOCKER_HTTP_PROXY", "DOCKER_HTTPS_PROXY",
		"WS_PROXY", "WSS_PROXY", "ws_proxy", "wss_proxy",
	}, httpURL)
	setEnvKeys(env, NoProxyEnvKeys, ProxyDefaultNoProxyValue)
	env["ELECTRON_GET_USE_PROXY"] = "true"
	env["NODE_USE_ENV_PROXY"] = "1"
	if socksEnabled {
		setEnvKeys(env, AllProxyEnvKeys, socksURL)
		setEnvKeys(env, []string{"FTP_PROXY", "ftp_proxy"}, socksURL)
	} else {
		setEnvKeys(env, AllProxyEnvKeys, httpURL)
		setEnvKeys(env, []string{"FTP_PROXY", "ftp_proxy"}, httpURL)
	}
}

type ProxyManagedNetworkSandboxContext struct {
	LoopbackPorts     []uint16 `json:"loopbackPorts"`
	AllowLocalBinding bool     `json:"allowLocalBinding"`
}

type PreparedProxyManagedNetwork struct {
	mu             sync.RWMutex
	Env            map[string]string
	SandboxContext ProxyManagedNetworkSandboxContext
	server         *ProxyServer
	baseEnv        map[string]string
	config         ProxyConfig
	httpAddr       net.TCPAddr
	socksAddr      net.TCPAddr
	socksEnabled   bool
	environmentsMu sync.Mutex
	environments   map[string]*PreparedProxyManagedNetwork
	closed         bool
}

func (p *PreparedProxyManagedNetwork) Close() error {
	if p == nil {
		return nil
	}
	p.environmentsMu.Lock()
	p.closed = true
	environments := make([]*PreparedProxyManagedNetwork, 0, len(p.environments))
	for _, prepared := range p.environments {
		environments = append(environments, prepared)
	}
	p.environments = nil
	p.environmentsMu.Unlock()
	var closeErr error
	for _, prepared := range environments {
		if err := prepared.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if p.server != nil {
		if err := p.server.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (p *PreparedProxyManagedNetwork) ReloadConfig(config ProxyConfig) error {
	if p == nil || p.server == nil {
		return fmt.Errorf("managed network proxy is unavailable")
	}
	if err := p.server.ReloadConfig(config); err != nil {
		return err
	}
	prepared := PrepareProxyManagedNetwork(p.baseEnv, p.httpAddr, p.socksAddr, p.socksEnabled, config.Network.AllowLocalBinding)
	p.server.mitm.ApplyChildEnv(prepared.Env)
	p.server.runtimePolicy().broker.VirtualizeChildEnv(prepared.Env)
	p.mu.Lock()
	p.Env = prepared.Env
	p.SandboxContext = prepared.SandboxContext
	p.config = config
	p.mu.Unlock()
	p.environmentsMu.Lock()
	defer p.environmentsMu.Unlock()
	if p.closed {
		return fmt.Errorf("managed network proxy is closed")
	}
	for environmentID, environment := range p.environments {
		environmentConfig := config
		environmentConfig.EnvironmentID = environmentID
		if err := environment.ReloadConfig(environmentConfig); err != nil {
			return fmt.Errorf("reload managed network for environment %q: %w", environmentID, err)
		}
	}
	return nil
}

func (p *PreparedProxyManagedNetwork) PrepareForEnvironment(environmentID string) (map[string]string, ProxyManagedNetworkSandboxContext, error) {
	if p == nil || p.server == nil {
		return nil, ProxyManagedNetworkSandboxContext{}, fmt.Errorf("managed network proxy is unavailable")
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		environmentID = "local"
	}
	p.environmentsMu.Lock()
	defer p.environmentsMu.Unlock()
	if p.closed {
		return nil, ProxyManagedNetworkSandboxContext{}, fmt.Errorf("managed network proxy is closed")
	}
	if prepared := p.environments[environmentID]; prepared != nil {
		return prepared.EnvSnapshot(), prepared.SandboxContextSnapshot(), nil
	}
	p.mu.RLock()
	config := p.config
	baseEnv := cloneProxyEnv(p.baseEnv)
	p.mu.RUnlock()
	config.EnvironmentID = environmentID
	prepared, err := StartProxyManagedNetwork(context.Background(), config, baseEnv)
	if err != nil {
		return nil, ProxyManagedNetworkSandboxContext{}, err
	}
	if p.environments == nil {
		p.environments = map[string]*PreparedProxyManagedNetwork{}
	}
	p.environments[environmentID] = prepared
	return prepared.EnvSnapshot(), prepared.SandboxContextSnapshot(), nil
}

func (p *PreparedProxyManagedNetwork) EnvSnapshot() map[string]string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]string, len(p.Env))
	for key, value := range p.Env {
		out[key] = value
	}
	return out
}

func (p *PreparedProxyManagedNetwork) SandboxContextSnapshot() ProxyManagedNetworkSandboxContext {
	if p == nil {
		return ProxyManagedNetworkSandboxContext{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return ProxyManagedNetworkSandboxContext{
		LoopbackPorts:     append([]uint16(nil), p.SandboxContext.LoopbackPorts...),
		AllowLocalBinding: p.SandboxContext.AllowLocalBinding,
	}
}

func (p *PreparedProxyManagedNetwork) BlockedSnapshot() []ProxyBlockedRequest {
	if p == nil || p.server == nil {
		return nil
	}
	blocked := p.server.BlockedSnapshot()
	for _, environment := range p.environmentSnapshot() {
		blocked = append(blocked, environment.BlockedSnapshot()...)
	}
	sort.SliceStable(blocked, func(i, j int) bool { return blocked[i].Timestamp < blocked[j].Timestamp })
	if len(blocked) > maxProxyBlockedEvents {
		blocked = blocked[len(blocked)-maxProxyBlockedEvents:]
	}
	return blocked
}

func (p *PreparedProxyManagedNetwork) DrainBlocked() []ProxyBlockedRequest {
	if p == nil || p.server == nil {
		return nil
	}
	blocked := p.server.DrainBlocked()
	for _, environment := range p.environmentSnapshot() {
		blocked = append(blocked, environment.DrainBlocked()...)
	}
	sort.SliceStable(blocked, func(i, j int) bool { return blocked[i].Timestamp < blocked[j].Timestamp })
	if len(blocked) > maxProxyBlockedEvents {
		blocked = blocked[len(blocked)-maxProxyBlockedEvents:]
	}
	return blocked
}

func (p *PreparedProxyManagedNetwork) BlockedTotal() uint64 {
	if p == nil || p.server == nil {
		return 0
	}
	total := p.server.BlockedTotal()
	for _, environment := range p.environmentSnapshot() {
		total += environment.BlockedTotal()
	}
	return total
}

func (p *PreparedProxyManagedNetwork) environmentSnapshot() []*PreparedProxyManagedNetwork {
	if p == nil {
		return nil
	}
	p.environmentsMu.Lock()
	defer p.environmentsMu.Unlock()
	out := make([]*PreparedProxyManagedNetwork, 0, len(p.environments))
	for _, prepared := range p.environments {
		out = append(out, prepared)
	}
	return out
}

func PrepareProxyManagedNetwork(baseEnv map[string]string, httpAddr net.TCPAddr, socksAddr net.TCPAddr, socksEnabled bool, allowLocalBinding bool) PreparedProxyManagedNetwork {
	env := make(map[string]string, len(baseEnv)+16)
	for key, value := range baseEnv {
		env[key] = value
	}
	ApplyProxyEnvOverrides(env, httpAddr, socksAddr, socksEnabled, allowLocalBinding)
	ports := []uint16{uint16(httpAddr.Port)}
	if socksEnabled && socksAddr.Port != httpAddr.Port {
		ports = append(ports, uint16(socksAddr.Port))
	}
	return PreparedProxyManagedNetwork{
		Env: env,
		SandboxContext: ProxyManagedNetworkSandboxContext{
			LoopbackPorts:     ports,
			AllowLocalBinding: allowLocalBinding,
		},
	}
}

func setEnvKeys(env map[string]string, keys []string, value string) {
	for _, key := range keys {
		env[key] = value
	}
}

func ProxyTCPAddr(ip string, port uint16) net.TCPAddr {
	return net.TCPAddr{IP: net.ParseIP(ip), Port: int(port)}
}

func MustProxyTCPAddr(ip string, port uint16) net.TCPAddr {
	addr := ProxyTCPAddr(ip, port)
	if addr.IP == nil {
		panic(fmt.Sprintf("invalid ip %q", ip))
	}
	return addr
}
