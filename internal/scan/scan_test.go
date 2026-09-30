package scan

import (
	"context"
	"net"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- subnet maths -----------------------------------------------------------

func TestHosts(t *testing.T) {
	got, err := Hosts("192.168.1.0/30")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.168.1.1", "192.168.1.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Hosts(/30) = %v, want %v (network and broadcast excluded)", got, want)
	}

	got, err = Hosts("10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 254 {
		t.Errorf("a /24 has %d usable hosts, want 254", len(got))
	}
	if got[0] != "10.0.0.1" || got[253] != "10.0.0.254" {
		t.Errorf("range is %s..%s, want 10.0.0.1..10.0.0.254", got[0], got[253])
	}
}

func TestHostsRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"", "not-a-cidr", "192.168.1.1", "::1/64"} {
		if _, err := Hosts(bad); err == nil {
			t.Errorf("Hosts(%q) should have failed", bad)
		}
	}
}

func TestHostCountAndRouterGuess(t *testing.T) {
	_, n, _ := net.ParseCIDR("192.168.4.0/24")
	if got := hostCount(n); got != 254 {
		t.Errorf("hostCount(/24) = %d, want 254", got)
	}
	if got := guessRouter(n); got != "192.168.4.1" {
		t.Errorf("guessRouter = %q, want 192.168.4.1", got)
	}
	_, n32, _ := net.ParseCIDR("10.1.2.3/32")
	if got := hostCount(n32); got != 0 {
		t.Errorf("hostCount(/32) = %d, want 0", got)
	}
}

func TestLocalInterfacesRejectsBadCIDR(t *testing.T) {
	if _, _, err := LocalInterfaces("999.1.1.1/8"); err == nil {
		t.Error("expected an error for a bogus --cidr")
	}
}

func TestLocalInterfacesHonoursOverride(t *testing.T) {
	ifs, notes, err := LocalInterfaces("192.168.50.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 1 || ifs[0].CIDR != "192.168.50.0/24" {
		t.Fatalf("got %+v, want a single 192.168.50.0/24 entry", ifs)
	}
	if ifs[0].Hosts != 254 {
		t.Errorf("Hosts = %d, want 254", ifs[0].Hosts)
	}
	// Scanning a range you were not already on deserves a warning.
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "allowed") {
		t.Errorf("an explicit --cidr should warn about permission; notes = %v", notes)
	}
}

// The real host must be classified without error, and must never include
// loopback (scanning 127.0.0.0/8 would be 16 million pointless probes).
func TestLocalInterfacesOnThisMachine(t *testing.T) {
	ifs, _, err := LocalInterfaces("")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range ifs {
		ip := net.ParseIP(in.IP)
		if ip == nil {
			t.Errorf("interface %s has unparseable IP %q", in.Name, in.IP)
		}
		if ip.IsLoopback() {
			t.Errorf("loopback interface %s must not be scanned", in.Name)
		}
		if in.Hosts <= 0 {
			t.Errorf("interface %s reports %d hosts", in.Name, in.Hosts)
		}
	}
}

// --- IP ordering ------------------------------------------------------------

func TestIPLessIsNumericNotLexical(t *testing.T) {
	in := []string{"192.168.1.10", "192.168.1.2", "192.168.1.100", "192.168.1.1"}
	sort.Slice(in, func(i, j int) bool { return ipLess(in[i], in[j]) })
	want := []string{"192.168.1.1", "192.168.1.2", "192.168.1.10", "192.168.1.100"}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("sorted = %v, want %v", in, want)
	}
}

// --- port parsing and lists --------------------------------------------------

func TestServiceNames(t *testing.T) {
	if ServiceName(8787) != "lrm" {
		t.Errorf("port 8787 should be recognised as lrm, got %q", ServiceName(8787))
	}
	if ServiceName(22) != "ssh" {
		t.Errorf("port 22 = %q, want ssh", ServiceName(22))
	}
	if ServiceName(64999) != "" {
		t.Errorf("unknown port should have no name, got %q", ServiceName(64999))
	}
}

func TestFullPortListIsBoundedAndSane(t *testing.T) {
	ps := FullPortList()
	if len(ps) < 10000 {
		t.Errorf("full list has %d ports, expected at least 10000", len(ps))
	}
	seen := map[int]bool{}
	for _, p := range ps {
		if p < 1 || p > 65535 {
			t.Fatalf("port %d out of range", p)
		}
		if seen[p] {
			t.Fatalf("port %d listed twice", p)
		}
		seen[p] = true
	}
	for _, must := range []int{22, 80, 443, 8787, 62078} {
		if !seen[must] {
			t.Errorf("full list is missing port %d", must)
		}
	}
}

func TestCommonPortsIncludesLRM(t *testing.T) {
	found := false
	for _, p := range CommonPorts() {
		if p == 8787 {
			found = true
		}
	}
	if !found {
		t.Error("the fast list must include LRM's own port")
	}
}

// --- MAC vendor lookup -------------------------------------------------------

func TestVendorOf(t *testing.T) {
	cases := map[string]string{
		"b8:27:eb:11:22:33": "Raspberry Pi",
		"B8-27-EB-11-22-33": "Raspberry Pi",
		"a4:83:e7:aa:bb:cc": "Apple",
		"52:54:00:12:34:56": "QEMU/KVM",
		"ff:ff:ff:ff:ff:ff": "",
		"":                  "",
		"zz":                "",
	}
	for mac, want := range cases {
		if got := VendorOf(mac); got != want {
			t.Errorf("VendorOf(%q) = %q, want %q", mac, got, want)
		}
	}
}

// Modern phones randomise their Wi-Fi MAC. Reporting "unknown vendor"
// would imply we failed; reporting "randomised" is the truth.
func TestRandomisedMACsAreLabelled(t *testing.T) {
	// Bit 1 of the first octet set => locally administered.
	for _, mac := range []string{"02:11:22:33:44:55", "b6:aa:bb:cc:dd:ee", "8e:00:00:00:00:01"} {
		if !IsRandomMAC(mac) {
			t.Errorf("%s should be detected as locally administered", mac)
		}
		if got := VendorOf(mac); got != "randomised MAC" {
			t.Errorf("VendorOf(%s) = %q, want %q", mac, got, "randomised MAC")
		}
	}
	for _, mac := range []string{"b8:27:eb:11:22:33", "a4:83:e7:00:00:00"} {
		if IsRandomMAC(mac) {
			t.Errorf("%s is a real OUI, not randomised", mac)
		}
	}
	// A known vendor that happens to live in the locally-administered
	// range must still be named, not written off as randomised.
	if got := VendorOf("52:54:00:12:34:56"); got != "QEMU/KVM" {
		t.Errorf("QEMU OUI = %q, want QEMU/KVM (a real OUI beats the LAA bit)", got)
	}
	// Broadcast is not a device.
	if got := VendorOf("ff:ff:ff:ff:ff:ff"); got != "" {
		t.Errorf("broadcast MAC = %q, want empty", got)
	}
}

// --- Wi-Fi helpers -----------------------------------------------------------

func TestBandAndChannel(t *testing.T) {
	cases := []struct {
		mhz  int
		band string
		ch   int
	}{
		{2412, "2.4GHz", 1},
		{2437, "2.4GHz", 6},
		{2462, "2.4GHz", 11},
		{2484, "2.4GHz", 14},
		{5180, "5GHz", 36},
		{5745, "5GHz", 149},
		{6135, "6GHz", 37},
	}
	for _, c := range cases {
		if got := BandOf(c.mhz); got != c.band {
			t.Errorf("BandOf(%d) = %q, want %q", c.mhz, got, c.band)
		}
		if got := ChannelOf(c.mhz); got != c.ch {
			t.Errorf("ChannelOf(%d) = %d, want %d", c.mhz, got, c.ch)
		}
	}
}

func TestSecurityOf(t *testing.T) {
	cases := map[string]string{
		"[WPA2-PSK-CCMP][ESS]":     "WPA2",
		"[WPA3-SAE-CCMP][ESS]":     "WPA3",
		"[RSN-PSK-CCMP][WPS][ESS]": "WPA2",
		"[WEP][ESS]":               "WEP (insecure)",
		"[ESS]":                    "open",
		"":                         "",
	}
	for caps, want := range cases {
		if got := SecurityOf(caps); got != want {
			t.Errorf("SecurityOf(%q) = %q, want %q", caps, got, want)
		}
	}
}

func TestSignalBars(t *testing.T) {
	if SignalBars(-45) != "****" {
		t.Error("a strong signal should show four bars")
	}
	if SignalBars(-85) != "." {
		t.Error("a very weak signal should show almost nothing")
	}
	if SignalBars(0) != "" {
		t.Error("an absent RSSI should render as empty")
	}
}

// --- neighbour table ----------------------------------------------------------

func TestReadNeighboursDoesNotExplode(t *testing.T) {
	// On a machine without /proc/net/arp, or with it restricted, this
	// must return cleanly rather than failing the scan.
	ns, note := ReadNeighbours()
	for _, n := range ns {
		if net.ParseIP(n.IP) == nil {
			t.Errorf("neighbour has bad IP %q", n.IP)
		}
		if n.MAC == "00:00:00:00:00:00" {
			t.Error("incomplete ARP entries must be skipped")
		}
	}
	_ = note
}

// --- discovery against a real listener -----------------------------------------

// TestProbeHostFindsARealListener starts a TCP server and checks the
// prober sees it. This is the core primitive of host discovery.
func TestProbeHostFindsARealListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port

	ctx := context.Background()
	if _, ok := probeHost(ctx, "127.0.0.1", []int{port}, time.Second); !ok {
		t.Error("prober did not find a listening port")
	}
	if !tcpOpen(ctx, "127.0.0.1", port, time.Second) {
		t.Error("tcpOpen did not find a listening port")
	}
}

// A closed port that answers RST still proves the host exists. This is how
// discovery works without ICMP, which needs root.
func TestRefusedConnectionCountsAsAlive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // now nothing is listening: connects get refused

	if _, ok := probeHost(context.Background(), "127.0.0.1", []int{port}, time.Second); !ok {
		t.Error("a refused connection should still identify the host as alive")
	}
}

func TestScanPortsFindsOpenPortsOnly(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	open := l.Addr().(*net.TCPAddr).Port

	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l2.Addr().(*net.TCPAddr).Port
	l2.Close()

	d := &Device{IP: "127.0.0.1"}
	opt := DefaultOptions()
	opt.Timeout = time.Second
	scanPorts(context.Background(), []*Device{d}, []int{open, closed}, opt)

	if len(d.Ports) != 1 || d.Ports[0].Number != open {
		t.Errorf("ports = %+v, want exactly the open port %d", d.Ports, open)
	}
}

// Scanning ourselves proves nothing and wastes time.
func TestScanPortsSkipsSelf(t *testing.T) {
	d := &Device{IP: "127.0.0.1", IsSelf: true}
	opt := DefaultOptions()
	opt.Timeout = 100 * time.Millisecond
	scanPorts(context.Background(), []*Device{d}, []int{22, 80}, opt)
	if len(d.Ports) != 0 {
		t.Errorf("self was scanned: %+v", d.Ports)
	}
}

// --- registry merging -----------------------------------------------------------

func TestRegistryMergesSources(t *testing.T) {
	r := newRegistry()
	r.add("192.168.1.5", "arp", func(d *Device) { d.MAC = "b8:27:eb:00:00:01" })
	r.add("192.168.1.5", "probe", func(d *Device) { d.RTT = "3ms" })
	r.add("192.168.1.5", "arp", nil) // duplicate source
	r.add("not-an-ip", "probe", nil)
	r.add("", "probe", nil)

	list := r.list()
	if len(list) != 1 {
		t.Fatalf("got %d devices, want 1 (bad addresses must be dropped)", len(list))
	}
	d := list[0]
	if d.MAC == "" || d.RTT == "" {
		t.Errorf("facts from both sources should be merged: %+v", d)
	}
	if len(d.Sources) != 2 {
		t.Errorf("sources = %v, want two distinct entries", d.Sources)
	}
}

// --- the whole thing, on this machine --------------------------------------------

// TestRunPassiveCompletes exercises the real entry point at the depth that
// sends (almost) nothing, so it is safe to run in CI.
func TestRunPassiveCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opt := DefaultOptions()
	opt.Depth = Passive
	opt.SkipWiFi = true
	opt.Budget = 20 * time.Second

	res, err := Run(ctx, opt)
	if err != nil {
		t.Skipf("no scannable network in this environment: %v", err)
	}
	if res.Duration == "" {
		t.Error("no duration recorded")
	}
	if res.Depth != Passive {
		t.Errorf("depth = %q, want passive", res.Depth)
	}
	// We must always find ourselves.
	foundSelf := false
	for _, d := range res.Devices {
		if d.IsSelf {
			foundSelf = true
		}
	}
	if !foundSelf && len(res.Interfaces) > 0 {
		t.Error("the scan did not include this device itself")
	}
	t.Logf("interfaces=%d devices=%d notes=%v in %s",
		len(res.Interfaces), len(res.Devices), res.Notes, res.Duration)
}

func TestRunRejectsBadCIDR(t *testing.T) {
	opt := DefaultOptions()
	opt.CIDR = "garbage"
	if _, err := Run(context.Background(), opt); err == nil {
		t.Error("expected an error for a bad --cidr")
	}
}

// A huge subnet must be refused rather than silently attempted.
func TestOversizedSubnetIsRefusedNotAttempted(t *testing.T) {
	opt := DefaultOptions()
	opt.CIDR = "10.0.0.0/8"
	opt.Depth = Fast
	opt.SkipWiFi = true
	opt.Budget = 10 * time.Second
	res, err := Run(context.Background(), opt)
	if err != nil {
		t.Skipf("environment refused the scan entirely: %v", err)
	}
	joined := strings.Join(res.Notes, " ")
	if !strings.Contains(joined, "more than the") {
		t.Errorf("a /8 should trip the MaxHosts guard; notes = %v", res.Notes)
	}
}

// TestManualCIDRDoesNotInventSelfOrGateway is the regression test for a bug
// found running the scanner for real: with --cidr 127.0.0.0/30 the network
// address 127.0.0.0 was labelled "this phone", because self-detection was
// reading the supplied range instead of the machine's own addresses.
func TestManualCIDRDoesNotInventSelfOrGateway(t *testing.T) {
	ifs, _, err := LocalInterfaces("192.168.77.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) != 1 {
		t.Fatalf("got %d interfaces, want 1", len(ifs))
	}
	if ifs[0].IP != "" {
		t.Errorf("manual range claimed a local IP %q; it cannot know one", ifs[0].IP)
	}
	if ifs[0].Router != "" {
		t.Errorf("manual range guessed a gateway %q; it cannot know one", ifs[0].Router)
	}
}

func TestLocalIPsIncludesLoopbackAndIsSane(t *testing.T) {
	ips := LocalIPs()
	if len(ips) == 0 {
		t.Skip("no addresses on this host")
	}
	if !ips["127.0.0.1"] {
		t.Error("LocalIPs should include loopback, so a --cidr over 127/8 marks self correctly")
	}
	for ip := range ips {
		if net.ParseIP(ip) == nil {
			t.Errorf("LocalIPs returned unparseable %q", ip)
		}
	}
}
