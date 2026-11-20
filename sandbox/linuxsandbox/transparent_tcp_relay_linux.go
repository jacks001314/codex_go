//go:build linux

package linuxsandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	transparentTCPMTU          = 1500
	transparentTCPMaxFlows     = 128
	transparentTCPProbeAddress = "198.18.0.0:9"
)

var transparentTCPProbeTarget = netip.MustParseAddrPort(transparentTCPProbeAddress)

// transparentTCPRelay owns the TUN data path and the DNS stub inside the
// command's private network namespace. The kernel still owns the native client
// sockets; only the managed SOCKS bridge can supply an upstream stream.
type transparentTCPRelay struct {
	proxy net.Addr
	names *transparentTCPDNSNames
	stack *stack.Stack
	slots chan struct{}
	probe chan netip.AddrPort
	tunFD int
	udp   *net.UDPConn
	tcp   *net.TCPListener

	probeMu    sync.Mutex
	probeConns []*gonet.TCPConn

	// stopped closes when the relay data path stops serving. With an inherited
	// PID namespace there is no namespace reaper, so losing the data path must
	// end the workload (Rust #62100).
	stopped  chan struct{}
	stopOnce sync.Once
}

// acceptTransparentTCPDescriptors takes ownership of the three descriptors the
// trusted outer helper passed through bubblewrap. Descriptors returned to the
// workload must not leak into it.
func acceptTransparentTCPDescriptors(raw []int) (tun, dnsUDP, dnsTCP *os.File, err error) {
	if len(raw) != 3 {
		return nil, nil, nil, errors.New("invalid transparent TCP descriptors")
	}
	seen := map[int]bool{}
	for _, fd := range raw {
		if fd <= 2 || seen[fd] {
			return nil, nil, nil, errors.New("invalid transparent TCP descriptors")
		}
		seen[fd] = true
		if _, err := unixFcntlSetCloexec(fd); err != nil {
			return nil, nil, nil, err
		}
	}
	return os.NewFile(uintptr(raw[0]), "codex-transparent-tcp-tun"),
		os.NewFile(uintptr(raw[1]), "codex-transparent-tcp-dns-udp"),
		os.NewFile(uintptr(raw[2]), "codex-transparent-tcp-dns-tcp"), nil
}

// prepareTransparentTCPRelay validates the inherited descriptors and builds the
// DNS listeners. The stack starts only after the workload's seccomp child is
// ready, mirroring the Rust helper's readiness probe.
func prepareTransparentTCPRelay(raw []int, proxy net.Addr) (*transparentTCPRelay, error) {
	if proxy == nil {
		return nil, errors.New("transparent TCP requires managed SOCKS5")
	}
	tun, udpFile, tcpFile, err := acceptTransparentTCPDescriptors(raw)
	if err != nil {
		return nil, err
	}
	defer udpFile.Close()
	defer tcpFile.Close()

	udpConn, err := net.FilePacketConn(udpFile)
	if err != nil {
		tun.Close()
		return nil, fmt.Errorf("open transparent TCP DNS UDP listener: %w", err)
	}
	udp, ok := udpConn.(*net.UDPConn)
	if !ok {
		udpConn.Close()
		tun.Close()
		return nil, errors.New("transparent TCP DNS UDP listener is not UDP")
	}
	tcpListener, err := net.FileListener(tcpFile)
	if err != nil {
		udp.Close()
		tun.Close()
		return nil, fmt.Errorf("open transparent TCP DNS TCP listener: %w", err)
	}
	tcpConn, ok := tcpListener.(*net.TCPListener)
	if !ok {
		tcpListener.Close()
		udp.Close()
		tun.Close()
		return nil, errors.New("transparent TCP DNS TCP listener is not TCP")
	}
	fd, err := transparentTCPDupFD(int(tun.Fd()))
	tun.Close()
	if err != nil {
		udp.Close()
		tcpConn.Close()
		return nil, fmt.Errorf("duplicate transparent TCP TUN: %w", err)
	}
	return &transparentTCPRelay{
		proxy:   proxy,
		names:   newTransparentTCPDNSNames(),
		slots:   make(chan struct{}, transparentTCPMaxFlows),
		probe:   make(chan netip.AddrPort, 1),
		stopped: make(chan struct{}),
		tunFD:   fd,
		udp:     udp,
		tcp:     tcpConn,
	}, nil
}

// start publishes DNS, attaches the TUN to a userspace TCP stack, and verifies
// the whole path with a native probe connection before the workload is released.
func (r *transparentTCPRelay) start(ctx context.Context) error {
	serveTransparentTCPDNS(r.names, r.udp, r.tcp)

	stackInstance := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	nicID := stackInstance.NextNICID()
	forwarder := tcp.NewForwarder(stackInstance, 0, transparentTCPMaxFlows, r.handleForwarderRequest)
	stackInstance.SetTransportProtocolHandler(tcp.ProtocolNumber, forwarder.HandlePacket)

	endpoint, err := fdbased.New(&fdbased.Options{
		FDs:            []int{r.tunFD},
		MTU:            transparentTCPMTU,
		EthernetHeader: false,
	})
	if err != nil {
		stackInstance.Close()
		return fmt.Errorf("attach transparent TCP TUN: %w", err)
	}
	if tcpErr := stackInstance.CreateNIC(nicID, endpoint); tcpErr != nil {
		stackInstance.Close()
		return fmt.Errorf("create transparent TCP NIC: %v", tcpErr)
	}
	stackInstance.SetPromiscuousMode(nicID, true)
	stackInstance.SetSpoofing(nicID, true)
	stackInstance.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})
	r.stack = stackInstance
	r.tunFD = -1

	dialer := net.Dialer{Timeout: transparentTCPSOCKSTimeout}
	probe, err := dialer.DialContext(ctx, "tcp", transparentTCPProbeAddress)
	if err != nil {
		r.stack.Close()
		return fmt.Errorf("transparent TCP readiness probe: %w", err)
	}
	local := probe.LocalAddr().(*net.TCPAddr).AddrPort()
	_ = probe.Close()
	r.closeProbeConns()
	select {
	case source := <-r.probe:
		if source != local {
			r.stack.Close()
			return errors.New("unexpected transparent TCP readiness source")
		}
	case <-time.After(transparentTCPSOCKSTimeout):
		r.stack.Close()
		return errors.New("transparent TCP readiness probe timed out")
	case <-ctx.Done():
		r.stack.Close()
		return ctx.Err()
	}
	return nil
}

func (r *transparentTCPRelay) closeProbeConns() {
	r.probeMu.Lock()
	conns := r.probeConns
	r.probeConns = nil
	r.probeMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (r *transparentTCPRelay) close() {
	if r.stack != nil {
		r.stack.Close()
	}
	if r.udp != nil {
		r.udp.Close()
	}
	if r.tcp != nil {
		r.tcp.Close()
	}
	r.stopOnce.Do(func() { close(r.stopped) })
}

func (r *transparentTCPRelay) handleForwarderRequest(request *tcp.ForwarderRequest) {
	id := request.ID()
	source := netip.AddrPortFrom(transparentTCPAddrFrom(id.RemoteAddress), id.RemotePort)
	target := netip.AddrPortFrom(transparentTCPAddrFrom(id.LocalAddress), id.LocalPort)
	var queue waiter.Queue
	endpoint, tcpErr := request.CreateEndpoint(&queue)
	if tcpErr != nil {
		request.Complete(true)
		return
	}
	defer request.Complete(false)
	conn := gonet.NewTCPConn(&queue, endpoint)
	if target == transparentTCPProbeTarget {
		r.probeMu.Lock()
		r.probeConns = append(r.probeConns, conn)
		r.probeMu.Unlock()
		select {
		case r.probe <- source:
		default:
		}
		return
	}
	select {
	case r.slots <- struct{}{}:
	default:
		endpoint.Abort()
		return
	}
	go func() {
		defer func() { <-r.slots }()
		r.relayFlow(conn, endpoint, source, target)
	}()
}

func (r *transparentTCPRelay) relayFlow(conn *gonet.TCPConn, endpoint tcpip.Endpoint, source, target netip.AddrPort) {
	defer conn.Close()
	host := target.Addr().String()
	if name, err := r.names.lookup(target.Addr()); err != nil {
		endpoint.Abort()
		return
	} else if name != nil {
		host = *name
	}
	owner, err := captureTransparentTCPPendingOwner(source, target)
	if err != nil {
		endpoint.Abort()
		return
	}
	upstream, err := dialTransparentTCPSOCKS5(context.Background(), r.proxy, host, target.Port(), owner)
	if err != nil {
		endpoint.Abort()
		return
	}
	defer upstream.Close()
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(upstream, conn)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(conn, upstream)
		_ = conn.CloseWrite()
	}()
	wait.Wait()
}

func transparentTCPAddrFrom(address tcpip.Address) netip.Addr {
	if address.Len() == 4 {
		return netip.AddrFrom4(address.As4())
	}
	return netip.AddrFrom16(address.As16())
}
