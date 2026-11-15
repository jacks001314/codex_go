package network

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// TcpUpstreamProxy is the executor-provisioned mandatory HTTP CONNECT next hop
// for opaque SOCKS TCP (Rust #61372 TcpUpstreamProxy). It is trusted startup
// state: the wire policy can neither select nor clear it, and there is no
// direct fallback when a request is routed through it.
type TcpUpstreamProxy struct {
	Host string
	Port uint16
}

// ParseTcpUpstreamProxy applies Rust's `TcpUpstreamProxy::from_str`: the value
// is a hostname or IP literal with an explicit, nonzero port.
func ParseTcpUpstreamProxy(raw string) (*TcpUpstreamProxy, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return nil, errors.New("TCP upstream proxy must be HOST:PORT")
	}
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid TCP upstream proxy %q: %w", raw, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("TCP upstream proxy host must be non-empty")
	}
	port, err := strconv.ParseUint(strings.TrimSpace(portText), 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid TCP upstream proxy port %q", portText)
	}
	if port == 0 {
		return nil, errors.New("TCP upstream proxy port must be nonzero")
	}
	return &TcpUpstreamProxy{Host: host, Port: uint16(port)}, nil
}

// Address returns the dialable host:port spelling.
func (p *TcpUpstreamProxy) Address() string {
	if p == nil {
		return ""
	}
	return net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port)))
}

// normalizeTcpUpstreamProxy copies trusted startup state so a caller cannot
// mutate a running proxy's next hop.
func normalizeTcpUpstreamProxy(proxy *TcpUpstreamProxy) *TcpUpstreamProxy {
	if proxy == nil {
		return nil
	}
	cloned := *proxy
	cloned.Host = strings.TrimSpace(cloned.Host)
	return &cloned
}
