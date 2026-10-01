package scan

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// --- NetBIOS -----------------------------------------------------------

func TestNetbiosQueryIsWellFormed(t *testing.T) {
	q := netbiosNodeStatusQuery()
	if len(q) != 12+1+32+1+2+2 {
		t.Fatalf("query is %d bytes, want %d", len(q), 12+1+32+1+2+2)
	}
	if q[4] != 0x00 || q[5] != 0x01 {
		t.Error("question count must be 1")
	}
	if q[12] != 0x20 {
		t.Errorf("encoded name length = %#x, want 0x20", q[12])
	}
	// "*" is 0x2A, first-level encoded as 'C','K'.
	if q[13] != 'C' || q[14] != 'K' {
		t.Errorf("encoded name starts %q%q, want \"CK\" for '*'", q[13], q[14])
	}
	// Padding NULs encode as "AA".
	if q[15] != 'A' || q[16] != 'A' {
		t.Error("NUL padding must encode as 'AA'")
	}
	tail := q[len(q)-4:]
	if tail[0] != 0x00 || tail[1] != 0x21 {
		t.Error("query type must be NBSTAT (0x0021)")
	}
	if tail[2] != 0x00 || tail[3] != 0x01 {
		t.Error("query class must be IN")
	}
}

// buildNodeStatusResponse synthesises what a Windows box sends back.
func buildNodeStatusResponse(names []struct {
	name   string
	suffix byte
	group  bool
}) []byte {
	b := make([]byte, 12+34+4+2+2+4+2)
	b[0], b[1] = 0x13, 0x37
	b = append(b, byte(len(names)))
	for _, n := range names {
		padded := n.name + strings.Repeat(" ", 15-len(n.name))
		b = append(b, []byte(padded[:15])...)
		b = append(b, n.suffix)
		var flags uint16
		if n.group {
			flags |= 0x8000
		}
		b = append(b, byte(flags>>8), byte(flags))
	}
	return b
}

func TestParseNetbiosNodeStatus(t *testing.T) {
	type entry = struct {
		name   string
		suffix byte
		group  bool
	}
	// A realistic reply: workgroup (group), then the workstation name.
	resp := buildNodeStatusResponse([]entry{
		{"WORKGROUP", 0x00, true},    // group — must be skipped
		{"DESKTOP-ABC", 0x00, false}, // unique workstation name — want this
		{"DESKTOP-ABC", 0x20, false}, // file server service
	})
	if got := parseNetbiosNodeStatus(resp); got != "DESKTOP-ABC" {
		t.Errorf("got %q, want DESKTOP-ABC", got)
	}
}

func TestParseNetbiosRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, 60)} {
		if got := parseNetbiosNodeStatus(b); got != "" {
			t.Errorf("garbage produced a name: %q", got)
		}
	}
}

func TestIsPrintableName(t *testing.T) {
	if !isPrintableName("PRINTER-1") {
		t.Error("a normal name should be accepted")
	}
	if isPrintableName("bad\x01name") {
		t.Error("control characters must be rejected")
	}
	if isPrintableName("") {
		t.Error("empty is not a name")
	}
}

// --- SSDP --------------------------------------------------------------

func TestParseSSDPHeaders(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"LOCATION: http://192.168.1.1:5000/rootDesc.xml\r\n" +
		"SERVER: Linux/3.10 UPnP/1.0 MiniUPnPd/2.1\r\n" +
		"ST: upnp:rootdevice\r\n\r\n"
	h := parseSSDPHeaders(raw)
	if h["location"] != "http://192.168.1.1:5000/rootDesc.xml" {
		t.Errorf("location = %q", h["location"])
	}
	if !strings.Contains(h["server"], "MiniUPnPd") {
		t.Errorf("server = %q", h["server"])
	}
}

func TestShortDeviceType(t *testing.T) {
	cases := map[string]string{
		"urn:schemas-upnp-org:device:MediaRenderer:1":         "MediaRenderer",
		"urn:schemas-upnp-org:device:InternetGatewayDevice:1": "InternetGatewayDevice",
		"":         "",
		"nonsense": "",
	}
	for in, want := range cases {
		if got := shortDeviceType(in); got != want {
			t.Errorf("shortDeviceType(%q) = %q, want %q", in, got, want)
		}
	}
}

// A real UPnP description document, trimmed.
const upnpXML = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
    <friendlyName>Living Room Router</friendlyName>
    <manufacturer>ASUSTeK Computer Inc.</manufacturer>
    <modelName>RT-AX88U</modelName>
    <modelNumber>3.0.0.4</modelNumber>
  </device>
</root>`

func TestFetchUPnPDescription(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			_, _ = c.Read(buf)
			resp := "HTTP/1.0 200 OK\r\nContent-Type: text/xml\r\n\r\n" + upnpXML
			_, _ = c.Write([]byte(resp))
			c.Close()
		}
	}()

	url := "http://" + ln.Addr().String() + "/rootDesc.xml"
	info := fetchUPnPDescription(context.Background(), url)
	if info == nil {
		t.Fatal("no info parsed")
	}
	if info.Name != "Living Room Router" {
		t.Errorf("name = %q", info.Name)
	}
	if info.Vendor != "ASUSTeK Computer Inc." {
		t.Errorf("vendor = %q", info.Vendor)
	}
	if info.Model != "RT-AX88U 3.0.0.4" {
		t.Errorf("model = %q, want model name plus number", info.Model)
	}
	if len(info.Services) == 0 || info.Services[0] != "InternetGatewayDevice" {
		t.Errorf("services = %v", info.Services)
	}
}

// --- banners -------------------------------------------------------------

// serveOnce answers one connection with the given bytes, after optionally
// reading a request.
func serveOnce(t *testing.T, reply string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = c.Write([]byte(reply))
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSSHBannerExtractsSoftware(t *testing.T) {
	port := serveOnce(t, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5\r\n")
	info := sshBanner(context.Background(), "127.0.0.1", port, time.Second)
	if info == nil {
		t.Fatal("no banner parsed")
	}
	if !strings.Contains(info.OS, "OpenSSH_9.6p1") {
		t.Errorf("OS = %q, want the OpenSSH version", info.OS)
	}
	if len(info.Services) == 0 || info.Services[0] != "ssh" {
		t.Errorf("services = %v", info.Services)
	}
}

func TestSSHBannerIgnoresNonSSH(t *testing.T) {
	port := serveOnce(t, "220 mail.example.com ESMTP Postfix\r\n")
	if info := sshBanner(context.Background(), "127.0.0.1", port, time.Second); info != nil {
		t.Errorf("a non-SSH greeting was parsed as SSH: %+v", info)
	}
}

func TestHTTPIdentityReadsServerAndTitle(t *testing.T) {
	port := serveOnce(t,
		"HTTP/1.0 200 OK\r\nServer: nginx/1.24.0 (Ubuntu)\r\nContent-Type: text/html\r\n\r\n"+
			"<html><head><title>  Brother  HL-L2350DW </title></head><body>x</body></html>")
	info := httpIdentity(context.Background(), "127.0.0.1", port, 2*time.Second)
	if info == nil {
		t.Fatal("no info parsed")
	}
	if info.OS != "nginx/1.24.0 (Ubuntu)" {
		t.Errorf("server = %q", info.OS)
	}
	if info.Name != "Brother HL-L2350DW" {
		t.Errorf("title = %q, want whitespace collapsed", info.Name)
	}
}

func TestHTMLTitle(t *testing.T) {
	cases := map[string]string{
		"<title>Hi</title>":                  "Hi",
		"<TITLE>Caps</TITLE>":                "Caps",
		`<title lang="en">Attr</title>`:      "Attr",
		"<title>\n  multi\n  line\n</title>": "multi line",
		"no title here":                      "",
		"<title>unterminated":                "",
	}
	for in, want := range cases {
		if got := htmlTitle(in); got != want {
			t.Errorf("htmlTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrimTo(t *testing.T) {
	if got := trimTo("  spaced  ", 20); got != "spaced" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 50)
	got := trimTo(long, 10)
	if len([]rune(got)) != 10 {
		t.Errorf("trimTo produced %d runes, want 10", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncation should be marked: %q", got)
	}
}

// --- merging ---------------------------------------------------------------

func TestMergeIdentityDoesNotOverwriteBetterData(t *testing.T) {
	d := &Device{IP: "1.2.3.4", Hostname: "already-known", Vendor: "Apple"}
	d.mergeIdentity(&identityInfo{Name: "worse", Vendor: "Generic", Model: "X1", Source: "ssdp"})
	if d.Hostname != "already-known" {
		t.Errorf("hostname was overwritten: %q", d.Hostname)
	}
	if d.Vendor != "Apple" {
		t.Errorf("vendor was overwritten: %q", d.Vendor)
	}
	if d.Model != "X1" {
		t.Errorf("model should have been filled: %q", d.Model)
	}
	if !contains(d.Sources, "ssdp") {
		t.Error("source not recorded")
	}
	d.mergeIdentity(nil) // must not panic
}

func TestMergeIdentityDeduplicatesServices(t *testing.T) {
	d := &Device{IP: "1.2.3.4"}
	d.mergeIdentity(&identityInfo{Services: []string{"ssh", ""}})
	d.mergeIdentity(&identityInfo{Services: []string{"ssh", "https"}})
	if len(d.Services) != 2 {
		t.Errorf("services = %v, want ssh and https once each", d.Services)
	}
}

func TestIdentifySkipsSelf(t *testing.T) {
	d := &Device{IP: "127.0.0.1", IsSelf: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	opt := DefaultOptions()
	opt.Timeout = 100 * time.Millisecond
	identify(ctx, []*Device{d}, opt)
	if d.Hostname != "" || d.Model != "" {
		t.Errorf("self should not be probed: %+v", d)
	}
}
