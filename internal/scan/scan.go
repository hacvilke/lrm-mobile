// Package scan finds what is around the phone: the devices sharing its
// Wi-Fi, and the Wi-Fi networks within radio range.
//
// It exists because a phone is usually the only computer in the room that
// is already on the network you care about, and `lrm peers` only ever shows
// machines that are already running LRM. When a sync is not working, the
// first question is "is the other machine even here", and that is a
// question about the network, not about LRM.
//
// Design constraints, all of them consequences of running unprivileged on
// Android:
//
//   - No raw sockets, so no real ICMP ping and no ARP requests of our own.
//     Host discovery is TCP connect probes plus whatever the kernel's
//     neighbour table already knows.
//   - /proc/net/arp is restricted on Android 10+. We read it when we can
//     and shrug when we cannot.
//   - Wi-Fi scanning is not something a process can do itself; it goes
//     through termux-api, which needs the Location permission, and Android
//     rate-limits it to a few scans per two minutes.
//
// Scanning is confined to the subnets this device is actually joined to.
// You cannot point it at an arbitrary range without saying so explicitly,
// because a tool that sweeps networks you are not on is a different kind of
// tool than this one.
package scan

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Depth controls how hard we look.
type Depth string

const (
	// Passive reads the neighbour table and listens for mDNS. It sends
	// almost nothing.
	Passive Depth = "passive"
	// Fast probes a handful of common ports per address to find live
	// hosts, then stops.
	Fast Depth = "fast"
	// Full does Fast to find live hosts, then sweeps the full port range
	// on the hosts that answered. Two-phase on purpose: sweeping 65535
	// ports across a whole /24 blind would take hours on a phone and
	// most of it would be addresses where nothing lives.
	Full Depth = "full"
)

// Device is one machine found on the local network.
type Device struct {
	IP       string   `json:"ip"`
	MAC      string   `json:"mac,omitempty"`
	Vendor   string   `json:"vendor,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Ports    []Port   `json:"ports,omitempty"`
	Sources  []string `json:"sources"` // how we learned about it
	IsSelf   bool     `json:"is_self,omitempty"`
	IsRouter bool     `json:"is_router,omitempty"`
	LRM      *LRMInfo `json:"lrm,omitempty"`
	RTT      string   `json:"rtt,omitempty"`
}

// Port is one open TCP port.
type Port struct {
	Number  int    `json:"port"`
	Service string `json:"service,omitempty"`
}

// LRMInfo is set when the device is running LRM.
type LRMInfo struct {
	PeerID    string `json:"peer_id,omitempty"`
	User      string `json:"user,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Port      int    `json:"port,omitempty"`
}

// Network is one Wi-Fi access point in range.
type Network struct {
	SSID      string `json:"ssid"`
	BSSID     string `json:"bssid,omitempty"`
	RSSI      int    `json:"rssi,omitempty"` // dBm
	Frequency int    `json:"frequency_mhz,omitempty"`
	Channel   int    `json:"channel,omitempty"`
	Band      string `json:"band,omitempty"`
	Security  string `json:"security,omitempty"`
	Connected bool   `json:"connected,omitempty"`
}

// Result is everything one run found.
type Result struct {
	Interfaces []Interface `json:"interfaces"`
	Devices    []Device    `json:"devices"`
	Networks   []Network   `json:"networks,omitempty"`
	// Notes explain anything that could not be done, e.g. a restricted
	// neighbour table or a missing termux-api. A scan that silently
	// returns less than it should is worse than one that says why.
	Notes    []string `json:"notes,omitempty"`
	Duration string   `json:"duration"`
	Depth    Depth    `json:"depth"`
}

// Interface is a local network interface worth scanning.
type Interface struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	CIDR   string `json:"cidr"`
	Hosts  int    `json:"hosts"`
	MAC    string `json:"mac,omitempty"`
	Router string `json:"router,omitempty"`
}

// Options configures a run.
type Options struct {
	Depth Depth
	// CIDR overrides the automatically detected subnets. Supplying it is
	// the explicit act of scanning something you were not already on.
	CIDR string
	// Timeout is the per-connection dial timeout.
	Timeout time.Duration
	// Budget is the wall-clock ceiling for the whole scan.
	Budget time.Duration
	// Concurrency is the number of in-flight connections.
	Concurrency int
	// Ports overrides the port list for the deep phase.
	Ports []int
	// SkipWiFi disables the termux-api Wi-Fi survey.
	SkipWiFi bool
	// MaxHosts refuses to scan a subnet larger than this many addresses,
	// so that a /8 on some corporate VPN does not silently become a
	// week-long job.
	MaxHosts int
	// Progress, if set, is called with human-readable status lines.
	Progress func(string)
}

// DefaultOptions are tuned for a phone: enough concurrency to finish a /24
// quickly, short enough timeouts that a silent host does not stall the run,
// and a hard budget so the command always returns.
func DefaultOptions() Options {
	return Options{
		Depth:       Full,
		Timeout:     600 * time.Millisecond,
		Budget:      3 * time.Minute,
		Concurrency: 256,
		MaxHosts:    4096,
	}
}

func (o *Options) normalize() {
	d := DefaultOptions()
	if o.Depth == "" {
		o.Depth = d.Depth
	}
	if o.Timeout <= 0 {
		o.Timeout = d.Timeout
	}
	if o.Budget <= 0 {
		o.Budget = d.Budget
	}
	if o.Concurrency <= 0 {
		o.Concurrency = d.Concurrency
	}
	if o.MaxHosts <= 0 {
		o.MaxHosts = d.MaxHosts
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
}

// Run performs a scan.
func Run(ctx context.Context, opt Options) (*Result, error) {
	opt.normalize()
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, opt.Budget)
	defer cancel()

	res := &Result{Depth: opt.Depth}

	// --- which networks are we on? -----------------------------------
	ifaces, notes, err := LocalInterfaces(opt.CIDR)
	res.Notes = append(res.Notes, notes...)
	if err != nil {
		return nil, err
	}
	if len(ifaces) == 0 {
		return nil, fmt.Errorf("no usable IPv4 network found — is Wi-Fi on? (mobile data alone gives no LAN to scan)")
	}
	res.Interfaces = ifaces

	// --- collect candidate addresses ---------------------------------
	found := newRegistry()

	// Neighbour table: free, instant, and often the only way to see a
	// device that refuses every TCP probe.
	arp, arpNote := ReadNeighbours()
	if arpNote != "" {
		res.Notes = append(res.Notes, arpNote)
	}
	for _, n := range arp {
		found.add(n.IP, "arp", func(d *Device) {
			d.MAC = n.MAC
			d.Vendor = VendorOf(n.MAC)
		})
	}

	// Our own addresses come from the machine, not from the range being
	// scanned: with --cidr those are unrelated.
	self := LocalIPs()
	for ip := range self {
		found.add(ip, "self", func(d *Device) {
			d.IsSelf = true
			if d.Hostname == "" {
				d.Hostname = localHostname()
			}
		})
	}
	for _, in := range ifaces {
		if in.Router != "" {
			found.add(in.Router, "gateway", func(d *Device) { d.IsRouter = true })
		}
	}

	// mDNS: names, and LRM peers specifically.
	opt.Progress("listening for mDNS announcements...")
	for _, p := range browseLRM(ctx, 3*time.Second) {
		p := p
		// A peer may announce several addresses (Wi-Fi, tether, VPN).
		// Record it against each, so the one on our subnet lines up
		// with what the probe phase finds.
		addrs := p.Addrs
		if len(addrs) == 0 {
			if h, _, err := net.SplitHostPort(p.Addr()); err == nil {
				addrs = []string{h}
			}
		}
		for _, host := range addrs {
			found.add(host, "mdns", func(d *Device) {
				d.LRM = &LRMInfo{
					PeerID:    p.PeerHex,
					User:      p.User,
					Workspace: p.WS,
					Port:      p.Port,
				}
			})
		}
	}

	// --- host discovery ----------------------------------------------
	if opt.Depth != Passive {
		total := 0
		for _, in := range ifaces {
			total += in.Hosts
		}
		if total > opt.MaxHosts {
			res.Notes = append(res.Notes, fmt.Sprintf(
				"subnet has %d addresses, more than the %d limit — scanning the neighbour table only (use --cidr to narrow it)",
				total, opt.MaxHosts))
		} else {
			opt.Progress(fmt.Sprintf("probing %d addresses...", total))
			live := discoverHosts(ctx, ifaces, opt)
			for ip, rtt := range live {
				found.add(ip, "probe", func(d *Device) {
					if d.RTT == "" {
						d.RTT = rtt.Round(time.Millisecond).String()
					}
				})
			}
		}
	}

	devices := found.list()

	// --- deep port scan on hosts that answered ------------------------
	if opt.Depth == Full && len(devices) > 0 {
		ports := opt.Ports
		if len(ports) == 0 {
			ports = FullPortList()
		}
		opt.Progress(fmt.Sprintf("port-scanning %d live host(s) across %d ports...", len(devices), len(ports)))
		scanPorts(ctx, devices, ports, opt)
	} else if opt.Depth == Fast {
		scanPorts(ctx, devices, CommonPorts(), opt)
	}

	// --- names ---------------------------------------------------------
	resolveNames(ctx, devices)

	// --- identify LRM by port even without mDNS -------------------------
	for _, d := range devices {
		if d.LRM != nil {
			continue
		}
		for _, p := range d.Ports {
			if p.Service == "lrm" {
				d.LRM = &LRMInfo{Port: p.Number}
				break
			}
		}
	}

	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		sort.Slice(d.Ports, func(i, j int) bool { return d.Ports[i].Number < d.Ports[j].Number })
		sort.Strings(d.Sources)
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	res.Devices = out

	// --- Wi-Fi survey ----------------------------------------------------
	if !opt.SkipWiFi {
		nets, note := WiFiScan(ctx)
		res.Networks = nets
		if note != "" {
			res.Notes = append(res.Notes, note)
		}
	}

	if ctx.Err() != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("stopped at the %s time budget — results are partial (raise it with --budget)", opt.Budget))
	}
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

// registry accumulates devices keyed by IP, merging facts from each source.
type registry struct {
	byIP map[string]*Device
}

func newRegistry() *registry { return &registry{byIP: map[string]*Device{}} }

func (r *registry) add(ip, source string, fill func(*Device)) {
	ip = strings.TrimSpace(ip)
	if ip == "" || net.ParseIP(ip) == nil {
		return
	}
	d, ok := r.byIP[ip]
	if !ok {
		d = &Device{IP: ip}
		r.byIP[ip] = d
	}
	if !contains(d.Sources, source) {
		d.Sources = append(d.Sources, source)
	}
	if fill != nil {
		fill(d)
	}
}

func (r *registry) list() []*Device {
	out := make([]*Device, 0, len(r.byIP))
	for _, d := range r.byIP {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ipLess orders IPv4 addresses numerically, so .2 comes before .10.
func ipLess(a, b string) bool {
	ai, bi := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ai == nil || bi == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if ai[i] != bi[i] {
			return ai[i] < bi[i]
		}
	}
	return false
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
