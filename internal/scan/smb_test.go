package scan

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func avPair(id uint16, s string) []byte {
	v := utf16le(s)
	b := make([]byte, 4+len(v))
	binary.LittleEndian.PutUint16(b[0:2], id)
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(v)))
	copy(b[4:], v)
	return b
}

// buildChallenge synthesises an NTLMSSP type 2 message the way a Windows
// server sends one, optionally with junk in front (SPNEGO wrapping, SMB
// headers) to prove we locate it by signature.
func buildChallenge(prefix []byte, target string, pairs []byte) []byte {
	tn := utf16le(target)
	const hdr = 56
	m := make([]byte, hdr)
	copy(m[0:8], "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(m[8:12], 2) // CHALLENGE

	tnOff := hdr
	binary.LittleEndian.PutUint16(m[12:14], uint16(len(tn)))
	binary.LittleEndian.PutUint16(m[14:16], uint16(len(tn)))
	binary.LittleEndian.PutUint32(m[16:20], uint32(tnOff))

	tiOff := tnOff + len(tn)
	binary.LittleEndian.PutUint16(m[40:42], uint16(len(pairs)))
	binary.LittleEndian.PutUint16(m[42:44], uint16(len(pairs)))
	binary.LittleEndian.PutUint32(m[44:48], uint32(tiOff))

	m = append(m, tn...)
	m = append(m, pairs...)
	return append(prefix, m...)
}

func TestParseNTLMChallengeExtractsComputerName(t *testing.T) {
	pairs := append(avPair(avNbDomainName, "WORKGROUP"), avPair(avNbComputerName, "DESKTOP-7QK2P1")...)
	pairs = append(pairs, avPair(avDNSComputerName, "DESKTOP-7QK2P1.local")...)
	pairs = append(pairs, 0, 0, 0, 0) // MsvAvEOL

	// Prefix with plausible SMB/SPNEGO noise.
	msg := buildChallenge([]byte{0xFE, 'S', 'M', 'B', 0x40, 0x00, 0xa1, 0x82}, "WORKGROUP", pairs)

	info := parseNTLMChallenge(msg)
	if info == nil {
		t.Fatal("no identity parsed from a valid challenge")
	}
	if info.Name != "DESKTOP-7QK2P1.local" {
		t.Errorf("name = %q, want the DNS computer name", info.Name)
	}
	if info.OS != "Windows/SMB" {
		t.Errorf("OS = %q", info.OS)
	}
	joined := strings.Join(info.Services, ",")
	if !strings.Contains(joined, "smb") {
		t.Errorf("services = %v", info.Services)
	}
	if !strings.Contains(joined, "domain:WORKGROUP") {
		t.Errorf("domain not reported: %v", info.Services)
	}
}

func TestParseNTLMChallengeFallsBackToNetBIOSName(t *testing.T) {
	pairs := append(avPair(avNbComputerName, "FILESERVER"), 0, 0, 0, 0)
	info := parseNTLMChallenge(buildChallenge(nil, "DOM", pairs))
	if info == nil || info.Name != "FILESERVER" {
		t.Fatalf("got %+v, want the NetBIOS name when there is no DNS name", info)
	}
}

func TestParseNTLMChallengeRejectsNonChallenge(t *testing.T) {
	// A type 1 message must not be mistaken for a challenge.
	if info := parseNTLMChallenge(ntlmNegotiate()); info != nil {
		t.Errorf("a NEGOTIATE message was parsed as a CHALLENGE: %+v", info)
	}
	for _, b := range [][]byte{nil, {}, []byte("no signature here"), make([]byte, 100)} {
		if info := parseNTLMChallenge(b); info != nil {
			t.Errorf("garbage parsed: %+v", info)
		}
	}
}

func TestParseNTLMChallengeSurvivesTruncation(t *testing.T) {
	pairs := append(avPair(avNbComputerName, "HOST"), 0, 0, 0, 0)
	full := buildChallenge(nil, "D", pairs)
	for n := 1; n < len(full); n++ {
		_ = parseNTLMChallenge(full[:n]) // must never panic
	}
}

func TestParseNTLMChallengeRejectsOutOfRangeOffsets(t *testing.T) {
	pairs := append(avPair(avNbComputerName, "HOST"), 0, 0, 0, 0)
	m := buildChallenge(nil, "D", pairs)
	// Point TargetInfo far beyond the buffer.
	binary.LittleEndian.PutUint32(m[44:48], 0xFFFF)
	_ = parseNTLMChallenge(m) // must not panic or read out of bounds
}

func TestSMBWireFormat(t *testing.T) {
	neg := smb2Negotiate()
	if len(neg) < 64+36 {
		t.Fatalf("negotiate is %d bytes, too short", len(neg))
	}
	if string(neg[0:4]) != "\xfeSMB" {
		t.Error("missing SMB2 protocol id")
	}
	if binary.LittleEndian.Uint16(neg[4:6]) != 64 {
		t.Error("header StructureSize must be 64")
	}
	if binary.LittleEndian.Uint16(neg[12:14]) != smbCmdNegotiate {
		t.Error("wrong command")
	}
	if binary.LittleEndian.Uint16(neg[64:66]) != 36 {
		t.Error("negotiate StructureSize must be 36")
	}

	ss := smb2SessionSetup(ntlmNegotiate())
	if binary.LittleEndian.Uint16(ss[64:66]) != 25 {
		t.Error("session setup StructureSize must be 25")
	}
	off := binary.LittleEndian.Uint16(ss[64+12 : 64+14])
	l := binary.LittleEndian.Uint16(ss[64+14 : 64+16])
	if int(off)+int(l) != len(ss) {
		t.Errorf("security buffer (off=%d len=%d) does not match message length %d", off, l, len(ss))
	}
	if string(ss[off:off+8]) != "NTLMSSP\x00" {
		t.Error("security buffer does not contain the NTLMSSP token")
	}
}

func TestNetbiosWrapLength(t *testing.T) {
	w := netbiosWrap([]byte("hello"))
	if w[0] != 0x00 {
		t.Error("session service message type must be 0")
	}
	n := int(w[1])<<16 | int(w[2])<<8 | int(w[3])
	if n != 5 || len(w) != 9 {
		t.Errorf("length prefix = %d, frame = %d bytes", n, len(w))
	}
}

func TestDecodeUTF16LE(t *testing.T) {
	if got := decodeUTF16LE(utf16le("DESKTOP-1")); got != "DESKTOP-1" {
		t.Errorf("got %q", got)
	}
	if got := decodeUTF16LE(append(utf16le("X"), 0x41)); got != "X" {
		t.Errorf("odd-length input mishandled: %q", got)
	}
	if got := decodeUTF16LE(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

// End to end against a fake SMB server that performs the handshake.
func TestSMBIdentityAgainstFakeServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	pairs := append(avPair(avNbComputerName, "WINBOX"), avPair(avNbDomainName, "WORKGROUP")...)
	pairs = append(pairs, 0, 0, 0, 0)
	challenge := buildChallenge(nil, "WORKGROUP", pairs)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		// negotiate
		if _, err := readNetbiosMessage(c); err != nil {
			return
		}
		_, _ = c.Write(netbiosWrap(append(smb2Header(smbCmdNegotiate, 0), make([]byte, 64)...)))
		// session setup
		if _, err := readNetbiosMessage(c); err != nil {
			return
		}
		body := append(smb2Header(smbCmdSessionSetup, 1), challenge...)
		_, _ = c.Write(netbiosWrap(body))
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	info := smbIdentity(context.Background(), "127.0.0.1", port, 3*time.Second)
	if info == nil {
		t.Fatal("no identity from the fake SMB server")
	}
	if info.Name != "WINBOX" {
		t.Errorf("name = %q, want WINBOX", info.Name)
	}
}

func TestSMBIdentityOnDeadPortReturnsNil(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if info := smbIdentity(context.Background(), "127.0.0.1", port, 500*time.Millisecond); info != nil {
		t.Errorf("got %+v from a closed port", info)
	}
}
