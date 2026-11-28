package anytls

import (
	"encoding/binary"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/net"
)

const (
	cmdWaste               byte = 0
	cmdSYN                 byte = 1
	cmdPSH                 byte = 2
	cmdFIN                 byte = 3
	cmdSettings            byte = 4
	cmdAlert               byte = 5
	cmdUpdatePaddingScheme byte = 6
	cmdSYNACK              byte = 7
	cmdHeartRequest        byte = 8
	cmdHeartResponse       byte = 9
	cmdServerSettings      byte = 10
)

const (
	protocolVersion = 2
	frameOverhead   = 7
	maxFramePayload = 0xffff
	passwordLen     = 32

	settingVersion    = "v"
	settingClient     = "client"
	settingPaddingMD5 = "padding-md5"

	uotVersion            = 2
	uotLegacyVersion      = 1
	uotMagicAddress       = "sp.v2.udp-over-tcp.arpa"
	uotLegacyMagicAddress = "sp.udp-over-tcp.arpa"
)

const (
	socksAtypEmpty  byte = 0x00
	socksAtypIPv4   byte = 0x01
	socksAtypDomain byte = 0x03
	socksAtypIPv6   byte = 0x04

	uotAtypIPv4   byte = 0x00
	uotAtypIPv6   byte = 0x01
	uotAtypDomain byte = 0x02
)

func putFrameHeader(dst []byte, cmd byte, sid uint32, length int) {
	dst[0] = cmd
	binary.BigEndian.PutUint32(dst[1:5], sid)
	binary.BigEndian.PutUint16(dst[5:7], uint16(length))
}

func encodeSettings(kv map[string]string) []byte {
	var sb strings.Builder
	for i, k := range slices.Sorted(maps.Keys(kv)) {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(kv[k])
	}
	return []byte(sb.String())
}

func parseSettings(data []byte) map[string]string {
	out := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func socksAddrLen(dest net.Destination) int {
	if !dest.IsValid() {
		return 1
	}
	switch dest.Address.Family() {
	case net.AddressFamilyIPv4:
		return 1 + 4 + 2
	case net.AddressFamilyIPv6:
		return 1 + 16 + 2
	case net.AddressFamilyDomain:
		return 1 + 1 + len(dest.Address.Domain()) + 2
	default:
		return 1
	}
}

func writeSocksAddr(b *buf.Buffer, dest net.Destination) error {
	if !dest.IsValid() {
		return b.WriteByte(socksAtypEmpty)
	}
	switch dest.Address.Family() {
	case net.AddressFamilyIPv4:
		p := b.Extend(1 + 4 + 2)
		p[0] = socksAtypIPv4
		copy(p[1:5], dest.Address.IP().To4())
		binary.BigEndian.PutUint16(p[5:7], dest.Port.Value())
	case net.AddressFamilyIPv6:
		p := b.Extend(1 + 16 + 2)
		p[0] = socksAtypIPv6
		copy(p[1:17], dest.Address.IP().To16())
		binary.BigEndian.PutUint16(p[17:19], dest.Port.Value())
	case net.AddressFamilyDomain:
		domain := dest.Address.Domain()
		if len(domain) == 0 || len(domain) > 255 {
			return errors.New("anytls: invalid domain length: ", len(domain))
		}
		p := b.Extend(int32(1 + 1 + len(domain) + 2))
		p[0] = socksAtypDomain
		p[1] = byte(len(domain))
		copy(p[2:2+len(domain)], domain)
		binary.BigEndian.PutUint16(p[2+len(domain):], dest.Port.Value())
	default:
		return errors.New("anytls: unsupported address family")
	}
	return nil
}

func readSocksAddr(r io.Reader, network net.Network, allowEmpty bool) (net.Destination, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return net.Destination{}, err
	}
	var addr net.Address
	switch atyp[0] {
	case socksAtypEmpty:
		if allowEmpty {
			return net.Destination{}, nil
		}
		return net.Destination{}, errors.New("anytls: empty socks address")
	case socksAtypIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(r, ip[:]); err != nil {
			return net.Destination{}, err
		}
		addr = net.IPAddress(ip[:])
	case socksAtypIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(r, ip[:]); err != nil {
			return net.Destination{}, err
		}
		addr = net.IPAddress(ip[:])
	case socksAtypDomain:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return net.Destination{}, err
		}
		if l[0] == 0 {
			return net.Destination{}, errors.New("anytls: empty domain in socks address")
		}
		var domainBuf [255]byte
		domainSlice := domainBuf[:l[0]]
		if _, err := io.ReadFull(r, domainSlice); err != nil {
			return net.Destination{}, err
		}
		addr = net.ParseAddress(string(domainSlice))
	default:
		return net.Destination{}, errors.New("anytls: unknown socks address type: ", atyp[0])
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(r, portBuf[:]); err != nil {
		return net.Destination{}, err
	}
	return net.Destination{
		Address: addr,
		Port:    net.PortFromBytes(portBuf[:]),
		Network: network,
	}, nil
}

func uotAddrLen(dest net.Destination) int {
	switch dest.Address.Family() {
	case net.AddressFamilyIPv4:
		return 1 + 4 + 2
	case net.AddressFamilyIPv6:
		return 1 + 16 + 2
	case net.AddressFamilyDomain:
		return 1 + 1 + len(dest.Address.Domain()) + 2
	default:
		return 0
	}
}

func writeUoTAddr(b *buf.Buffer, dest net.Destination) error {
	switch dest.Address.Family() {
	case net.AddressFamilyIPv4:
		p := b.Extend(1 + 4 + 2)
		p[0] = uotAtypIPv4
		copy(p[1:5], dest.Address.IP().To4())
		binary.BigEndian.PutUint16(p[5:7], dest.Port.Value())
	case net.AddressFamilyIPv6:
		p := b.Extend(1 + 16 + 2)
		p[0] = uotAtypIPv6
		copy(p[1:17], dest.Address.IP().To16())
		binary.BigEndian.PutUint16(p[17:19], dest.Port.Value())
	case net.AddressFamilyDomain:
		domain := dest.Address.Domain()
		if len(domain) == 0 || len(domain) > 255 {
			return errors.New("anytls: invalid uot domain length: ", len(domain))
		}
		p := b.Extend(int32(1 + 1 + len(domain) + 2))
		p[0] = uotAtypDomain
		p[1] = byte(len(domain))
		copy(p[2:2+len(domain)], domain)
		binary.BigEndian.PutUint16(p[2+len(domain):], dest.Port.Value())
	default:
		return errors.New("anytls: unsupported uot address family")
	}
	return nil
}

func readUoTAddr(r io.Reader) (net.Destination, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return net.Destination{}, err
	}
	var addr net.Address
	switch atyp[0] {
	case uotAtypIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(r, ip[:]); err != nil {
			return net.Destination{}, err
		}
		addr = net.IPAddress(ip[:])
	case uotAtypIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(r, ip[:]); err != nil {
			return net.Destination{}, err
		}
		addr = net.IPAddress(ip[:])
	case uotAtypDomain:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return net.Destination{}, err
		}
		if l[0] == 0 {
			return net.Destination{}, errors.New("anytls: empty domain in uot address")
		}
		var domainBuf [255]byte
		domainSlice := domainBuf[:l[0]]
		if _, err := io.ReadFull(r, domainSlice); err != nil {
			return net.Destination{}, err
		}
		addr = net.ParseAddress(string(domainSlice))
	default:
		return net.Destination{}, errors.New("anytls: unknown uot address type: ", atyp[0])
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(r, portBuf[:]); err != nil {
		return net.Destination{}, err
	}
	return net.UDPDestination(addr, net.PortFromBytes(portBuf[:])), nil
}
