package compiler

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func boundPortCommand(values map[string]plan.ResolvedParam) string {
	protocol := fmt.Sprint(values["protocol"].Value)
	flag := "-t"
	if strings.HasPrefix(protocol, "udp") {
		flag = "-u"
	}
	command := `printf '__RS_IPV4__\n'; `
	if !strings.HasSuffix(protocol, "6") {
		command += `ss -H -l -n -e -4 ` + flag + ` || exit 2; `
	}
	return command + `printf '__RS_IPV6__\n'; ss -H -l -n -e -6 ` + flag + ` || exit 2`
}

type boundSocket struct {
	address netip.Addr
	port    int
	v6only  *bool
}

func evaluateBoundPort(params map[string]any, text string) string {
	if !strings.HasPrefix(text, "__RS_IPV4__\n") {
		return "ERROR"
	}
	parts := strings.Split(strings.TrimPrefix(text, "__RS_IPV4__\n"), "__RS_IPV6__\n")
	if len(parts) != 2 {
		return "ERROR"
	}
	var sockets []boundSocket
	for family, part := range parts {
		for _, line := range strings.Split(part, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return "ERROR"
			}
			if fields[0] != "LISTEN" && fields[0] != "UNCONN" {
				return "ERROR"
			}
			host, portText, err := net.SplitHostPort(fields[3])
			if err != nil {
				return "ERROR"
			}
			port, err := strconv.Atoi(portText)
			if err != nil || port < 1 || port > 65535 {
				return "ERROR"
			}
			if host == "*" {
				host = "0.0.0.0"
				if family == 1 {
					host = "::"
				}
			}
			host, _, _ = strings.Cut(host, "%")
			address, err := netip.ParseAddr(host)
			if err != nil {
				return "ERROR"
			}
			socket := boundSocket{address: address, port: port}
			for _, field := range fields[5:] {
				if field == "v6only:0" || field == "v6only:1" {
					only := field == "v6only:1"
					socket.v6only = &only
				}
			}
			sockets = append(sockets, socket)
		}
	}
	port, err := strconv.Atoi(fmt.Sprint(params["port"]))
	if err != nil {
		return "ERROR"
	}
	expected, ok := params["listening"].(bool)
	if !ok {
		return "ERROR"
	}
	if _, specified := params["ip"]; !specified {
		occupied := false
		for _, socket := range sockets {
			if socket.port == port {
				occupied = true
			}
		}
		if occupied == expected {
			return "PASS"
		}
		return "FAIL"
	}
	targets, ok := params["ip"].([]any)
	if !ok || len(targets) == 0 {
		return "ERROR"
	}
	unknown, mismatch := false, false
	for _, target := range targets {
		address, err := netip.ParseAddr(fmt.Sprint(target))
		if err != nil {
			return "ERROR"
		}
		address = address.Unmap()
		occupied, uncertain := false, false
		for _, socket := range sockets {
			if socket.port != port {
				continue
			}
			actual := socket.address.Unmap()
			if actual.Is4() == address.Is4() {
				if actual == address || actual.IsUnspecified() || address.IsUnspecified() {
					occupied = true
				}
				continue
			}
			if actual.Is6() && actual.IsUnspecified() && address.Is4() {
				if socket.v6only == nil {
					uncertain = true
				} else if !*socket.v6only {
					occupied = true
				}
			}
		}
		if occupied {
			if !expected {
				mismatch = true
			}
		} else if uncertain {
			unknown = true
		} else if expected {
			mismatch = true
		}
	}
	if mismatch {
		return "FAIL"
	}
	if unknown {
		return "ERROR"
	}
	return "PASS"
}
