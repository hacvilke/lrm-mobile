# `lrm scan` — what is around this phone

A mobile-only command. It answers the question a phone is uniquely well
placed to answer: *what else is on this network, and what networks are
within reach?*

`lrm peers` only ever shows machines already running LRM. When a sync is
not working, the first thing you need to know is whether the other machine
is even reachable — and that is a question about the network, not about
LRM.

```sh
lrm scan                      # devices on your Wi-Fi + networks in range
lrm scan --fast               # quicker: live hosts and common services
lrm scan --passive            # sends almost nothing
lrm scan --wifi-only          # just the Wi-Fi survey
lrm scan --json               # machine-readable
```

## Example

```
lrm scan  (android/arm64 (Termux, Android 16), depth=full)

interface wlan0  192.168.1.37  on 192.168.1.0/24  gateway~192.168.1.1

7 device(s) on the local network

  ADDRESS          MAC                VENDOR          NAME / PORTS
  ---------------  -----------------  --------------  ----------------------------
  192.168.1.1      c0:56:27:aa:bb:cc  Belkin          router.lan  <- gateway
                                                      ports: 53/dns, 80/http, 443/https
  192.168.1.12     b8:27:eb:11:22:33  Raspberry Pi    pi.lan
                                                      ports: 22/ssh, 8787/lrm
                                                      * LRM peer — brandon (workspace 9f2c1a...) :8787
  192.168.1.37     -                  -               localhost  <- this phone
  192.168.1.44     a4:83:e7:00:11:22  Apple           macbook.lan
                                                      ports: 22/ssh, 5000/upnp, 7000/airplay
  192.168.1.51     -                  randomised MAC  -
                                                      ports: 62078/iphone-sync

4 Wi-Fi network(s) in range

  SIG    SSID                      BAND     CH    SECURITY        BSSID
  -----  ------------------------  -------  ----  --------------  -----------------
  ****   HomeNet *                 5GHz     149   WPA2            c0:56:27:aa:bb:cd
  ***    HomeNet                   2.4GHz   6     WPA2            c0:56:27:aa:bb:cc
  **     NeighbourWiFi             2.4GHz   11    WPA3            18:b4:30:00:11:22
  *      (hidden)                  2.4GHz   1     WPA2            -

  * = currently connected

scanned in 34.2s
```

## How it finds things

| Source | What it gives | Caveat |
|---|---|---|
| `/proc/net/arp` | IP + MAC of anything that recently talked | **Restricted on Android 10+ — confirmed unreadable on Android 16.** No MAC addresses or vendor names on a modern phone. |
| TCP connect probes | live hosts | No ICMP — see below |
| mDNS (upstream LRM's browser) | LRM peers, user, workspace, port | Only devices running `lrm daemon` |
| Reverse DNS | hostnames | Depends on your router |
| MAC OUI table | vendor names | Short built-in list, see below |
| `termux-wifi-scaninfo` | access points in range | Needs Termux:API + Location |

### Netlink is blocked on Android 11+

Go's `net.Interfaces()` is built on a `NETLINK_ROUTE` dump, which Android
11 and later deny to ordinary apps. On an Android 16 device `lrm scan` died
outright with:

```
lrm scan: cannot list network interfaces: route ip+net: netlinkrib: permission denied
```

This is a platform restriction with no permission to request and no flag to
pass. Since v0.2.1 the scanner never relies on netlink:

1. **`/proc/net/route`** — the routing table as plain text: interface,
   destination, netmask and gateway. This gives the *real* gateway rather
   than the conventional `.1` guess.
2. **A connected UDP socket** — `connect(2)` on UDP sends no packets; the
   kernel merely selects a source address, which is this device's IP on the
   route that would be used. No permissions at all.
3. **Fall back to assuming a `/24`** around that address if the routing
   table is unreadable too.

On Android 16, step 1 is unavailable as well — `/proc/net/route` is
restricted along with the rest of `/proc/net` — so a current phone lands on
step 3. In practice that works: a `/24` is correct on essentially every
home and office Wi-Fi, and the `.1` gateway guess was right on the test
network. If your subnet is not a `/24`, pass `--cidr`.

Whenever a fallback is used the scan says so in its `note:` output, because
a degraded scan that looks identical to a full one is worse than one that
admits it. Interface MAC addresses are unavailable via this path.

### There is no ping

A real ICMP ping needs a raw socket, which needs root. LRM Mobile refuses
to require root, so host discovery is TCP-based. Two consequences:

- **A refused connection still counts as alive.** If a host answers a SYN
  with RST, it exists. Only silence is treated as "nothing there".
- **A device behind a strict firewall may not appear.** If it drops every
  packet and has not talked recently enough to be in the neighbour table,
  nothing short of raw sockets will see it.

### Why `--full` is two-phase

A blind sweep of 254 hosts × 65535 ports is 16.6 million connections. On a
phone that is hours of radio time and a hot battery, and almost all of it
is addresses where nothing lives.

So `--full` **discovers live hosts first**, then sweeps only those. Same
result, roughly a hundred times faster. The deep phase covers ports
1–10000 plus the well-known high ports; if you genuinely need everything,
`--ports 1-65535` and go make coffee.

Work is also interleaved across hosts rather than finishing one at a time,
so no single machine sees a burst of thousands of connections and no one
unresponsive host stalls the run.

### Identifying devices when there are no MAC addresses

On Android the ARP table is unreadable, so there is no MAC and therefore no
OUI vendor lookup. Rather than show a table of bare IP addresses, the
scanner asks the devices who they are. All of this is information they
publish to anyone on the local network, and none of it needs root, ARP or
netlink:

| Probe | What it gives | Typically answers |
|---|---|---|
| **SSDP/UPnP** | friendly name, manufacturer, model | routers, TVs, speakers, printers, NAS, consoles |
| **NetBIOS** (UDP 137) | workstation name | Windows machines, Samba servers |
| **TLS certificate** | common name, SANs, organisation | anything on 443; embedded devices put the model here |
| **Service banners** | software and often the OS | SSH announces `OpenSSH_9.6p1 Ubuntu-3ubuntu13`; HTTP returns `Server:` and the page title |

In practice this is *more* informative than a MAC lookup: an OUI tells you
a board was made by Realtek, whereas the device tells you it is a
`Brother HL-L2350DW` or `nginx/1.24.0 (Ubuntu)`.

`--no-identify` turns it all off if you want the quietest possible scan.

SSDP works on Android without a multicast lock because the M-SEARCH goes
out as multicast but the replies come back unicast to our source port.

### Vendor names

The IEEE OUI registry is ~35,000 entries and about 3 MB. Embedding it would
triple the binary for a cosmetic feature, and downloading it would make an
offline-first tool need the network. There is a short built-in list of the
vendors that actually appear on home networks. Anything else shows no
vendor, which is honest.

Modern phones randomise their Wi-Fi MAC per network, so you will see
`randomised MAC` a lot. That is reported explicitly rather than as "unknown
vendor", because the address is meaningless by design, not because lookup
failed. A real OUI always wins over the randomisation bit — QEMU's
`52:54:00` is a genuine vendor prefix that happens to sit in the
locally-administered range.

## The Wi-Fi survey

A process cannot touch the Wi-Fi radio on Android. It goes through the
Termux:API app, which needs Location permission:

**Two separate installs are required**, and having only the first is the
most common reason the survey returns nothing:

```sh
pkg install termux-api        # 1. the command-line helpers
```

2. the **Termux:API app** from F-Droid —
   <https://f-droid.org/packages/com.termux.api/> — then open it once and
   grant it **Location** permission.

With the package but not the app, `termux-wifi-scaninfo` blocks waiting for
a reply that never arrives, so the scan applies a 15-second timeout and
explains which half is missing.

Three things go wrong routinely, and each gets its own message rather than
an empty list:

- the `termux-api` package is missing
- the Termux:API **app** is missing, or Location is denied
- **Android throttles Wi-Fi scans** to roughly four per two minutes since
  Android 9. Past that you get stale or empty results, not an error.

`--no-wifi` skips it entirely.

## Scope and etiquette

By default the scan is confined to the subnets this device is **actually
joined to**. Pointing it elsewhere requires `--cidr`, which prints a
warning, because a tool that sweeps networks you are not on is a different
kind of tool than this one.

Please only scan networks you are responsible for. A full port sweep is
visible in any IDS and on many corporate and campus networks it will get
your device blocked. `--passive` exists for when you want to look without
touching anything.

Subnets larger than 4096 addresses are refused rather than silently
attempted — otherwise a `/8` on a VPN becomes a week-long job. Narrow it
with `--cidr`.

## Options

| Flag | Meaning |
|---|---|
| `--full` | discover, then sweep ports 1–10000 on live hosts (default) |
| `--fast` | discover, identify common services only |
| `--passive` | neighbour table + mDNS only |
| `--cidr RANGE` | scan a specific range instead of the detected subnet |
| `--ports LIST` | `22,80,443` or `1-1024` or `1-65535` |
| `--timeout MS` | per-connection timeout (default 600) |
| `--budget SEC` | wall-clock ceiling; always returns (default 180) |
| `--concurrency N` | simultaneous connections (default 256) |
| `--no-identify` | skip SSDP, NetBIOS, TLS and banner probing |
| `--no-wifi` / `--wifi-only` | control the Wi-Fi survey |
| `--json` | machine-readable output |

Ctrl-C ends the scan and prints what was found so far rather than
discarding it.
