package compiler

import (
	"fmt"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func collectionSource(id string, values map[string]plan.ResolvedParam) string {
	if id == "R02" {
		return "ps -eo pid=,comm= (process names only)"
	}
	if id == "A01" || id == "A02" {
		return "libc NSS (configured passwd/group providers)"
	}
	sources := map[string]string{"H01": "uname -m", "H02": "getconf _NPROCESSORS_ONLN (online logical processors)", "H03": "/proc/meminfo:MemTotal", "H04": "/proc/swaps", "O01": "/etc/os-release", "O02": "uname -r", "O04": "/proc/modules", "S03": "/proc/self/mountinfo", "N01": "ip -j address", "N02": "ss IPv4/IPv6 listening sockets", "N03": "TCP connection and system resolver", "N04": "DNS query to configured resolver", "N05": "HTTP response", "N06": "ip -j route get", "R01": "systemctl show", "R02": "ps -eo comm=", "R03": "/proc/loadavg", "R05": "timedatectl and systemctl", "A01": "NSS user lookup", "A02": "/etc/group and /etc/passwd", "A03": "/usr/sbin/sshd -T", "A04": "kernel security module state", "D01": "package manager query and version comparison", "D02": "PATH resolution and filesystem metadata (no target execution)", "D03": "runtime version query", "D04": "readelf and ldconfig cache", "L01": "/proc/uptime", "L02": "system kernel journal", "L03": "system service journal"}
	if id == "O03" {
		return "/proc/sys/" + strings.ReplaceAll(fmt.Sprint(values["name"].Value), ".", "/")
	}
	if id == "R04" {
		if unit, ok := values["unit"]; ok {
			return "systemctl show " + fmt.Sprint(unit.Value)
		}
		return "/proc/" + fmt.Sprint(values["pid"].Value) + "/limits"
	}
	if id == "S01" || id == "S02" || id == "D05" {
		return fmt.Sprint(values["path"].Value)
	}
	if id == "S04" {
		return "stat -f (f_bavail) and /proc/self/mountinfo: " + fmt.Sprint(values["path"].Value)
	}
	if id == "S05" {
		return "stat -f (inode counts) and /proc/self/mountinfo: " + fmt.Sprint(values["path"].Value)
	}
	if source, ok := sources[id]; ok {
		return source
	}
	return "custom Shell command"
}
