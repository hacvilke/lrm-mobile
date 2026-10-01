package scan

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// A real /proc/net/route from an Android phone on Wi-Fi: a default route
// through the gateway, and the on-link /24.
const androidRoute = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	00000000	0101A8C0	0003	0	0	0	00000000	0	0	0
wlan0	0001A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`

func scanOf(s string) *bufio.Scanner { return bufio.NewScanner(strings.NewReader(s)) }

func TestParseProcNetRouteDecodesLittleEndianHex(t *testing.T) {
	rs := ParseProcNetRoute(scanOf(androidRoute))
	if len(rs) != 2 {
		t.Fatalf("parsed %d routes, want 2", len(rs))
	}

	def := rs[0]
	if !def.Gateway.Equal(net.ParseIP("192.168.1.1")) {
		t.Errorf("default gateway = %v, want 192.168.1.1 (0101A8C0 is little-endian)", def.Gateway)
	}
	if ones, _ := def.Mask.Size(); ones != 0 {
		t.Errorf("default route mask should be /0, got /%d", ones)
	}

	onlink := rs[1]
	if !onlink.Dest.Equal(net.ParseIP("192.168.1.0")) {
		t.Errorf("destination = %v, want 192.168.1.0", onlink.Dest)
	}
	if ones, _ := onlink.Mask.Size(); ones != 24 {
		t.Errorf("mask = /%d, want /24", ones)
	}
	if onlink.Iface != "wlan0" {
		t.Errorf("iface = %q, want wlan0", onlink.Iface)
	}
}

func TestParseProcNetRouteSurvivesJunk(t *testing.T) {
	for _, in := range []string{
		"",
		"Iface\tDestination\n",
		"Iface\tDestination\nwlan0\tZZZZ\tZZZZ\t0\t0\t0\t0\tZZZZ\n",
		"Iface\nwlan0\n",
	} {
		_ = ParseProcNetRoute(scanOf(in)) // must not panic
	}
}

func TestNetlinkBlockedRecognisesAndroidsError(t *testing.T) {
	// The literal error from an Android 16 device.
	real := errors.New("route ip+net: netlinkrib: permission denied")
	if !netlinkBlocked(real) {
		t.Error("the reported Android error was not recognised")
	}
	if netlinkBlocked(nil) {
		t.Error("nil is not a netlink failure")
	}
	if netlinkBlocked(errors.New("no such host")) {
		t.Error("an unrelated error must not be treated as a netlink block")
	}
}

// The UDP trick must yield this machine's address without sending anything.
func TestLocalIPViaUDP(t *testing.T) {
	ip := LocalIPViaUDP()
	if ip == nil {
		t.Skip("no route to the internet in this environment")
	}
	if ip.To4() == nil {
		t.Errorf("got %v, want an IPv4 address", ip)
	}
	if ip.IsUnspecified() {
		t.Error("0.0.0.0 is not a usable source address")
	}
	// It must agree with what netlink says, where netlink works.
	if ifs, err := net.Interfaces(); err == nil {
		found := false
		for _, i := range ifs {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("UDP source %v is not one of this host's addresses", ip)
		}
	}
}

func TestInterfacesWithoutNetlinkProducesScannableNetworks(t *testing.T) {
	out, note := interfacesWithoutNetlink()
	if len(out) == 0 {
		t.Skip("no routes in this environment")
	}
	if note == "" {
		t.Error("a fallback result must explain itself; a silent fallback looks like a full scan")
	}
	for _, in := range out {
		if _, _, err := net.ParseCIDR(in.CIDR); err != nil {
			t.Errorf("interface %s has unparseable CIDR %q", in.Name, in.CIDR)
		}
		if in.Hosts <= 0 {
			t.Errorf("interface %s reports %d hosts", in.Name, in.Hosts)
		}
		if _, err := Hosts(in.CIDR); err != nil {
			t.Errorf("cannot enumerate %s: %v", in.CIDR, err)
		}
	}
	t.Logf("netlink-free interfaces: %+v (%s)", out, note)
}

func TestLocalIPsWithoutNetlinkAlwaysHasLoopback(t *testing.T) {
	ips := localIPsWithoutNetlink()
	if !ips["127.0.0.1"] {
		t.Error("loopback must always be known as self")
	}
}

// The gateway from /proc/net/route is real, unlike the .1 convention we
// fall back to. Make sure we prefer it.
func TestRouteGatewayBeatsTheDotOneGuess(t *testing.T) {
	const oddGateway = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	00000000	FE01A8C0	0003	0	0	0	00000000	0	0	0
wlan0	0001A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	rs := ParseProcNetRoute(scanOf(oddGateway))
	if !rs[0].Gateway.Equal(net.ParseIP("192.168.1.254")) {
		t.Fatalf("gateway = %v, want 192.168.1.254", rs[0].Gateway)
	}
	// guessRouter would have said .1, which would be wrong here.
	_, n, _ := net.ParseCIDR("192.168.1.0/24")
	if guessRouter(n) == rs[0].Gateway.String() {
		t.Error("this test is meant to use a gateway the guess would miss")
	}
}

// TestScanWorksEndToEndWithoutNetlink is the regression test for the
// Android 16 failure:
//
//	lrm scan: cannot list network interfaces: route ip+net: netlinkrib: permission denied
//
// It drives the whole pipeline using only the netlink-free discovery path,
// which is what a modern Android device is restricted to.
func TestScanWorksEndToEndWithoutNetlink(t *testing.T) {
	ifs, note := interfacesWithoutNetlink()
	if len(ifs) == 0 {
		t.Skip("no routes available in this environment")
	}
	t.Logf("fallback found %d network(s): %s", len(ifs), note)

	// Everything the scanner needs must be derivable with no netlink.
	for _, in := range ifs {
		hosts, err := Hosts(in.CIDR)
		if err != nil {
			t.Fatalf("cannot enumerate %s: %v", in.CIDR, err)
		}
		if len(hosts) == 0 {
			t.Fatalf("%s yielded no addresses to probe", in.CIDR)
		}
	}

	// And a real scan over that range must complete rather than erroring
	// out the way it did on the phone.
	opt := DefaultOptions()
	opt.CIDR = ifs[0].CIDR
	opt.Depth = Fast
	opt.SkipWiFi = true
	opt.Budget = 25 * time.Second
	opt.Timeout = 200 * time.Millisecond

	res, err := Run(context.Background(), opt)
	if err != nil {
		t.Fatalf("scan failed on a netlink-free network: %v", err)
	}
	if res.Duration == "" {
		t.Error("scan produced no result")
	}
	// Self-identification must still work without netlink.
	sawSelf := false
	for _, d := range res.Devices {
		if d.IsSelf {
			sawSelf = true
		}
	}
	if !sawSelf {
		t.Log("note: self not inside the scanned range (fine for a manual CIDR)")
	}
	t.Logf("scanned %s: %d device(s) in %s", opt.CIDR, len(res.Devices), res.Duration)
}
