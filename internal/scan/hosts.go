package scan

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lrm-project/lrm/internal/mdns"
)

// LocalInterfaces returns the IPv4 networks this device is joined to.
//
// Loopback, down interfaces and point-to-point tunnels are skipped: the
// question "what is near my phone" means the broadcast domains it shares
// with other machines, which in practice is Wi-Fi (wlan0) and occasionally
// a USB tether (rndis0).
func LocalInterfaces(overrideCIDR string) ([]Interface, []string, error) {
	var notes []string

	if overrideCIDR != "" {
		ip, ipnet, err := net.ParseCIDR(overrideCIDR)
		if err != nil {
			return nil, notes, fmt.Errorf("bad --cidr %q: %w", overrideCIDR, err)
		}
		if ipnet.IP.To4() == nil {
			return nil, notes, fmt.Errorf("--cidr must be IPv4 for now")
		}
		notes = append(notes, "scanning an explicitly supplied range — make sure you are allowed to")
		_ = ip
		// No IP and no Router: a manually supplied range says nothing
		// about which address is ours or which is the gateway, and
		// guessing would mislabel the network address as "this phone".
		return []Interface{{
			Name:  "manual",
			CIDR:  ipnet.String(),
			Hosts: hostCount(ipnet),
		}}, notes, nil
	}

	ifs, err := net.Interfaces()
	if err != nil {
		return nil, notes, fmt.Errorf("cannot list network interfaces: %w", err)
	}

	var out []Interface
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
				continue
			}
			ones, bits := ipnet.Mask.Size()
			if bits != 32 || ones >= 31 {
				continue // /31 and /32 have no neighbours to find
			}
			out = append(out, Interface{
				Name:   ifc.Name,
				IP:     ipnet.IP.String(),
				CIDR:   (&net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask}).String(),
				Hosts:  hostCount(ipnet),
				MAC:    ifc.HardwareAddr.String(),
				Router: guessRouter(ipnet),
			})
		}
	}
	if len(out) == 0 {
		notes = append(notes, "no IPv4 LAN interface is up — on mobile data there is no local network to scan")
	}
	return out, notes, nil
}

func hostCount(n *net.IPNet) int {
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return 0
	}
	h := 1 << uint(bits-ones)
	if h <= 2 {
		return 0
	}
	return h - 2 // network and broadcast
}

// guessRouter returns the conventional gateway address (.1) for a subnet.
// It is a guess, labelled as one: Android does not let an unprivileged
// process read the routing table reliably.
func guessRouter(n *net.IPNet) string {
	ip := n.IP.Mask(n.Mask).To4()
	if ip == nil {
		return ""
	}
	g := make(net.IP, 4)
	copy(g, ip)
	g[3]++
	return g.String()
}

// LocalIPs returns every IPv4 address this machine holds, including
// loopback. Self-identification must come from here rather than from the
// interface list being scanned: with --cidr the scanned range has nothing
// to do with our own addresses.
func LocalIPs() map[string]bool {
	out := map[string]bool{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out[n.IP.String()] = true
			}
		}
	}
	return out
}

// Hosts enumerates the usable addresses of a CIDR.
func Hosts(cidr string) ([]string, error) {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ip := n.IP.Mask(n.Mask).To4()
	if ip == nil {
		return nil, fmt.Errorf("not IPv4: %s", cidr)
	}
	ones, bits := n.Mask.Size()
	count := 1 << uint(bits-ones)
	out := make([]string, 0, count)
	cur := make(net.IP, 4)
	copy(cur, ip)
	for i := 0; i < count; i++ {
		if i != 0 && i != count-1 { // skip network + broadcast
			c := make(net.IP, 4)
			copy(c, cur)
			out = append(out, c.String())
		}
		inc(cur)
	}
	return out, nil
}

func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

// Neighbour is one entry from the kernel's ARP/neighbour table.
type Neighbour struct {
	IP     string
	MAC    string
	Device string
}

// ReadNeighbours reads /proc/net/arp.
//
// This is free information about devices that are already talking on the
// network, including ones that answer no TCP port at all. It is also
// restricted on Android 10+ for ordinary apps — when that bites, we say so
// rather than pretending the network is empty.
func ReadNeighbours() ([]Neighbour, string) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		if os.IsPermission(err) {
			return nil, "cannot read /proc/net/arp (restricted on Android 10+) — relying on active probes for discovery"
		}
		return nil, ""
	}
	defer f.Close()

	var out []Neighbour
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		ip, mac, dev := fields[0], fields[3], fields[5]
		if mac == "00:00:00:00:00:00" {
			continue // incomplete entry
		}
		out = append(out, Neighbour{IP: ip, MAC: strings.ToLower(mac), Device: dev})
	}
	if len(out) == 0 {
		return nil, "the neighbour table is empty — nothing has talked to this device recently"
	}
	return out, ""
}

// probePorts are the ports used to decide "is anything here". They are
// chosen to cover the widest range of device types with the fewest
// connections: a router, a printer, a laptop, a NAS, an IoT plug and
// another phone running LRM will each answer at least one.
func probePorts() []int {
	return []int{80, 443, 22, 8787, 445, 139, 5555, 62078, 9100, 7000, 8080, 53, 3389, 548, 631}
}

// discoverHosts finds addresses that answer a TCP connection.
//
// There is no ICMP here. A ping needs a raw socket, which needs root, which
// LRM Mobile refuses to require. A TCP SYN to a closed port still proves a
// host exists — the kernel replies RST — so "connection refused" counts as
// alive just as much as a successful connect.
func discoverHosts(ctx context.Context, ifaces []Interface, opt Options) map[string]time.Duration {
	type job struct{ ip string }

	live := struct {
		sync.Mutex
		m map[string]time.Duration
	}{m: map[string]time.Duration{}}

	jobs := make(chan job, opt.Concurrency)
	ports := probePorts()

	var wg sync.WaitGroup
	for i := 0; i < opt.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				if rtt, ok := probeHost(ctx, j.ip, ports, opt.Timeout); ok {
					live.Lock()
					if cur, seen := live.m[j.ip]; !seen || rtt < cur {
						live.m[j.ip] = rtt
					}
					live.Unlock()
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, in := range ifaces {
			hosts, err := Hosts(in.CIDR)
			if err != nil {
				continue
			}
			for _, h := range hosts {
				select {
				case <-ctx.Done():
					return
				case jobs <- job{ip: h}:
				}
			}
		}
	}()

	wg.Wait()
	return live.m
}

// probeHost reports whether anything is at ip, and how long the fastest
// answer took. A refused connection is an answer.
func probeHost(ctx context.Context, ip string, ports []int, timeout time.Duration) (time.Duration, bool) {
	for _, p := range ports {
		if ctx.Err() != nil {
			return 0, false
		}
		start := time.Now()
		d := net.Dialer{Timeout: timeout}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, itoa(p)))
		rtt := time.Since(start)
		if err == nil {
			conn.Close()
			return rtt, true
		}
		// "connection refused" means a host answered with RST: it is
		// alive. A timeout means silence, which proves nothing.
		if isRefused(err) {
			return rtt, true
		}
	}
	return 0, false
}

func isRefused(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "refused") || strings.Contains(s, "reset")
}

// browseLRM wraps upstream's mDNS browser so a failure to discover is not
// a failure to scan.
func browseLRM(ctx context.Context, d time.Duration) []mdns.Peer {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	peers, err := mdns.Browse(c, d)
	if err != nil {
		return nil
	}
	return peers
}

func localHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
