package network

import (
	"net/url"
	"strings"
)

type ProxyAddress struct {
	Raw      string
	Scheme   string
	Host     string
	Port     string
	Username string
	Password string
}

type ProxyUpstreamConfig struct {
	HTTP  *ProxyAddress
	HTTPS *ProxyAddress
	All   *ProxyAddress
}

func ProxyUpstreamConfigFromEnv(env map[string]string) ProxyUpstreamConfig {
	return ProxyUpstreamConfig{
		HTTP:  ReadProxyAddressEnv(env, []string{"HTTP_PROXY", "http_proxy"}),
		HTTPS: ReadProxyAddressEnv(env, []string{"HTTPS_PROXY", "https_proxy"}),
		All:   ReadProxyAddressEnv(env, []string{"ALL_PROXY", "all_proxy"}),
	}
}

func (c *ProxyUpstreamConfig) ProxyForProtocol(secure bool) *ProxyAddress {
	if c == nil {
		return nil
	}
	if secure {
		if c.HTTPS != nil {
			return c.HTTPS.Clone()
		}
		if c.HTTP != nil {
			return c.HTTP.Clone()
		}
		if c.All != nil {
			return c.All.Clone()
		}
		return nil
	}
	if c.HTTP != nil {
		return c.HTTP.Clone()
	}
	if c.All != nil {
		return c.All.Clone()
	}
	return nil
}

func ReadProxyAddressEnv(env map[string]string, keys []string) *ProxyAddress {
	for _, key := range keys {
		value := strings.TrimSpace(env[key])
		if value == "" {
			continue
		}
		address, err := ParseProxyAddress(value)
		if err == nil && address.IsHTTP() {
			return &address
		}
	}
	return nil
}

func ProxyAddressForConnect(env map[string]string) *ProxyAddress {
	proxyConfig := ProxyUpstreamConfigFromEnv(env)
	return (&proxyConfig).ProxyForProtocol(true)
}

func ParseProxyAddress(value string) (ProxyAddress, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ProxyAddress{}, err
	}
	password, _ := parsed.User.Password()
	address := ProxyAddress{
		Raw:      value,
		Scheme:   strings.ToLower(parsed.Scheme),
		Host:     parsed.Hostname(),
		Port:     parsed.Port(),
		Username: parsed.User.Username(),
		Password: password,
	}
	if address.Scheme == "" {
		address.Scheme = "http"
	}
	return address, nil
}

func (a *ProxyAddress) IsHTTP() bool {
	if a == nil {
		return false
	}
	return a.Scheme == "" || a.Scheme == "http" || a.Scheme == "https"
}

func (a *ProxyAddress) Clone() *ProxyAddress {
	if a == nil {
		return nil
	}
	clone := *a
	return &clone
}
