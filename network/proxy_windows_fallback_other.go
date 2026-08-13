//go:build !windows

package network

import "net"

func proxyWindowsHTTPPortRange() [2]int {
	return [2]int{3128, 3159}
}

func proxyWindowsSOCKSPortRange() [2]int {
	return [2]int{8081, 8112}
}

func listenProxyTCP(requested net.TCPAddr, _ [2]int) (*net.TCPListener, error) {
	return net.ListenTCP("tcp", &requested)
}
