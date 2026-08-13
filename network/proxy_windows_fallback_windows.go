//go:build windows

package network

import (
	"errors"
	"net"
	"os"
	"syscall"
)

// proxyWindowsHTTPPortRange is the preferred bounded HTTP proxy port range
// used when the explicitly configured Windows managed HTTP proxy port is
// unavailable (Rust #38265).
func proxyWindowsHTTPPortRange() [2]int {
	return [2]int{3128, 3159}
}

// proxyWindowsSOCKSPortRange is the preferred bounded SOCKS5 proxy port range
// used when the explicitly configured Windows managed SOCKS5 proxy port is
// unavailable (Rust #38265).
func proxyWindowsSOCKSPortRange() [2]int {
	return [2]int{8081, 8112}
}

// listenProxyTCP reserves the requested loopback TCP listener. On Windows it
// first tries the requested port, then the preferred bounded range, and
// finally falls back to an ephemeral loopback port.
func listenProxyTCP(requested net.TCPAddr, preferred [2]int) (*net.TCPListener, error) {
	if requested.Port == 0 {
		return net.ListenTCP("tcp", &requested)
	}
	listener, err := net.ListenTCP("tcp", &requested)
	if err == nil {
		return listener, nil
	}
	if !isProxyPortUnavailable(err) {
		return nil, err
	}
	for port := preferred[0]; port <= preferred[1]; port++ {
		if port == requested.Port {
			continue
		}
		candidate := requested
		candidate.Port = port
		listener, listenErr := net.ListenTCP("tcp", &candidate)
		if listenErr == nil {
			return listener, nil
		}
		if !isProxyPortUnavailable(listenErr) {
			return nil, listenErr
		}
	}
	fallback := requested
	fallback.Port = 0
	listener, err = net.ListenTCP("tcp", &fallback)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func isProxyPortUnavailable(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) || errors.Is(err, os.ErrPermission)
}
