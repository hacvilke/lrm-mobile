package scan

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Android 11 and later block NETLINK_ROUTE for ordinary apps. Go's
// net.Interfaces() and net.InterfaceAddrs() are implemented on top of a
// netlink RIB dump, so on a modern Android device they fail outright:
//
//	route ip+net: netlinkrib: permission denied
//
// This is a platform restriction, not something a caller can ask for
// differently, and it is not going to be lifted. Reported on an Android 16
// device running `lrm scan`.
//
// So everything here answers the same questions without netlink:
//
//	/proc/net/route   the routing table as text: interface, destination,
//	                  netmask and gateway. Gives a real gateway rather
//	                  than the conventional .1 guess.
//	a connected UDP   socket to an off-link address. connect(2) on UDP
//	                  sends no packets; the kernel just picks a source
//	                  address, which is this device's IP on the route that
//	                  would be used. Works with no permissions at all.
//
// Together those reconstruct what net.Interfaces() would have told us.
// Neither needs root and neither touches netlink.

// procRoute is one parsed line of /proc/net/route.
type procRoute struct {
	Iface   string
	Dest    net.IP
	Mask    net.IPMask
	Gateway net.IP
}

// ParseProcNetRoute parses the kernel's IPv4 routing table from a reader.
//
// The numeric columns are hex in *host* byte order, which on every platform
// Android runs on is little-endian, so 0000A8C0 is 192.168.0.0.
func ParseProcNetRoute(r *bufio.Scanner) []procRoute {
	var out []procRoute
	first := true
	for r.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(r.Text())
		if len(f) < 8 {
			continue
		}
		dest, ok1 := hexLEtoIP(f[1])
		gw, ok2 := hexLEtoIP(f[2])
		mask, ok3 := hexLEtoIP(f[7])
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		out = append(out, procRoute{
			Iface:   f[0],
			Dest:    dest,
			Gateway: gw,
			Mask:    net.IPMask(mask.To4()),
		})
	}
	return out
}

// hexLEtoIP converts a /proc/net/route hex field to an IPv4 address.
func hexLEtoIP(s string) (net.IP, bool) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, false
	}
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(v))
	return net.IPv4(b[0], b[1], b[2], b[3]), true
}

// readProcNetRoute reads the real file.
func readProcNetRoute() ([]procRoute, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseProcNetRoute(bufio.NewScanner(f)), nil
}

// LocalIPViaUDP returns this device's source address for off-link traffic,
// without sending anything and without any permission.
//
// A connected UDP socket performs no handshake: connect(2) only fixes the
// peer and makes the kernel select a source address, which is exactly the
// value we want. The address dialled is never contacted.
func LocalIPViaUDP() net.IP {
	for _, probe := range []string{"8.8.8.8:80", "1.1.1.1:80", "192.0.2.1:80"} {
		c, err := net.Dial("udp4", probe)
		if err != nil {
			continue
		}
		ua, ok := c.LocalAddr().(*net.UDPAddr)
		c.Close()
		if ok && ua.IP != nil && !ua.IP.IsUnspecified() && ua.IP.To4() != nil {
			return ua.IP.To4()
		}
	}
	return nil
}

// interfacesWithoutNetlink reconstructs the scannable networks when
// net.Interfaces() is unavailable.
//
// The second return value is a note describing what was used, because a
// scan built from a fallback should say so rather than quietly looking the
// same as a full one.
func interfacesWithoutNetlink() ([]Interface, string) {
	self := LocalIPViaUDP()

	routes, err := readProcNetRoute()
	routeReadable := err == nil
	if routeReadable && len(routes) > 0 {
		var out []Interface
		gw := map[string]string{}
		for _, r := range routes { // default routes carry the gateway
			if isZero(r.Mask) && !r.Gateway.Equal(net.IPv4zero) {
				gw[r.Iface] = r.Gateway.String()
			}
		}
		seen := map[string]bool{}
		for _, r := range routes {
			if isZero(r.Mask) {
				continue // default route: no subnet to scan
			}
			ones, bits := r.Mask.Size()
			if bits != 32 || ones >= 31 || ones == 0 {
				continue
			}
			n := &net.IPNet{IP: r.Dest.Mask(r.Mask), Mask: r.Mask}
			if n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
				continue
			}
			cidr := n.String()
			if seen[cidr] {
				continue
			}
			seen[cidr] = true

			ip := ""
			if self != nil && n.Contains(self) {
				ip = self.String()
			}
			out = append(out, Interface{
				Name:   r.Iface,
				IP:     ip,
				CIDR:   cidr,
				Hosts:  hostCount(n),
				Router: gw[r.Iface],
			})
		}
		if len(out) > 0 {
			return out, "netlink is blocked on this Android version — " +
				"networks read from /proc/net/route instead (interface MAC addresses are unavailable)"
		}
	}

	// Last resort: we know our own address but not the prefix. Assume the
	// /24 that covers virtually every home and office Wi-Fi, and say so.
	why := "/proc/net/route is unreadable"
	if routeReadable {
		why = "/proc/net/route listed no scannable network"
	}
	if self != nil {
		m := net.CIDRMask(24, 32)
		n := &net.IPNet{IP: self.Mask(m), Mask: m}
		return []Interface{{
				Name:   "net0?",
				IP:     self.String(),
				CIDR:   n.String(),
				Hosts:  hostCount(n),
				Router: guessRouter(n),
			}}, "netlink is blocked and " + why + " — assuming a /24 around " +
				self.String() + " and guessing the gateway (pass --cidr if that is wrong)"
	}

	return nil, "netlink is blocked, " + why +
		", and no local address could be determined — pass --cidr to scan a range explicitly"
}

func isZero(m net.IPMask) bool {
	ones, _ := m.Size()
	return ones == 0
}

// localIPsWithoutNetlink is the self-identification fallback.
func localIPsWithoutNetlink() map[string]bool {
	out := map[string]bool{"127.0.0.1": true}
	if ip := LocalIPViaUDP(); ip != nil {
		out[ip.String()] = true
	}
	return out
}

// netlinkBlocked recognises the error Android returns.
func netlinkBlocked(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "netlinkrib") ||
		strings.Contains(s, "route ip+net") ||
		strings.Contains(s, "permission denied")
}

var _ = fmt.Sprintf // keep fmt for future diagnostics
