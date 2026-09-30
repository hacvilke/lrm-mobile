package scan

import (
	"context"
	"net"
	"sync"
	"time"
)

// services names the ports worth recognising on a home or office network.
// It is deliberately short: a long copy of /etc/services would be noise,
// and the point of the list is to let someone glance at the output and
// understand what a device is.
var services = map[int]string{
	21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns",
	80: "http", 81: "http-alt", 110: "pop3", 111: "rpcbind", 123: "ntp",
	135: "msrpc", 139: "netbios", 143: "imap", 443: "https", 445: "smb",
	465: "smtps", 515: "printer", 548: "afp", 554: "rtsp", 587: "smtp",
	631: "ipp", 993: "imaps", 995: "pop3s",
	1080: "socks", 1433: "mssql", 1723: "pptp", 1883: "mqtt", 1900: "ssdp",
	2049: "nfs", 2222: "ssh-alt", 3000: "dev-http", 3306: "mysql",
	3389: "rdp", 4444: "metasploit?", 5000: "upnp/http", 5001: "http-alt",
	5060: "sip", 5353: "mdns", 5432: "postgres", 5555: "adb",
	5900: "vnc", 6000: "x11", 6379: "redis", 7000: "airplay",
	8000: "http-alt", 8006: "proxmox", 8008: "chromecast", 8009: "chromecast",
	8080: "http-proxy", 8081: "http-alt", 8123: "home-assistant",
	8443: "https-alt", 8787: "lrm", 8888: "http-alt", 9000: "http-alt",
	9090: "http-alt", 9100: "printer-raw", 9200: "elasticsearch",
	11211: "memcached", 27017: "mongodb", 32400: "plex",
	49152: "upnp", 62078: "iphone-sync",
}

// ServiceName returns a friendly name for a port, or "".
func ServiceName(p int) string { return services[p] }

// CommonPorts is the Fast-depth list: the ports that identify what a device
// is, without sweeping.
func CommonPorts() []int {
	out := make([]int, 0, len(services))
	for p := range services {
		out = append(out, p)
	}
	return out
}

// FullPortList is the Full-depth list.
//
// It is 1-10000 plus the well-known high ports, not 1-65535. Sweeping the
// entire space from a phone means 65535 connections per host; at any
// concurrency that keeps a phone's radio and battery sane, that is tens of
// minutes per host, and essentially everything a home network runs is below
// 10000 or in the short tail below. If you genuinely need every port, pass
// --ports 1-65535 and accept the wait.
func FullPortList() []int {
	out := make([]int, 0, 10000+32)
	for p := 1; p <= 10000; p++ {
		out = append(out, p)
	}
	for _, p := range []int{
		11211, 16992, 20000, 25565, 27017, 27018, 32400, 32768, 32769,
		49152, 49153, 49154, 49155, 49156, 49157, 51820, 62078, 65535,
	} {
		out = append(out, p)
	}
	return out
}

// scanPorts fills in the open ports of each device, in place.
//
// Work is spread across devices rather than finishing one host at a time,
// so that a single unresponsive machine cannot hold up the whole scan and
// so that no one host sees a burst of thousands of connections at once.
func scanPorts(ctx context.Context, devices []*Device, ports []int, opt Options) {
	type job struct {
		dev  *Device
		port int
	}

	jobs := make(chan job, opt.Concurrency)
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < opt.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				if !tcpOpen(ctx, j.dev.IP, j.port, opt.Timeout) {
					continue
				}
				mu.Lock()
				j.dev.Ports = append(j.dev.Ports, Port{
					Number:  j.port,
					Service: ServiceName(j.port),
				})
				mu.Unlock()
			}
		}()
	}

	go func() {
		defer close(jobs)
		// Interleave: port p on every device, then port p+1, ...
		for _, p := range ports {
			for _, d := range devices {
				if d.IsSelf {
					continue // scanning ourselves proves nothing
				}
				select {
				case <-ctx.Done():
					return
				case jobs <- job{dev: d, port: p}:
				}
			}
		}
	}()

	wg.Wait()
}

func tcpOpen(ctx context.Context, ip string, port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// resolveNames fills in hostnames by reverse DNS, which on a home network
// is usually answered by the router or by mDNS.
func resolveNames(ctx context.Context, devices []*Device) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, d := range devices {
		if d.Hostname != "" {
			continue
		}
		wg.Add(1)
		go func(d *Device) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
			defer cancel()
			var r net.Resolver
			names, err := r.LookupAddr(c, d.IP)
			if err == nil && len(names) > 0 {
				d.Hostname = trimDot(names[0])
			}
		}(d)
	}
	wg.Wait()
}

func trimDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
