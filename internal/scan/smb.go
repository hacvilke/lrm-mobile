package scan

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"time"
	"unicode/utf16"
)

// A Windows machine with 445 open will tell you its name without any
// credentials. This matters on Android, where the ARP table is unreadable
// and a Windows host that has NetBIOS-over-TCP disabled — the default on
// many networks, and on Internet Connection Sharing interfaces — otherwise
// shows up as a bare IP with "135/msrpc, 445/smb" and nothing else.
//
// The mechanism is the NTLM handshake. We send an SMB2 NEGOTIATE, then a
// SESSION_SETUP carrying an NTLMSSP NEGOTIATE token. The server answers
// STATUS_MORE_PROCESSING_REQUIRED with an NTLMSSP CHALLENGE, and that
// message carries a TargetInfo block listing the NetBIOS computer name,
// the DNS computer name and the domain. No credentials are sent and no
// session is established: we read the challenge and hang up.
//
// This is the same unauthenticated disclosure every network scanner uses,
// and it is information the server volunteers to anyone who connects.

const (
	smbCmdNegotiate    = 0x0000
	smbCmdSessionSetup = 0x0001

	avEOL             = 0x0000
	avNbComputerName  = 0x0001
	avNbDomainName    = 0x0002
	avDNSComputerName = 0x0003
	avDNSDomainName   = 0x0004
)

// smbIdentity asks an SMB server who it is.
func smbIdentity(ctx context.Context, ip string, port int, timeout time.Duration) *identityInfo {
	if timeout < time.Second {
		timeout = time.Second
	}
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, itoa(port)))
	if err != nil {
		return nil
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))

	if _, err := c.Write(netbiosWrap(smb2Negotiate())); err != nil {
		return nil
	}
	if _, err := readNetbiosMessage(c); err != nil {
		return nil
	}
	if _, err := c.Write(netbiosWrap(smb2SessionSetup(ntlmNegotiate()))); err != nil {
		return nil
	}
	resp, err := readNetbiosMessage(c)
	if err != nil {
		return nil
	}
	return parseNTLMChallenge(resp)
}

// netbiosWrap prefixes the 4-byte NetBIOS session service header that SMB
// over TCP uses: one zero byte then a 24-bit big-endian length.
func netbiosWrap(payload []byte) []byte {
	n := len(payload)
	out := make([]byte, 4, 4+n)
	out[0] = 0x00
	out[1] = byte(n >> 16)
	out[2] = byte(n >> 8)
	out[3] = byte(n)
	return append(out, payload...)
}

func readNetbiosMessage(c net.Conn) ([]byte, error) {
	var hdr [4]byte
	if _, err := ioReadFull(c, hdr[:]); err != nil {
		return nil, err
	}
	n := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
	if n <= 0 || n > 1<<20 {
		return nil, errShortSMB
	}
	buf := make([]byte, n)
	if _, err := ioReadFull(c, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

type smbError string

func (e smbError) Error() string { return string(e) }

const errShortSMB = smbError("smb: implausible message length")

func ioReadFull(c net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := c.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// smb2Header builds the fixed 64-byte SMB2 header.
func smb2Header(command uint16, messageID uint64) []byte {
	h := make([]byte, 64)
	copy(h[0:4], []byte{0xFE, 'S', 'M', 'B'})
	binary.LittleEndian.PutUint16(h[4:6], 64) // StructureSize
	binary.LittleEndian.PutUint16(h[12:14], command)
	binary.LittleEndian.PutUint16(h[14:16], 1) // CreditRequest
	binary.LittleEndian.PutUint64(h[24:32], messageID)
	return h
}

// smb2Negotiate offers the dialects that do not require negotiate
// contexts. 3.1.1 is deliberately omitted: it would force us to append
// preauth-integrity and encryption contexts for no benefit here.
func smb2Negotiate() []byte {
	dialects := []uint16{0x0202, 0x0210, 0x0300, 0x0302}
	body := make([]byte, 36+2*len(dialects))
	binary.LittleEndian.PutUint16(body[0:2], 36) // StructureSize
	binary.LittleEndian.PutUint16(body[2:4], uint16(len(dialects)))
	binary.LittleEndian.PutUint16(body[4:6], 0x0001) // signing enabled
	for i, d := range dialects {
		binary.LittleEndian.PutUint16(body[36+2*i:], d)
	}
	return append(smb2Header(smbCmdNegotiate, 0), body...)
}

// smb2SessionSetup carries the NTLMSSP token in its security buffer.
func smb2SessionSetup(token []byte) []byte {
	const fixed = 24
	body := make([]byte, fixed)
	binary.LittleEndian.PutUint16(body[0:2], 25) // StructureSize (24 + 1 variable)
	body[3] = 0x01                               // SecurityMode: signing enabled
	binary.LittleEndian.PutUint16(body[12:14], 64+fixed)
	binary.LittleEndian.PutUint16(body[14:16], uint16(len(token)))
	out := append(smb2Header(smbCmdSessionSetup, 1), body...)
	return append(out, token...)
}

// ntlmNegotiate is an NTLMSSP type 1 message. REQUEST_TARGET is what makes
// the server include the TargetInfo block we are after.
func ntlmNegotiate() []byte {
	const (
		negotiateUnicode   = 0x00000001
		requestTarget      = 0x00000004
		negotiateNTLM      = 0x00000200
		alwaysSign         = 0x00008000
		extendedSessionSec = 0x00080000
		negotiateVersion   = 0x02000000
	)
	flags := uint32(negotiateUnicode | requestTarget | negotiateNTLM |
		alwaysSign | extendedSessionSec | negotiateVersion)

	m := make([]byte, 32)
	copy(m[0:8], "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(m[8:12], 1) // type 1
	binary.LittleEndian.PutUint32(m[12:16], flags)
	// Domain and workstation fields stay empty.
	return m
}

// parseNTLMChallenge finds the NTLMSSP type 2 message anywhere in the
// response and extracts the names from its TargetInfo block.
//
// Scanning for the signature rather than walking the SMB structures is
// deliberate: servers differ in whether they wrap the token in SPNEGO, and
// the signature is unambiguous.
func parseNTLMChallenge(b []byte) *identityInfo {
	i := indexOf(b, []byte("NTLMSSP\x00"))
	if i < 0 {
		return nil
	}
	m := b[i:]
	if len(m) < 48 {
		return nil
	}
	if binary.LittleEndian.Uint32(m[8:12]) != 2 { // must be CHALLENGE
		return nil
	}

	tiLen := int(binary.LittleEndian.Uint16(m[40:42]))
	tiOff := int(binary.LittleEndian.Uint32(m[44:48]))
	if tiLen <= 0 || tiOff < 0 || tiOff+tiLen > len(m) {
		// No TargetInfo: fall back to the target name field.
		if n := utf16Field(m, 12); n != "" {
			return &identityInfo{Name: n, Source: "smb", Services: []string{"smb"}}
		}
		return nil
	}

	info := &identityInfo{Source: "smb", Services: []string{"smb"}}
	var nbName, dnsName, domain string
	p := m[tiOff : tiOff+tiLen]
	for len(p) >= 4 {
		id := binary.LittleEndian.Uint16(p[0:2])
		l := int(binary.LittleEndian.Uint16(p[2:4]))
		if id == avEOL || 4+l > len(p) {
			break
		}
		v := decodeUTF16LE(p[4 : 4+l])
		switch id {
		case avNbComputerName:
			nbName = v
		case avDNSComputerName:
			dnsName = v
		case avNbDomainName, avDNSDomainName:
			if domain == "" {
				domain = v
			}
		}
		p = p[4+l:]
	}

	switch {
	case dnsName != "":
		info.Name = dnsName
	case nbName != "":
		info.Name = nbName
	}
	if info.Name == "" {
		return nil
	}
	if domain != "" && !strings.EqualFold(domain, info.Name) {
		info.Services = append(info.Services, "domain:"+domain)
	}
	info.OS = "Windows/SMB"
	return info
}

// utf16Field reads a (len, maxlen, offset) descriptor at off and decodes
// the UTF-16LE string it points at.
func utf16Field(m []byte, off int) string {
	if off+8 > len(m) {
		return ""
	}
	l := int(binary.LittleEndian.Uint16(m[off : off+2]))
	o := int(binary.LittleEndian.Uint32(m[off+4 : off+8]))
	if l <= 0 || o < 0 || o+l > len(m) {
		return ""
	}
	return decodeUTF16LE(m[o : o+l])
}

func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00")
}

func indexOf(hay, needle []byte) int {
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j := range needle {
			if hay[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
