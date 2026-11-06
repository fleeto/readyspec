package compiler

import (
	"encoding/json"
	"net/netip"
	"strings"
)

// interfaceEvaluation validates the single interface record emitted for N01.
// A missing device is reported explicitly by the collector instead of being
// collapsed into a generic query error.
func interfaceEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"name": params["name"]}
	if strings.TrimSpace(text) == "__RS_INTERFACE_MISSING__" {
		summary["exists"] = false
		return "FAIL", summary
	}
	var interfaces []struct {
		Name      string   `json:"ifname"`
		Flags     []string `json:"flags"`
		Addresses []struct {
			Local  string `json:"local"`
			Prefix int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(text), &interfaces); err != nil || len(interfaces) != 1 {
		return "ERROR", map[string]any{"error": "INVALID_INTERFACE_OBSERVATION"}
	}
	up := false
	for _, flag := range interfaces[0].Flags {
		if flag == "UP" {
			up = true
		}
	}
	addresses := make([]string, 0, len(interfaces[0].Addresses))
	for _, actual := range interfaces[0].Addresses {
		address, err := netip.ParseAddr(actual.Local)
		if err != nil {
			return "ERROR", map[string]any{"error": "INVALID_INTERFACE_ADDRESS"}
		}
		addresses = append(addresses, netip.PrefixFrom(address, actual.Prefix).String())
	}
	summary["exists"] = true
	summary["up"] = up
	summary["addresses"] = addresses
	if interfaces[0].Name != "" {
		summary["observed_name"] = interfaces[0].Name
	}
	if up != params["up"] {
		return "FAIL", summary
	}
	expected, _ := params["addresses"].([]any)
	for _, value := range expected {
		network, ok := value.(string)
		if !ok {
			return "ERROR", map[string]any{"error": "INVALID_INTERFACE_PARAMETER"}
		}
		prefix, err := netip.ParsePrefix(network)
		if err != nil {
			address, err := netip.ParseAddr(network)
			if err != nil {
				return "ERROR", map[string]any{"error": "INVALID_INTERFACE_PARAMETER"}
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		found := false
		for _, actual := range interfaces[0].Addresses {
			address, err := netip.ParseAddr(actual.Local)
			if err != nil {
				return "ERROR", map[string]any{"error": "INVALID_INTERFACE_ADDRESS"}
			}
			if prefix.Contains(address) {
				found = true
			}
		}
		if !found {
			return "FAIL", summary
		}
	}
	return "PASS", summary
}
