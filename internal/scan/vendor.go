package scan

import "strings"

// A MAC address begins with a 24-bit Organisationally Unique Identifier
// assigned to the manufacturer. Turning that into a name is what makes a
// scan readable: "a4:83:e7" means nothing, "Apple" tells you the unknown
// device on your network is somebody's laptop.
//
// The full IEEE registry is about 35,000 entries and 3 MB. Embedding it
// would triple the binary for a convenience feature, and downloading it
// would make an offline-first tool need the network. This is the short
// list: the vendors that actually turn up on a home or small-office
// network. Anything unrecognised simply shows no vendor, which is honest.
var ouis = map[string]string{
	"00:03:93": "Apple", "00:0a:27": "Apple", "00:1b:63": "Apple",
	"00:1e:c2": "Apple", "00:25:00": "Apple", "28:cf:e9": "Apple",
	"3c:15:c2": "Apple", "40:a6:d9": "Apple", "a4:83:e7": "Apple",
	"ac:bc:32": "Apple", "b8:e8:56": "Apple", "d0:e1:40": "Apple",
	"f0:18:98": "Apple", "f4:0f:24": "Apple", "8c:85:90": "Apple",

	"00:16:6c": "Samsung", "00:1d:25": "Samsung", "00:26:37": "Samsung",
	"08:37:3d": "Samsung", "34:23:87": "Samsung", "5c:0a:5b": "Samsung",
	"78:1f:db": "Samsung", "8c:77:12": "Samsung", "bc:20:a4": "Samsung",
	"e8:50:8b": "Samsung",

	"00:1a:11": "Google", "3c:5a:b4": "Google", "94:eb:2c": "Google",
	"a4:77:33": "Google", "f4:f5:d8": "Google", "f8:8f:ca": "Google",
	"da:a1:19": "Google",

	"00:0c:29": "VMware", "00:50:56": "VMware", "08:00:27": "VirtualBox",
	"52:54:00": "QEMU/KVM", "00:15:5d": "Hyper-V",

	"b8:27:eb": "Raspberry Pi", "dc:a6:32": "Raspberry Pi",
	"e4:5f:01": "Raspberry Pi", "28:cd:c1": "Raspberry Pi",

	"00:1a:2b": "Ayecom", "00:09:5b": "Netgear", "00:14:6c": "Netgear",
	"00:1e:2a": "Netgear", "20:4e:7f": "Netgear", "a0:40:a0": "Netgear",
	"00:18:4d": "Netgear", "c0:3f:0e": "Netgear",

	"00:13:10": "Linksys", "00:18:39": "Linksys", "00:1a:70": "Linksys",
	"48:f8:b3": "Linksys", "c0:56:27": "Belkin",

	"00:1d:7e": "Cisco", "00:23:04": "Cisco", "00:26:99": "Cisco",
	"70:81:05": "Cisco", "f4:0f:1b": "Cisco",

	"00:25:9c": "TP-Link", "14:cc:20": "TP-Link", "50:c7:bf": "TP-Link",
	"a0:f3:c1": "TP-Link", "ec:08:6b": "TP-Link", "f4:f2:6d": "TP-Link",
	"60:e3:27": "TP-Link",

	"00:1f:33": "D-Link", "14:d6:4d": "D-Link", "28:10:7b": "D-Link",
	"bc:f6:85": "D-Link",

	"00:24:01": "D-Link", "00:26:5a": "D-Link",

	"1c:bd:b9": "Huawei", "48:46:fb": "Huawei", "84:a8:e4": "Huawei",
	"e0:19:1d": "Huawei", "00:e0:fc": "Huawei",

	"64:09:80": "Xiaomi", "78:11:dc": "Xiaomi", "8c:be:be": "Xiaomi",
	"f8:a4:5f": "Xiaomi", "50:8f:4c": "Xiaomi",

	"00:17:88": "Philips Hue", "ec:b5:fa": "Philips Hue",
	"18:b4:30": "Nest", "64:16:66": "Nest",
	"44:65:0d": "Amazon", "68:37:e9": "Amazon", "74:c2:46": "Amazon",
	"fc:65:de": "Amazon", "0c:47:c9": "Amazon",
	"b0:4e:26": "TP-Link Kasa", "50:d4:f7": "TP-Link",
	"d8:0d:17": "TP-Link",

	"00:11:32": "Synology", "00:c0:b7": "Synology",
	"00:08:9b": "ICP Electronics", "00:d0:59": "Ambit",

	"00:1b:a9": "Brother", "00:80:77": "Brother",
	"00:00:48": "Epson", "00:26:ab": "Epson",
	"00:15:99": "HP", "3c:d9:2b": "HP", "9c:b6:54": "HP",
	"00:21:5a": "HP", "70:5a:0f": "HP",
	"00:00:aa": "Xerox", "00:1e:0b": "Xerox",
	"00:1e:8f": "Canon", "88:87:17": "Canon",

	"00:04:20": "Slim Devices", "00:04:4b": "NVIDIA",
	"00:1c:42": "Parallels", "00:16:3e": "Xen",
	"00:05:cd": "Denon", "00:09:b0": "Onkyo",
	"00:24:e4": "Withings", "00:1a:22": "eQ-3",
	"ac:63:be": "Amazon", "4c:ef:c0": "Amazon",
	"18:74:2e": "Amazon", "38:f7:3d": "Amazon",
	"6c:56:97": "Amazon", "40:b4:cd": "Amazon",
	"00:12:17": "Cisco-Linksys", "00:1c:10": "Cisco-Linksys",
	"90:72:40": "Apple", "d8:9e:3f": "Apple", "6c:94:f8": "Apple",
	"04:d3:b0": "Intel", "34:02:86": "Intel", "94:65:9c": "Intel",
	"a4:c3:f0": "Intel", "e4:a4:71": "Intel", "7c:b2:7d": "Intel",
	"00:1f:16": "Wistron", "00:23:14": "Intel",
	"00:e0:4c": "Realtek", "52:54:ab": "Realtek",
	"00:90:4c": "Epigram/Broadcom", "00:10:18": "Broadcom",
	"00:24:d7": "Intel", "5c:51:4f": "Intel",
	"dc:a6:32:": "Raspberry Pi",
	"00:1c:bf":  "Intel", "00:26:c7": "Intel",
	"b8:27:eb:": "Raspberry Pi",
	"2c:f0:5d":  "Micro-Star", "00:16:17": "Micro-Star",
	"70:85:c2": "ASRock", "bc:5f:f4": "ASRock",
	"00:1b:fc": "ASUSTek", "2c:56:dc": "ASUSTek", "ac:22:0b": "ASUSTek",
	"38:d5:47": "ASUSTek", "04:d9:f5": "ASUSTek",
	"00:24:8c": "ASUSTek", "1c:87:2c": "ASUSTek",
	"00:e0:4d": "Internet Initiative", "00:1e:68": "Wistron",
	"00:1d:0f": "TP-Link", "00:27:19": "TP-Link",
	"54:e6:fc": "TP-Link", "b0:48:7a": "TP-Link",
	"00:1a:ef": "Loopcomm", "00:22:6b": "Cisco-Linksys",
	"6c:5a:b0": "TCL", "00:0e:8f": "Sercomm",
	"00:1e:52": "Apple", "00:03:6b": "Cisco",
	"00:1f:5b": "Apple", "00:22:41": "Apple", "00:23:df": "Apple",
	"7c:d1:c3": "Apple", "c8:2a:14": "Apple", "e0:b9:ba": "Apple",
	"e4:ce:8f": "Apple", "f8:1e:df": "Apple",
	"00:21:e9": "Apple", "00:25:bc": "Apple",
	"58:55:ca": "Apple", "5c:59:48": "Apple",
	"78:31:c1": "Apple", "80:e6:50": "Apple",
	"88:63:df": "Apple", "98:fe:94": "Apple",
	"b8:17:c2": "Apple", "cc:08:e0": "Apple",
	"d4:9a:20": "Apple", "e0:f8:47": "Apple",
	"04:0c:ce": "Apple", "0c:74:c2": "Apple",
	"10:40:f3": "Apple", "14:10:9f": "Apple",
	"24:ab:81": "Apple", "28:6a:ba": "Apple",
	"2c:b4:3a": "Apple", "34:c0:59": "Apple",
	"3c:07:54": "Apple", "44:2a:60": "Apple",
	"48:74:6e": "Apple", "4c:8d:79": "Apple",
	"54:26:96": "Apple", "5c:95:ae": "Apple",
	"60:33:4b": "Apple", "64:20:0c": "Apple",
	"68:a8:6d": "Apple", "6c:70:9f": "Apple",
	"70:cd:60": "Apple", "74:e2:f5": "Apple",
	"78:a3:e4": "Apple", "7c:6d:62": "Apple",
	"84:38:35": "Apple", "88:1f:a1": "Apple",
	"8c:2d:aa": "Apple", "90:b2:1f": "Apple",
	"94:e9:6a": "Apple", "98:03:d8": "Apple",
	"9c:04:eb": "Apple", "a0:ed:cd": "Apple",
	"a8:66:7f": "Apple", "ac:3c:0b": "Apple",
	"b0:65:bd": "Apple", "b4:f0:ab": "Apple",
	"bc:52:b7": "Apple", "c0:63:94": "Apple",
	"c4:2c:03": "Apple", "c8:69:cd": "Apple",
	"cc:78:5f": "Apple", "d0:23:db": "Apple",
	"d4:f4:6f": "Apple", "d8:30:62": "Apple",
	"dc:2b:2a": "Apple", "e0:66:78": "Apple",
	"e4:8b:7f": "Apple", "e8:80:2e": "Apple",
	"ec:35:86": "Apple", "f0:db:f8": "Apple",
	"f4:31:c3": "Apple", "f8:27:93": "Apple",
	"fc:25:3f": "Apple",
}

// VendorOf maps a MAC address to a manufacturer name, or "" if the OUI is
// not in the short list. Locally administered addresses — which every
// modern phone uses for Wi-Fi privacy — are reported as such, because
// "unknown vendor" would be misleading when the address is randomised by
// design.
func VendorOf(mac string) string {
	m := strings.ToLower(strings.TrimSpace(mac))
	m = strings.ReplaceAll(m, "-", ":")
	if len(m) < 8 {
		return ""
	}
	// A known OUI wins over the locally-administered bit. Some real
	// vendors sit in that range by design -- QEMU's 52:54:00 is the
	// obvious one -- and calling those "randomised" would be wrong.
	if v, ok := ouis[m[:8]]; ok {
		return v
	}
	// Broadcast and all-zero are not devices and not randomised.
	if m == "ff:ff:ff:ff:ff:ff" || m == "00:00:00:00:00:00" {
		return ""
	}
	if IsRandomMAC(m) {
		return "randomised MAC"
	}
	return ""
}

// IsRandomMAC reports whether the address is locally administered, i.e.
// bit 1 of the first octet is set. Android, iOS and recent desktop Linux
// all randomise their Wi-Fi MAC per network by default.
func IsRandomMAC(mac string) bool {
	m := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
	if len(m) < 2 {
		return false
	}
	var b byte
	for i := 0; i < 2; i++ {
		c := m[i]
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		default:
			return false
		}
		b = b<<4 | v
	}
	return b&0x02 != 0
}
