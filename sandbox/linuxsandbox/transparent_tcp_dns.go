package linuxsandbox

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
)

const (
	transparentTCPMaxNames = 8192
	transparentTCPDNSTTL   = 60
)

var (
	transparentTCPDNSv4Base = netip.MustParseAddr("198.18.0.0")
	transparentTCPDNSv6Base = netip.MustParseAddr("fdc0:de00::")
)

// errUnassignedSyntheticAddress reports a packet aimed at a synthetic address
// the DNS stub never issued. Rust treats this as a relay failure rather than
// falling back to a direct dial.
var errUnassignedSyntheticAddress = errors.New("unassigned synthetic address")

// transparentTCPDNSNames assigns stable synthetic addresses to hostnames for
// the lifetime of one command. Both address families encode the same one-based
// index, so a cached client answer keeps its owner across DNS TTL expiry.
type transparentTCPDNSNames struct {
	names []string
	index map[string]int
}

func newTransparentTCPDNSNames() *transparentTCPDNSNames {
	return &transparentTCPDNSNames{index: map[string]int{}}
}

func (n *transparentTCPDNSNames) id(host string) (uint32, bool) {
	if index, ok := n.index[host]; ok {
		return uint32(index + 1), true
	}
	if len(n.names) >= transparentTCPMaxNames {
		return 0, false
	}
	n.index[host] = len(n.names)
	n.names = append(n.names, host)
	return uint32(len(n.names)), true
}

// lookup returns the hostname that owns ip. A nil result means ip is outside
// the synthetic ranges (for example a literal client destination).
func (n *transparentTCPDNSNames) lookup(ip netip.Addr) (*string, error) {
	ip = ip.Unmap()
	if ip.Is4() {
		raw := ip.As4()
		value := binary.BigEndian.Uint32(raw[:])
		baseRaw := transparentTCPDNSv4Base.As4()
		base := binary.BigEndian.Uint32(baseRaw[:])
		if value&0xfffe0000 != base {
			return nil, nil
		}
		return n.nameForID(value - base)
	}
	if ip.Is6() {
		raw := ip.As16()
		base := transparentTCPDNSv6Base.As16()
		if binary.BigEndian.Uint32(raw[:4]) != binary.BigEndian.Uint32(base[:4]) {
			return nil, nil
		}
		return n.nameForID(binary.BigEndian.Uint32(raw[12:]))
	}
	return nil, nil
}

func (n *transparentTCPDNSNames) nameForID(id uint32) (*string, error) {
	if id == 0 || int(id) > len(n.names) {
		return nil, errUnassignedSyntheticAddress
	}
	name := n.names[id-1]
	return &name, nil
}

// answer builds a DNS response for one query. A nil result means the packet is
// not a well-formed A/AAAA query this stub will answer.
func (n *transparentTCPDNSNames) answer(packet []byte) []byte {
	if len(packet) > 4096 || len(packet) < 12 {
		return nil
	}
	id := binary.BigEndian.Uint16(packet[0:2])
	flags := binary.BigEndian.Uint16(packet[2:4])
	if flags&0x8000 != 0 || (flags>>11)&0x0f != 0 {
		return nil
	}
	if binary.BigEndian.Uint16(packet[4:6]) != 1 ||
		binary.BigEndian.Uint16(packet[6:8]) != 0 ||
		binary.BigEndian.Uint16(packet[8:10]) != 0 {
		return nil
	}
	name, offset, ok := parseTransparentTCPDNSName(packet, 12)
	if !ok || offset+4 > len(packet) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(packet[offset : offset+2])
	qclass := binary.BigEndian.Uint16(packet[offset+2 : offset+4])
	questionEnd := offset + 4

	replyFlags := uint16(0x8000 | 0x0080) // response, recursion available
	replyFlags |= flags & 0x0100         // preserve recursion desired
	var answer []byte
	if qclass == 1 && (qtype == 1 || qtype == 28) && name != "" {
		if syntheticID, ok := n.id(name); ok {
			answer = encodeTransparentTCPDNSAnswer(qtype, syntheticID)
		} else {
			replyFlags |= 2 // SERVFAIL
		}
	}
	reply := make([]byte, 12, questionEnd+len(answer))
	binary.BigEndian.PutUint16(reply[0:2], id)
	binary.BigEndian.PutUint16(reply[2:4], replyFlags)
	binary.BigEndian.PutUint16(reply[4:6], 1)
	if answer != nil {
		binary.BigEndian.PutUint16(reply[6:8], 1)
	}
	reply = append(reply, packet[12:questionEnd]...)
	reply = append(reply, answer...)
	return reply
}

// parseTransparentTCPDNSName reads one uncompressed question name. Names that
// need escapes or compression cannot round-trip into a SOCKS hostname, so the
// query is dropped exactly like Rust's `managed_tcp_dns`.
func parseTransparentTCPDNSName(packet []byte, offset int) (string, int, bool) {
	var labels []string
	for {
		if offset >= len(packet) {
			return "", 0, false
		}
		length := int(packet[offset])
		offset++
		if length == 0 {
			break
		}
		if length > 63 || offset+length > len(packet) {
			return "", 0, false
		}
		label := packet[offset : offset+length]
		for _, b := range label {
			if !isTransparentTCPDNSLabelByte(b) {
				return "", 0, false
			}
		}
		labels = append(labels, string(label))
		offset += length
		if len(labels) > 253 {
			return "", 0, false
		}
	}
	return strings.ToLower(strings.Join(labels, ".")), offset, true
}

func isTransparentTCPDNSLabelByte(b byte) bool {
	if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
		return true
	}
	return b == '-' || b == '_'
}

func encodeTransparentTCPDNSAnswer(qtype uint16, id uint32) []byte {
	record := make([]byte, 12)
	binary.BigEndian.PutUint16(record[0:2], 0xc00c) // compression pointer to the question name
	binary.BigEndian.PutUint16(record[2:4], qtype)
	binary.BigEndian.PutUint16(record[4:6], 1) // class IN
	binary.BigEndian.PutUint32(record[6:10], transparentTCPDNSTTL)
	if qtype == 1 {
		binary.BigEndian.PutUint16(record[10:12], 4)
		baseRaw := transparentTCPDNSv4Base.As4()
		rdata := make([]byte, 4)
		binary.BigEndian.PutUint32(rdata, binary.BigEndian.Uint32(baseRaw[:])+id)
		return append(record, rdata...)
	}
	binary.BigEndian.PutUint16(record[10:12], 16)
	rdata := transparentTCPDNSv6Base.As16()
	binary.BigEndian.PutUint32(rdata[12:], id)
	return append(record, rdata[:]...)
}

// serveTransparentTCPDNS answers UDP queries inline and TCP queries on four
// fixed workers, matching the Rust resolver's bounded concurrency.
func serveTransparentTCPDNS(names *transparentTCPDNSNames, udp *net.UDPConn, tcp *net.TCPListener) {
	if udp != nil {
		go func() {
			buffer := make([]byte, 4096)
			for {
				n, peer, err := udp.ReadFromUDPAddrPort(buffer)
				if err != nil {
					return
				}
				if reply := names.answer(buffer[:n]); reply != nil {
					_, _ = udp.WriteToUDPAddrPort(reply, peer)
				}
			}
		}()
	}
	if tcp == nil {
		return
	}
	for range 4 {
		go func() {
			for {
				conn, err := tcp.Accept()
				if err != nil {
					return
				}
				go answerTransparentTCPDNSOverTCP(conn, names)
			}
		}()
	}
}

func answerTransparentTCPDNSOverTCP(conn net.Conn, names *transparentTCPDNSNames) {
	defer conn.Close()
	var length [2]byte
	for range 32 {
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return
		}
		size := int(binary.BigEndian.Uint16(length[:]))
		if size == 0 || size > 4096 {
			return
		}
		query := make([]byte, size)
		if _, err := io.ReadFull(conn, query); err != nil {
			return
		}
		reply := names.answer(query)
		if reply == nil {
			return
		}
		binary.BigEndian.PutUint16(length[:], uint16(len(reply)))
		if _, err := conn.Write(length[:]); err != nil {
			return
		}
		if _, err := conn.Write(reply); err != nil {
			return
		}
	}
}
