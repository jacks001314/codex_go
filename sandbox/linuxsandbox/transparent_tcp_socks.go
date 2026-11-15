package linuxsandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	transparentTCPSOCKSTimeout    = 10 * time.Second
	transparentTCPApprovalPoll    = 500 * time.Millisecond
	transparentTCPSOCKSNoAuth     = 0x00
	transparentTCPSOCKSConnect    = 0x01
	transparentTCPSOCKSIPv4       = 0x01
	transparentTCPSOCKSIPv6       = 0x04
	transparentTCPSOCKSDomainName = 0x03
)

// transparentTCPApprovalOwner reports whether the native client socket that
// requested a destination is still open while the managed proxy waits for a
// human decision.
type transparentTCPApprovalOwner interface {
	stillOwned() (bool, error)
}

// transparentTCPBufferedConn keeps bytes the SOCKS proxy sent past its CONNECT
// reply available to the caller.
type transparentTCPBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *transparentTCPBufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}

// dialTransparentTCPSOCKS5 opens one managed SOCKS5 CONNECT tunnel. The target
// is always the original hostname when the DNS stub issued it, so proxy policy
// sees the same identity the client resolved.
func dialTransparentTCPSOCKS5(ctx context.Context, proxy net.Addr, host string, port uint16, owner transparentTCPApprovalOwner) (net.Conn, error) {
	dialer := net.Dialer{Timeout: transparentTCPSOCKSTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", proxy.String())
	if err != nil {
		return nil, fmt.Errorf("connect managed SOCKS5: %w", err)
	}
	closeOnError := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(transparentTCPSOCKSTimeout)); err != nil {
		return closeOnError(err)
	}
	if _, err := conn.Write([]byte{5, 1, transparentTCPSOCKSNoAuth}); err != nil {
		return closeOnError(fmt.Errorf("write managed SOCKS5 greeting: %w", err))
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return closeOnError(fmt.Errorf("read managed SOCKS5 greeting: %w", err))
	}
	if method[0] != 5 || method[1] != transparentTCPSOCKSNoAuth {
		return closeOnError(errors.New("managed SOCKS5 handshake failed"))
	}

	frame, err := transparentTCPSOCKSConnectFrame(host, port)
	if err != nil {
		return closeOnError(err)
	}
	if _, err := conn.Write(frame); err != nil {
		return closeOnError(fmt.Errorf("write managed SOCKS5 connect: %w", err))
	}
	reader := bufio.NewReader(conn)
	if err := waitForTransparentTCPSOCKSReply(conn, reader, owner); err != nil {
		if owner != nil {
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
		}
		return closeOnError(err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return closeOnError(err)
	}
	return &transparentTCPBufferedConn{Conn: conn, reader: reader}, nil
}

// transparentTCPSOCKSConnectFrame emits the whole CONNECT request in one write.
func transparentTCPSOCKSConnectFrame(host string, port uint16) ([]byte, error) {
	frame := []byte{5, transparentTCPSOCKSConnect, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			frame = append(frame, transparentTCPSOCKSIPv4)
			frame = append(frame, v4...)
		} else {
			frame = append(frame, transparentTCPSOCKSIPv6)
			frame = append(frame, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("managed SOCKS5 destination hostname is invalid")
		}
		frame = append(frame, transparentTCPSOCKSDomainName, byte(len(host)))
		frame = append(frame, host...)
	}
	frame = append(frame, byte(port>>8), byte(port))
	return frame, nil
}

// waitForTransparentTCPSOCKSReply blocks until the proxy answers. While an
// approval is outstanding the relay only polls framing progress and whether the
// native socket survives, so a closed client cancels the pending connection.
func waitForTransparentTCPSOCKSReply(conn net.Conn, reader *bufio.Reader, owner transparentTCPApprovalOwner) error {
	for {
		if owner == nil {
			if err := conn.SetReadDeadline(time.Now().Add(transparentTCPSOCKSTimeout)); err != nil {
				return err
			}
		} else if err := conn.SetReadDeadline(time.Now().Add(transparentTCPApprovalPoll)); err != nil {
			return err
		}
		if _, err := reader.Peek(1); err != nil {
			if isTransparentTCPTimeout(err) {
				if owner == nil {
					return errors.New("managed SOCKS5 connect reply timed out")
				}
				owned, ownedErr := owner.stillOwned()
				if ownedErr != nil {
					return ownedErr
				}
				if !owned {
					return errors.New("transparent TCP client closed before approval")
				}
				continue
			}
			return fmt.Errorf("read managed SOCKS5 connect reply: %w", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(transparentTCPSOCKSTimeout)); err != nil {
			return err
		}
		return readTransparentTCPSOCKSReply(reader)
	}
}

func isTransparentTCPTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func readTransparentTCPSOCKSReply(reader *bufio.Reader) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return fmt.Errorf("read managed SOCKS5 connect reply: %w", err)
	}
	if header[0] != 5 || header[1] != 0 {
		return errors.New("invalid managed SOCKS5 success reply")
	}
	var addressLength int
	switch header[3] {
	case transparentTCPSOCKSIPv4:
		addressLength = net.IPv4len
	case transparentTCPSOCKSIPv6:
		addressLength = net.IPv6len
	case transparentTCPSOCKSDomainName:
		length, err := reader.ReadByte()
		if err != nil {
			return fmt.Errorf("read managed SOCKS5 reply domain: %w", err)
		}
		if length == 0 {
			return errors.New("invalid managed SOCKS5 success reply")
		}
		addressLength = int(length)
	default:
		return errors.New("invalid managed SOCKS5 success reply")
	}
	if _, err := reader.Discard(addressLength + 2); err != nil {
		return fmt.Errorf("read managed SOCKS5 reply address: %w", err)
	}
	return nil
}
