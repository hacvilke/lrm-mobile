package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lrm-project/lrm/mobile/internal/platform"
	"github.com/lrm-project/lrm/mobile/internal/scan"
)

const scanUsage = `lrm scan — see what is on the network around this phone

Usage: lrm scan [options]

  Lists the devices sharing this device's Wi-Fi, and the Wi-Fi networks in
  radio range. Devices running LRM are marked, so this answers "is my
  laptop even reachable from here" without leaving the terminal.

Depth:
  --full           discover live hosts, then sweep ports 1-10000 on them (default)
  --fast           discover live hosts and identify common services only
  --passive        neighbour table and mDNS only; sends almost nothing

Options:
  --cidr RANGE     scan this range instead of the detected subnet
  --ports LIST     ports for the deep phase: 22,80,443 or 1-1024 or 1-65535
  --timeout MS     per-connection timeout (default 600)
  --budget SEC     wall-clock ceiling for the whole scan (default 180)
  --concurrency N  simultaneous connections (default 256)
  --no-wifi        skip the Wi-Fi survey
  --wifi-only      only survey Wi-Fi networks, do not touch the LAN
  --json           machine-readable output
  -h, --help       this text

Notes:
  Scanning is limited to the subnet this device is joined to unless you
  pass --cidr. Only scan networks you are responsible for.

  The Wi-Fi survey needs the Termux:API app and the termux-api package,
  plus Android's Location permission. Android throttles Wi-Fi scans to a
  few per two minutes.

  There is no ICMP ping here: that needs root. Hosts are found with TCP
  probes, so a device behind a strict firewall may not appear.
`

func runScan(args []string, info platform.Info) int {
	opt := scan.DefaultOptions()
	asJSON := false
	wifiOnly := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "lrm scan: %s needs a value\n", a)
				return "", false
			}
			i++
			return args[i], true
		}
		switch a {
		case "-h", "--help":
			fmt.Print(scanUsage)
			return 0
		case "--full":
			opt.Depth = scan.Full
		case "--fast":
			opt.Depth = scan.Fast
		case "--passive":
			opt.Depth = scan.Passive
		case "--no-wifi":
			opt.SkipWiFi = true
		case "--wifi-only":
			wifiOnly = true
		case "--json":
			asJSON = true
		case "--cidr":
			v, ok := next()
			if !ok {
				return 2
			}
			opt.CIDR = v
		case "--ports":
			v, ok := next()
			if !ok {
				return 2
			}
			ports, err := parsePorts(v)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lrm scan: %v\n", err)
				return 2
			}
			opt.Ports = ports
		case "--timeout":
			v, ok := next()
			if !ok {
				return 2
			}
			ms, err := strconv.Atoi(v)
			if err != nil || ms <= 0 {
				fmt.Fprintln(os.Stderr, "lrm scan: --timeout wants milliseconds")
				return 2
			}
			opt.Timeout = time.Duration(ms) * time.Millisecond
		case "--budget":
			v, ok := next()
			if !ok {
				return 2
			}
			s, err := strconv.Atoi(v)
			if err != nil || s <= 0 {
				fmt.Fprintln(os.Stderr, "lrm scan: --budget wants seconds")
				return 2
			}
			opt.Budget = time.Duration(s) * time.Second
		case "--concurrency":
			v, ok := next()
			if !ok {
				return 2
			}
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, "lrm scan: --concurrency wants a positive number")
				return 2
			}
			opt.Concurrency = n
		default:
			fmt.Fprintf(os.Stderr, "lrm scan: unknown option %q (try --help)\n", a)
			return 2
		}
	}

	if wifiOnly {
		return runWiFiOnly(asJSON)
	}

	// Ctrl-C should end the scan and print what we have, not lose it.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !asJSON {
		opt.Progress = func(s string) { fmt.Fprintf(os.Stderr, "  %s\n", s) }
		fmt.Printf("lrm scan  (%s, depth=%s)\n\n", info.Describe(), opt.Depth)
	}

	res, err := scan.Run(ctx, opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lrm scan: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return 0
	}
	printScan(res)
	return 0
}

func runWiFiOnly(asJSON bool) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nets, note := scan.WiFiScan(ctx)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"networks": nets, "note": note})
		return 0
	}
	printNetworks(nets)
	if note != "" {
		fmt.Println("\nnote: " + note)
	}
	return 0
}

func printScan(res *scan.Result) {
	fmt.Println()
	for _, in := range res.Interfaces {
		fmt.Printf("interface %s  %s  on %s", in.Name, in.IP, in.CIDR)
		if in.Router != "" {
			fmt.Printf("  gateway~%s", in.Router)
		}
		fmt.Println()
	}

	fmt.Printf("\n%d device(s) on the local network\n\n", len(res.Devices))
	if len(res.Devices) > 0 {
		fmt.Printf("  %-15s  %-17s  %-14s  %s\n", "ADDRESS", "MAC", "VENDOR", "NAME / PORTS")
		fmt.Printf("  %-15s  %-17s  %-14s  %s\n",
			strings.Repeat("-", 15), strings.Repeat("-", 17),
			strings.Repeat("-", 14), strings.Repeat("-", 28))
	}
	for _, d := range res.Devices {
		tag := ""
		switch {
		case d.IsSelf:
			tag = "  <- this phone"
		case d.IsRouter:
			tag = "  <- gateway"
		}
		name := d.Hostname
		if name == "" {
			name = "-"
		}
		fmt.Printf("  %-15s  %-17s  %-14s  %s%s\n",
			d.IP, dash(d.MAC), dash(d.Vendor), name, tag)

		if d.LRM != nil {
			l := d.LRM
			desc := "LRM peer"
			if l.User != "" {
				desc += " — " + l.User
			}
			if l.Workspace != "" {
				desc += " (workspace " + short(l.Workspace) + ")"
			}
			if l.Port != 0 {
				desc += fmt.Sprintf(" :%d", l.Port)
			}
			fmt.Printf("  %-15s  %-17s  %-14s  * %s\n", "", "", "", desc)
		}
		if len(d.Ports) > 0 {
			fmt.Printf("  %-15s  %-17s  %-14s  %s\n", "", "", "", portLine(d.Ports))
		}
	}

	if len(res.Networks) > 0 {
		fmt.Println()
		printNetworks(res.Networks)
	}

	if len(res.Notes) > 0 {
		fmt.Println()
		for _, n := range res.Notes {
			fmt.Println("note: " + n)
		}
	}
	fmt.Printf("\nscanned in %s\n", res.Duration)
}

func printNetworks(nets []scan.Network) {
	if len(nets) == 0 {
		fmt.Println("0 Wi-Fi networks in range")
		return
	}
	fmt.Printf("%d Wi-Fi network(s) in range\n\n", len(nets))
	fmt.Printf("  %-5s  %-24s  %-7s  %-4s  %-14s  %s\n", "SIG", "SSID", "BAND", "CH", "SECURITY", "BSSID")
	fmt.Printf("  %-5s  %-24s  %-7s  %-4s  %-14s  %s\n",
		"-----", strings.Repeat("-", 24), "-------", "----",
		strings.Repeat("-", 14), strings.Repeat("-", 17))
	for _, n := range nets {
		ssid := n.SSID
		if len(ssid) > 24 {
			ssid = ssid[:21] + "..."
		}
		if n.Connected {
			ssid += " *"
		}
		ch := ""
		if n.Channel > 0 {
			ch = strconv.Itoa(n.Channel)
		}
		fmt.Printf("  %-5s  %-24s  %-7s  %-4s  %-14s  %s\n",
			scan.SignalBars(n.RSSI), ssid, n.Band, ch, dash(n.Security), n.BSSID)
	}
	fmt.Println("\n  * = currently connected")
}

func portLine(ps []scan.Port) string {
	var b strings.Builder
	b.WriteString("ports: ")
	for i, p := range ps {
		if i > 0 {
			b.WriteString(", ")
		}
		if i == 12 {
			fmt.Fprintf(&b, "... (+%d more)", len(ps)-12)
			break
		}
		if p.Service != "" {
			fmt.Fprintf(&b, "%d/%s", p.Number, p.Service)
		} else {
			fmt.Fprintf(&b, "%d", p.Number)
		}
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// parsePorts accepts "22,80,443", "1-1024", and mixtures of the two.
func parsePorts(spec string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(p int) error {
		if p < 1 || p > 65535 {
			return fmt.Errorf("port %d is out of range (1-65535)", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
		return nil
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err := strconv.Atoi(strings.TrimSpace(lo))
			if err != nil {
				return nil, fmt.Errorf("bad port range %q", part)
			}
			b, err := strconv.Atoi(strings.TrimSpace(hi))
			if err != nil {
				return nil, fmt.Errorf("bad port range %q", part)
			}
			if a > b {
				a, b = b, a
			}
			if b-a > 65535 {
				return nil, fmt.Errorf("port range %q is too large", part)
			}
			for p := a; p <= b; p++ {
				if err := add(p); err != nil {
					return nil, err
				}
			}
			continue
		}
		p, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad port %q", part)
		}
		if err := add(p); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ports given")
	}
	return out, nil
}
