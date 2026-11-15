//go:build linux

package linuxsandbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"

	json "github.com/goccy/go-json"
	"golang.org/x/sys/unix"
)

const (
	transparentTCPTunName       = "codextun"
	transparentTCPNamespaceArg  = "--transparent-tcp-setup"
	transparentTCPResolverName  = "codex-resolver"
	transparentTCPResolver      = "nameserver 127.0.0.53\n"
	transparentTCPCapVersion3   = 0x20080522
	transparentTCPCapSetFCAPBit = 31
)

type transparentTCPNamespacePayload struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

type transparentTCPCapHeader struct {
	Version uint32
	PID     int32
}

type transparentTCPCapData struct {
	Effective   uint32
	Permitted   uint32
	Inheritable uint32
}

type transparentTCPIn6Ifreq struct {
	Address   [16]byte
	PrefixLen uint32
	IfIndex   int32
}

type transparentTCPChildExitError struct {
	code int
}

func (e *transparentTCPChildExitError) Error() string {
	return fmt.Sprintf("sandboxed command exited with status %d", e.code)
}

func transparentTCPDupFD(fd int) (int, error) {
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return -1, err
	}
	return dup, nil
}

func unixFcntlSetCloexec(fd int) (int, error) {
	return unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
}

func unixFcntlClearCloexec(fd int) error {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0)
	return err
}

// execTransparentTCPBubblewrap starts the trusted namespace stage: a child
// process creates a private user and network namespace, prepares the TUN and
// resolver descriptors, then execs stock bubblewrap without unsharing the
// network namespace again.
func execTransparentTCPBubblewrap(program string, args []string, resolver *os.File) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve current executable path: %w", err)
	}
	payloadReader, payloadWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create transparent TCP payload pipe: %w", err)
	}
	defer payloadReader.Close()
	payload := transparentTCPNamespacePayload{Program: program, Args: args}
	encoded, err := json.Marshal(&payload)
	if err != nil {
		payloadWriter.Close()
		return err
	}

	command := exec.Command(executable, transparentTCPNamespaceArg)
	command.Args[0] = LinuxSandboxArg0
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.ExtraFiles = []*os.File{payloadReader, resolver}
	uid, gid := os.Getuid(), os.Getgid()
	command.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWNET,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	if err := command.Start(); err != nil {
		payloadWriter.Close()
		return fmt.Errorf("failed to create private TCP namespace: %w", err)
	}
	if _, err := payloadWriter.Write(encoded); err != nil {
		payloadWriter.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("failed to hand off transparent TCP namespace: %w", err)
	}
	payloadWriter.Close()

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if command.Process != nil {
				_ = command.Process.Signal(sig)
			}
		}
	}()

	err = command.Wait()
	if err == nil {
		return &transparentTCPChildExitError{}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				code = 128 + int(status.Signal())
			}
		}
		return &transparentTCPChildExitError{code: code}
	}
	return fmt.Errorf("failed to wait for private TCP namespace: %w", err)
}

// runTransparentTCPNamespaceSetup runs inside the fresh user and network
// namespaces. It configures the TUN, resolver, and routes, then replaces itself
// with the prepared bubblewrap command.
func runTransparentTCPNamespaceSetup(payloadFD int) error {
	payloadFile := os.NewFile(uintptr(payloadFD), "codex-transparent-tcp-payload")
	if payloadFile == nil {
		return errors.New("missing transparent TCP namespace payload")
	}
	defer payloadFile.Close()
	encoded, err := io.ReadAll(payloadFile)
	if err != nil {
		return fmt.Errorf("read transparent TCP namespace payload: %w", err)
	}
	var payload transparentTCPNamespacePayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return fmt.Errorf("invalid transparent TCP namespace payload: %w", err)
	}
	handles, err := createTransparentTCPNamespace()
	if err != nil {
		return err
	}
	if err := configureTransparentTCPInterface(handles.tun, handles.ifindex); err != nil {
		closeTransparentTCPHandles(handles)
		return err
	}
	args, err := insertTransparentTCPArgs(payload.Args, fmt.Sprintf("%d,%d,%d", handles.tun, handles.dnsUDP, handles.dnsTCP))
	if err != nil {
		closeTransparentTCPHandles(handles)
		return err
	}
	for _, fd := range []int{handles.tun, handles.dnsUDP, handles.dnsTCP} {
		if err := unixFcntlClearCloexec(fd); err != nil {
			closeTransparentTCPHandles(handles)
			return fmt.Errorf("prepare transparent TCP descriptor handoff: %w", err)
		}
	}
	if err := dropTransparentTCPCapabilities(); err != nil {
		closeTransparentTCPHandles(handles)
		return err
	}
	return syscall.Exec(payload.Program, args, os.Environ())
}

type transparentTCPHandles struct {
	tun      int
	dnsUDP   int
	dnsTCP   int
	ifindex  int
	closeFns []func()
}

func closeTransparentTCPHandles(handles *transparentTCPHandles) {
	for _, closeFn := range handles.closeFns {
		closeFn()
	}
}

func createTransparentTCPNamespace() (*transparentTCPHandles, error) {
	tun, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	handles := &transparentTCPHandles{tun: tun, dnsUDP: -1, dnsTCP: -1}
	handles.closeFns = append(handles.closeFns, func() { _ = unix.Close(tun) })
	interfaceRequest, err := unix.NewIfreq(transparentTCPTunName)
	if err != nil {
		closeTransparentTCPHandles(handles)
		return nil, err
	}
	interfaceRequest.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(tun, unix.TUNSETIFF, interfaceRequest); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("create transparent TCP TUN interface: %w", err)
	}
	control, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("open interface control socket: %w", err)
	}
	handles.closeFns = append(handles.closeFns, func() { _ = unix.Close(control) })
	if err := bringTransparentTCPInterfaceUp(control, "lo"); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, err
	}
	if err := bringTransparentTCPInterfaceUp(control, transparentTCPTunName); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, err
	}
	indexRequest, err := unix.NewIfreq(transparentTCPTunName)
	if err != nil {
		closeTransparentTCPHandles(handles)
		return nil, err
	}
	if err := unix.IoctlIfreq(control, unix.SIOCGIFINDEX, indexRequest); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("resolve transparent TCP interface index: %w", err)
	}
	handles.ifindex = int(indexRequest.Uint32())

	udp, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("open transparent TCP DNS UDP socket: %w", err)
	}
	handles.dnsUDP = udp
	handles.closeFns = append(handles.closeFns, func() { _ = unix.Close(udp) })
	tcp, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("open transparent TCP DNS TCP socket: %w", err)
	}
	handles.dnsTCP = tcp
	handles.closeFns = append(handles.closeFns, func() { _ = unix.Close(tcp) })
	dnsAddress := &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 53}, Port: 53}
	if err := unix.Bind(udp, dnsAddress); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("bind transparent TCP DNS UDP socket: %w", err)
	}
	if err := unix.Bind(tcp, dnsAddress); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("bind transparent TCP DNS TCP socket: %w", err)
	}
	if err := unix.Listen(tcp, 128); err != nil {
		closeTransparentTCPHandles(handles)
		return nil, fmt.Errorf("listen on transparent TCP DNS socket: %w", err)
	}
	return handles, nil
}

func bringTransparentTCPInterfaceUp(control int, name string) error {
	request, err := unix.NewIfreq(name)
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(control, unix.SIOCGIFFLAGS, request); err != nil {
		return fmt.Errorf("read %s interface flags: %w", name, err)
	}
	request.SetUint16(request.Uint16() | unix.IFF_UP)
	if err := unix.IoctlIfreq(control, unix.SIOCSIFFLAGS, request); err != nil {
		return fmt.Errorf("bring %s interface up: %w", name, err)
	}
	return nil
}

func configureTransparentTCPInterface(tun, ifindex int) error {
	control, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open transparent TCP address socket: %w", err)
	}
	defer unix.Close(control)
	address, err := unix.NewIfreq(transparentTCPTunName)
	if err != nil {
		return err
	}
	if err := address.SetInet4Addr([]byte{192, 0, 2, 1}); err != nil {
		return err
	}
	if err := unix.IoctlIfreq(control, unix.SIOCSIFADDR, address); err != nil {
		return fmt.Errorf("assign transparent TCP interface address: %w", err)
	}
	netmask, err := unix.NewIfreq(transparentTCPTunName)
	if err != nil {
		return err
	}
	if err := netmask.SetInet4Addr([]byte{255, 255, 255, 255}); err != nil {
		return err
	}
	if err := unix.IoctlIfreq(control, unix.SIOCSIFNETMASK, netmask); err != nil {
		return fmt.Errorf("assign transparent TCP interface netmask: %w", err)
	}
	if err := addTransparentTCPIPv6Address(ifindex); err != nil {
		return err
	}
	if err := addTransparentTCPDefaultRoute(unix.AF_INET, ifindex); err != nil {
		return err
	}
	return addTransparentTCPDefaultRoute(unix.AF_INET6, ifindex)
}

func addTransparentTCPIPv6Address(ifindex int) error {
	control, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open transparent TCP IPv6 socket: %w", err)
	}
	defer unix.Close(control)
	request := transparentTCPIn6Ifreq{
		Address:   netip.MustParseAddr("2001:db8::1").As16(),
		PrefixLen: 128,
		IfIndex:   int32(ifindex),
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(control), uintptr(unix.SIOCSIFADDR), uintptr(unsafe.Pointer(&request))); errno != 0 {
		return fmt.Errorf("assign transparent TCP IPv6 address: %w", errno)
	}
	return nil
}

// addTransparentTCPDefaultRoute installs a device route through the TUN using
// netlink, avoiding the libc `rtentry` layout Rust needs for its ioctl path.
func addTransparentTCPDefaultRoute(family int, ifindex int) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open route netlink socket: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("bind route netlink socket: %w", err)
	}
	request := make([]byte, unix.SizeofNlMsghdr+unix.SizeofRtMsg+8)
	message := (*unix.NlMsghdr)(unsafe.Pointer(&request[0]))
	message.Type = unix.RTM_NEWROUTE
	message.Flags = unix.NLM_F_REQUEST | unix.NLM_F_ACK | unix.NLM_F_CREATE | unix.NLM_F_REPLACE
	message.Seq = 1
	route := (*unix.RtMsg)(unsafe.Pointer(&request[unix.SizeofNlMsghdr]))
	route.Family = uint8(family)
	route.Table = unix.RT_TABLE_MAIN
	route.Protocol = unix.RTPROT_BOOT
	route.Scope = unix.RT_SCOPE_UNIVERSE
	route.Type = unix.RTN_UNICAST
	attributeOffset := unix.SizeofNlMsghdr + unix.SizeofRtMsg
	binary.NativeEndian.PutUint16(request[attributeOffset:attributeOffset+2], 8)
	binary.NativeEndian.PutUint16(request[attributeOffset+2:attributeOffset+4], unix.RTA_OIF)
	binary.NativeEndian.PutUint32(request[attributeOffset+4:attributeOffset+8], uint32(ifindex))
	message.Len = uint32(len(request))
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("install transparent TCP default route: %w", err)
	}
	reply := make([]byte, 1024)
	for {
		n, _, err := unix.Recvfrom(fd, reply, 0)
		if err != nil {
			return fmt.Errorf("read transparent TCP route acknowledgement: %w", err)
		}
		messages, err := syscall.ParseNetlinkMessage(reply[:n])
		if err != nil {
			return fmt.Errorf("parse transparent TCP route acknowledgement: %w", err)
		}
		for _, replyMessage := range messages {
			if replyMessage.Header.Type != unix.NLMSG_ERROR {
				continue
			}
			if len(replyMessage.Data) < 4 {
				return errors.New("invalid transparent TCP route acknowledgement")
			}
			code := int32(binary.NativeEndian.Uint32(replyMessage.Data[:4]))
			if code == 0 || code == -int32(unix.EEXIST) {
				return nil
			}
			return fmt.Errorf("install transparent TCP default route: %w", syscall.Errno(-code))
		}
	}
}

func insertTransparentTCPArgs(args []string, descriptors string) ([]string, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(args) {
		return nil, errors.New("missing inner command for transparent TCP")
	}
	insertAt := separator + 2
	if insertAt > len(args) {
		insertAt = len(args)
	}
	out := make([]string, 0, len(args)+3)
	out = append(out, args[:insertAt]...)
	out = append(out, "--transparent-tcp", "--transparent-tcp-fd", descriptors)
	out = append(out, args[insertAt:]...)
	return out, nil
}

func dropTransparentTCPCapabilities() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs for transparent TCP: %w", err)
	}
	keep := uint32(0)
	if os.Geteuid() == 0 {
		keep = 1 << transparentTCPCapSetFCAPBit
	}
	header := transparentTCPCapHeader{Version: transparentTCPCapVersion3}
	data := [2]transparentTCPCapData{{Effective: keep, Permitted: keep}}
	if _, _, errno := unix.Syscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0); errno != 0 {
		return fmt.Errorf("drop transparent TCP capabilities: %w", errno)
	}
	return nil
}

func createTransparentTCPResolver() (*os.File, error) {
	fd, err := unix.MemfdCreate(transparentTCPResolverName, unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("create transparent TCP resolver: %w", err)
	}
	file := os.NewFile(uintptr(fd), transparentTCPResolverName)
	if _, err := file.WriteString(transparentTCPResolver); err != nil {
		file.Close()
		return nil, fmt.Errorf("write transparent TCP resolver: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("rewind transparent TCP resolver: %w", err)
	}
	return file, nil
}
