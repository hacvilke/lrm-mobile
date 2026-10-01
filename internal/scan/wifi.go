package scan

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/lrm-project/lrm/mobile/internal/platform"
)

// A process cannot scan Wi-Fi itself on Android. The radio is behind a
// system service, reachable only through an app with the Location
// permission — which is what the Termux:API companion app provides via the
// termux-wifi-* commands.
//
// Three things therefore go wrong routinely, and each gets its own message
// rather than an empty list:
//
//   - termux-api (the package) is not installed
//   - Termux:API (the app) is not installed, or Location is denied
//   - Android's scan throttling: since Android 9, foreground apps get about
//     four scans per two minutes, and the call then returns stale or empty
//     results rather than failing
//
// VendorOf and the LAN scan work regardless; the Wi-Fi survey is additive.

// WiFiScan returns the access points in range. The second value is a note
// explaining any shortfall, empty when everything worked.
func WiFiScan(ctx context.Context) ([]Network, string) {
	if _, err := exec.LookPath("termux-wifi-scaninfo"); err != nil {
		if !platform.Detect(platform.RealEnv()).Termux {
			return nil, "" // not on Termux: silently skip, this is a phone feature
		}
		return nil, "Wi-Fi survey skipped: install the helpers with `pkg install termux-api` " +
			"and the Termux:API app from F-Droid, then grant it Location permission"
	}

	connected := currentSSID(ctx)

	// A termux-api helper blocks indefinitely when the Termux:API *app*
	// is absent — the shell script waits on a reply that never comes. The
	// timeout is the difference between a clear message and a hung
	// terminal.
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := platform.CommandContext(c, "termux-wifi-scaninfo").Output()
	if err != nil {
		if c.Err() != nil {
			return nil, "Wi-Fi survey timed out after 15s — the termux-api package is installed " +
				"but the Termux:API *app* is not responding. Install it from F-Droid " +
				"(https://f-droid.org/packages/com.termux.api/) and grant it Location permission."
		}
		return nil, "Wi-Fi survey failed: the termux-api package is installed, but the Termux:API *app* " +
			"is a separate install. Get it from F-Droid (https://f-droid.org/packages/com.termux.api/), " +
			"open it once, and grant Location permission."
	}

	var raw []struct {
		SSID      string `json:"ssid"`
		BSSID     string `json:"bssid"`
		RSSI      int    `json:"rssi"`
		Frequency int    `json:"frequency_mhz"`
		Caps      string `json:"capabilities"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, "Wi-Fi survey returned data this version does not understand — please open an issue with `termux-wifi-scaninfo` output"
	}
	if len(raw) == 0 {
		return nil, "Wi-Fi scan returned nothing: Android throttles scans to a few per two minutes, " +
			"and requires Location to be switched on. Try again shortly."
	}

	nets := make([]Network, 0, len(raw))
	for _, r := range raw {
		ssid := r.SSID
		if ssid == "" {
			ssid = "(hidden)"
		}
		nets = append(nets, Network{
			SSID:      ssid,
			BSSID:     strings.ToLower(r.BSSID),
			RSSI:      r.RSSI,
			Frequency: r.Frequency,
			Channel:   ChannelOf(r.Frequency),
			Band:      BandOf(r.Frequency),
			Security:  SecurityOf(r.Caps),
			Connected: ssid != "" && ssid == connected,
		})
	}
	return nets, ""
}

func currentSSID(ctx context.Context) string {
	if _, err := exec.LookPath("termux-wifi-connectioninfo"); err != nil {
		return ""
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := platform.CommandContext(c, "termux-wifi-connectioninfo").Output()
	if err != nil {
		return ""
	}
	var info struct {
		SSID string `json:"ssid"`
	}
	if json.Unmarshal(out, &info) != nil {
		return ""
	}
	return strings.Trim(info.SSID, `"`)
}

// BandOf maps a centre frequency to a human band name.
func BandOf(mhz int) string {
	switch {
	case mhz >= 2400 && mhz <= 2500:
		return "2.4GHz"
	case mhz >= 4900 && mhz <= 5900:
		return "5GHz"
	case mhz >= 5925 && mhz <= 7125:
		return "6GHz"
	case mhz >= 57000:
		return "60GHz"
	}
	return ""
}

// ChannelOf maps a centre frequency to its Wi-Fi channel number.
func ChannelOf(mhz int) int {
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz <= 2472:
		return (mhz-2412)/5 + 1
	case mhz >= 5160 && mhz <= 5885:
		return (mhz - 5000) / 5
	case mhz >= 5955 && mhz <= 7115:
		return (mhz-5955)/5 + 1
	}
	return 0
}

// SecurityOf summarises an Android capabilities string such as
// "[WPA2-PSK-CCMP][WPS][ESS]" into something readable.
func SecurityOf(caps string) string {
	c := strings.ToUpper(caps)
	switch {
	case strings.Contains(c, "WPA3"), strings.Contains(c, "SAE"):
		return "WPA3"
	case strings.Contains(c, "WPA2"), strings.Contains(c, "RSN"):
		return "WPA2"
	case strings.Contains(c, "WPA"):
		return "WPA"
	case strings.Contains(c, "WEP"):
		return "WEP (insecure)"
	case strings.Contains(c, "ESS"):
		return "open"
	}
	return ""
}

// SignalBars renders an RSSI in dBm as a coarse strength indicator.
// -30 is next to the router, -90 is unusable.
func SignalBars(rssi int) string {
	switch {
	case rssi == 0:
		return ""
	case rssi >= -50:
		return "****"
	case rssi >= -60:
		return "*** "
	case rssi >= -70:
		return "**  "
	case rssi >= -80:
		return "*   "
	}
	return "."
}
