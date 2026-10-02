package scan

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// On Android 10 and later the ARP table is unreadable, so a scan from a
// phone gets no MAC addresses and therefore no vendor names. Confirmed on
// Android 16: every MAC column comes back empty.
//
// Identity does not have to come from the MAC. It can come from the device
// itself, and asking is both more accurate and more interesting than an OUI
// lookup — an OUI tells you a board was made by Realtek, whereas the device
// will tell you it is a "Brother HL-L2350DW" or "OpenSSH 9.6 on Ubuntu".
//
// Four permission-free sources, in rough order of how much they reveal:
//
//	SSDP/UPnP   a multicast M-SEARCH, then fetch the description XML:
//	            friendly name, manufacturer, model. Routers, TVs, speakers,
//	            printers, NAS boxes and consoles nearly all answer.
//	NetBIOS     a node-status query on UDP 137: the workstation name of
//	            Windows machines and Samba servers.
//	TLS         the certificate on 443: common name and SANs, which on
//	            embedded devices is usually the model or hostname.
//	Banners     the greeting on an open port: SSH announces its version and
//	            often the distribution; HTTP returns a Server header and a
//	            page title.
//
// All of it is information the device publishes to anyone who asks on the
// local network, and none of it needs root, ARP or netlink.

// identify enriches devices in place, using whatever each one will tell us.
func identify(ctx context.Context, devices []*Device, opt Options) {
	byIP := map[string]*Device{}
	for _, d := range devices {
		if !d.IsSelf {
			byIP[d.IP] = d
		}
	}
	if len(byIP) == 0 {
		return
	}

	// SSDP is a single multicast question answered by many devices, so it
	// runs once for the whole network rather than per host.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ip, info := range ssdpDiscover(ctx, 3*time.Second) {
			if d, ok := byIP[ip]; ok {
				d.mergeIdentity(info)
			}
		}
	}()

	// The rest are per-host and run in parallel, bounded.
	sem := make(chan struct{}, 32)
	for _, d := range byIP {
		wg.Add(1)
		go func(d *Device) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if n := netbiosName(ctx, d.IP, opt.Timeout); n != "" && d.Hostname == "" {
				d.Hostname = n
				d.addSource("netbios")
			}
			for _, p := range d.Ports {
				if ctx.Err() != nil {
					return
				}
				if info := grabBanner(ctx, d.IP, p.Number, opt.Timeout); info != nil {
					d.mergeIdentity(info)
				}
			}
		}(d)
	}
	wg.Wait()
}

// identityInfo is what one probe learned.
type identityInfo struct {
	Name     string // friendly/host name
	Vendor   string // manufacturer
	Model    string
	OS       string
	Source   string
	Services []string
}

func (d *Device) mergeIdentity(i *identityInfo) {
	if i == nil {
		return
	}
	if d.Hostname == "" && i.Name != "" {
		d.Hostname = i.Name
	}
	if d.Vendor == "" && i.Vendor != "" {
		d.Vendor = i.Vendor
	}
	if d.Model == "" && i.Model != "" {
		d.Model = i.Model
	}
	if d.OS == "" && i.OS != "" {
		d.OS = i.OS
	}
	for _, s := range i.Services {
		if s != "" && !contains(d.Services, s) {
			d.Services = append(d.Services, s)
		}
	}
	if i.Source != "" {
		d.addSource(i.Source)
	}
}

func (d *Device) addSource(s string) {
	if !contains(d.Sources, s) {
		d.Sources = append(d.Sources, s)
	}
}

// ---------------------------------------------------------------- SSDP ---

const ssdpAddr = "239.255.255.250:1900"

// ssdpDiscover multicasts an M-SEARCH and collects answers, then fetches
// each responder's description document for the human-readable fields.
func ssdpDiscover(ctx context.Context, wait time.Duration) map[string]*identityInfo {
	out := map[string]*identityInfo{}

	raddr, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return out
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return out
	}
	defer conn.Close()

	req := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: " + ssdpAddr + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 2\r\n" +
		"ST: ssdp:all\r\n\r\n"
	// Multicast is lossy; ask a few times.
	for i := 0; i < 3; i++ {
		if _, err := conn.WriteToUDP([]byte(req), raddr); err != nil {
			break
		}
		time.Sleep(120 * time.Millisecond)
	}

	deadline := time.Now().Add(wait)
	_ = conn.SetReadDeadline(deadline)
	locations := map[string]string{} // ip -> LOCATION url
	servers := map[string]string{}   // ip -> SERVER header
	buf := make([]byte, 8192)
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		ip := src.IP.String()
		hdr := parseSSDPHeaders(string(buf[:n]))
		if l := hdr["location"]; l != "" {
			if _, seen := locations[ip]; !seen {
				locations[ip] = l
			}
		}
		if s := hdr["server"]; s != "" {
			servers[ip] = s
		}
	}

	// Fetch the description documents concurrently.
	var mu sync.Mutex
	var wg sync.WaitGroup
	for ip, loc := range locations {
		wg.Add(1)
		go func(ip, loc string) {
			defer wg.Done()
			info := fetchUPnPDescription(ctx, loc)
			if info == nil {
				info = &identityInfo{}
			}
			info.Source = "ssdp"
			if info.OS == "" {
				info.OS = tidyServer(servers[ip])
			}
			mu.Lock()
			out[ip] = info
			mu.Unlock()
		}(ip, loc)
	}
	// Responders with no LOCATION still told us their SERVER string.
	for ip, s := range servers {
		if _, ok := locations[ip]; ok {
			continue
		}
		out[ip] = &identityInfo{OS: tidyServer(s), Source: "ssdp"}
	}
	wg.Wait()
	return out
}

func parseSSDPHeaders(s string) map[string]string {
	h := map[string]string{}
	for _, line := range strings.Split(s, "\r\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		h[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return h
}

// upnpDevice is the subset of the UPnP description document we want.
type upnpDevice struct {
	Device struct {
		FriendlyName string `xml:"friendlyName"`
		Manufacturer string `xml:"manufacturer"`
		ModelName    string `xml:"modelName"`
		ModelNumber  string `xml:"modelNumber"`
		DeviceType   string `xml:"deviceType"`
	} `xml:"device"`
}

func fetchUPnPDescription(ctx context.Context, loc string) *identityInfo {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, "GET", loc, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil
	}
	var d upnpDevice
	if xml.Unmarshal(body, &d) != nil {
		return nil
	}
	model := strings.TrimSpace(d.Device.ModelName)
	if n := strings.TrimSpace(d.Device.ModelNumber); n != "" && !strings.Contains(model, n) {
		model = strings.TrimSpace(model + " " + n)
	}
	return &identityInfo{
		Name:     strings.TrimSpace(d.Device.FriendlyName),
		Vendor:   strings.TrimSpace(d.Device.Manufacturer),
		Model:    model,
		Services: []string{shortDeviceType(d.Device.DeviceType)},
	}
}

// shortDeviceType turns urn:schemas-upnp-org:device:MediaRenderer:1 into
// "MediaRenderer".
func shortDeviceType(t string) string {
	parts := strings.Split(t, ":")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
}

func tidyServer(s string) string {
	if s == "" {
		return ""
	}
	// "Linux/3.10 UPnP/1.0 Sonos/70.3" -> keep it short.
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// ------------------------------------------------------------- NetBIOS ---

// netbiosName asks UDP 137 for a node status report, which Windows
// machines and Samba servers answer with their workstation name.
func netbiosName(ctx context.Context, ip string, timeout time.Duration) string {
	if timeout < 400*time.Millisecond {
		timeout = 400 * time.Millisecond
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(ip, "137"))
	if err != nil {
		return ""
	}
	defer conn.Close()

	if _, err := conn.Write(netbiosNodeStatusQuery()); err != nil {
		return ""
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	return parseNetbiosNodeStatus(buf[:n])
}

// netbiosNodeStatusQuery builds the classic "*" NBSTAT request.
func netbiosNodeStatusQuery() []byte {
	q := []byte{
		0x13, 0x37, // transaction id
		0x00, 0x00, // flags
		0x00, 0x01, // questions
		0x00, 0x00, // answers
		0x00, 0x00, // authority
		0x00, 0x00, // additional
	}
	// The name "*" padded with NULs to 16 bytes, first-level encoded:
	// each byte becomes two characters in the range 'A'..'P'.
	name := make([]byte, 16)
	name[0] = '*'
	q = append(q, 0x20) // encoded length
	for _, b := range name {
		q = append(q, 'A'+(b>>4), 'A'+(b&0x0f))
	}
	q = append(q, 0x00)       // root label
	q = append(q, 0x00, 0x21) // type NBSTAT
	q = append(q, 0x00, 0x01) // class IN
	return q
}

// parseNetbiosNodeStatus pulls the first unique workstation name out of a
// node status response.
func parseNetbiosNodeStatus(b []byte) string {
	// header(12) + echoed question(34 name + 4) + rr header(2+2+4+2)
	const off = 12 + 34 + 4 + 2 + 2 + 4 + 2
	if len(b) < off+1 {
		return ""
	}
	count := int(b[off])
	p := off + 1
	for i := 0; i < count; i++ {
		if p+18 > len(b) {
			return ""
		}
		raw := b[p : p+15]
		suffix := b[p+15]
		flags := uint16(b[p+16])<<8 | uint16(b[p+17])
		p += 18

		group := flags&0x8000 != 0
		name := strings.TrimRight(string(raw), " \x00")
		// suffix 0x00 on a unique name is the workstation name.
		if suffix == 0x00 && !group && name != "" && isPrintableName(name) {
			return name
		}
	}
	return ""
}

func isPrintableName(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return len(s) > 0
}

// ------------------------------------------------------------- banners ---

// grabBanner asks an open port what it is.
func grabBanner(ctx context.Context, ip string, port int, timeout time.Duration) *identityInfo {
	if timeout < 700*time.Millisecond {
		timeout = 700 * time.Millisecond
	}
	switch port {
	case 22, 2222:
		return sshBanner(ctx, ip, port, timeout)
	case 445, 139:
		// A Windows host that has NetBIOS-over-TCP disabled will not
		// answer UDP 137, but still names itself over SMB.
		return smbIdentity(ctx, ip, port, timeout)
	case 443, 8443, 9443:
		return tlsIdentity(ctx, ip, port, timeout)
	case 80, 81, 8000, 8008, 8080, 8081, 8888, 9000, 5000, 631, 8123, 8006:
		return httpIdentity(ctx, ip, port, timeout)
	}
	return nil
}

func sshBanner(ctx context.Context, ip string, port int, timeout time.Duration) *identityInfo {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, itoa(port)))
	if err != nil {
		return nil
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(io.LimitReader(c, 512)).ReadString('\n')
	if err != nil && line == "" {
		return nil
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "SSH-") {
		return nil
	}
	// "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5"
	info := &identityInfo{Source: "banner", Services: []string{"ssh"}}
	if _, rest, ok := strings.Cut(line, "-"); ok {
		if _, sw, ok := strings.Cut(rest, "-"); ok {
			info.OS = trimTo(sw, 40)
		}
	}
	return info
}

func httpIdentity(ctx context.Context, ip string, port int, timeout time.Duration) *identityInfo {
	d := net.Dialer{Timeout: timeout}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, itoa(port)))
	if err != nil {
		return nil
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	fmt.Fprintf(c, "GET / HTTP/1.0\r\nHost: %s\r\nUser-Agent: lrm-scan\r\nConnection: close\r\n\r\n", ip)
	body, err := io.ReadAll(io.LimitReader(c, 32*1024))
	if err != nil && len(body) == 0 {
		return nil
	}
	text := string(body)
	info := &identityInfo{Source: "banner"}
	for _, line := range strings.Split(text, "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "server") {
			info.OS = trimTo(strings.TrimSpace(v), 40)
			break
		}
		if line == "" {
			break // end of headers
		}
	}
	if t := htmlTitle(text); t != "" {
		info.Name = trimTo(t, 40)
	}
	if info.OS == "" && info.Name == "" {
		return nil
	}
	return info
}

func htmlTitle(s string) string {
	l := strings.ToLower(s)
	i := strings.Index(l, "<title")
	if i < 0 {
		return ""
	}
	j := strings.Index(l[i:], ">")
	if j < 0 {
		return ""
	}
	start := i + j + 1
	k := strings.Index(l[start:], "</title>")
	if k < 0 {
		return ""
	}
	return strings.Join(strings.Fields(s[start:start+k]), " ")
}

func tlsIdentity(ctx context.Context, ip string, port int, timeout time.Duration) *identityInfo {
	d := &net.Dialer{Timeout: timeout}
	c, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(ip, itoa(port)),
		// We are reading the certificate to identify the device, not
		// trusting it. Verification is meaningless for a self-signed
		// embedded device and would simply hide the information.
		&tls.Config{InsecureSkipVerify: true}) // #nosec G402
	if err != nil {
		return nil
	}
	defer c.Close()
	st := c.ConnectionState()
	if len(st.PeerCertificates) == 0 {
		return nil
	}
	cert := st.PeerCertificates[0]
	info := &identityInfo{Source: "tls", Services: []string{"https"}}
	if cn := strings.TrimSpace(cert.Subject.CommonName); cn != "" && cn != ip {
		info.Name = trimTo(cn, 40)
	}
	if o := cert.Subject.Organization; len(o) > 0 && o[0] != "" {
		info.Vendor = trimTo(o[0], 24)
	}
	for _, n := range cert.DNSNames {
		if n != "" && info.Name == "" {
			info.Name = trimTo(n, 40)
		}
	}
	if info.Name == "" && info.Vendor == "" {
		return nil
	}
	return info
}

func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
