package plan

import (
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var capacityPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$`)

func normalizeParam(definition map[string]any, name string, value any) (any, error) {
	properties, _ := definition["properties"].(map[string]any)
	property, _ := properties[name].(map[string]any)
	kind, _ := property["x-readyspec-type"].(string)
	if value != nil && kind == "" {
		if values, ok := value.([]any); ok {
			if itemDefinition, ok := property["items"].(map[string]any); ok {
				normalized := make([]any, len(values))
				for i, item := range values {
					v, err := normalizeParam(map[string]any{"properties": map[string]any{"item": itemDefinition}}, "item", item)
					if err != nil {
						return nil, err
					}
					normalized[i] = v
				}
				return normalized, nil
			}
		}
	}
	if value == nil || kind == "" {
		return value, nil
	}
	text, ok := value.(string)
	if !ok && kind != "port" {
		return nil, fmt.Errorf("invalid domain value")
	}
	switch kind {
	case "capacity":
		match := capacityPattern.FindStringSubmatch(strings.TrimSpace(text))
		if match == nil {
			return nil, fmt.Errorf("invalid capacity")
		}
		units := map[string]int64{"B": 1, "KB": 1000, "MB": 1000000, "GB": 1000000000, "TB": 1000000000000, "KiB": 1024, "MiB": 1048576, "GiB": 1073741824, "TiB": 1099511627776}
		n, _ := new(big.Rat).SetString(match[1])
		n.Mul(n, new(big.Rat).SetInt64(units[match[2]]))
		if !n.IsInt() || !n.Num().IsInt64() {
			return nil, fmt.Errorf("capacity overflow or fractional byte")
		}
		if minimum, ok := property["x-readyspec-minimum"]; ok {
			bound, valid := new(big.Rat).SetString(fmt.Sprint(minimum))
			if !valid || n.Cmp(bound) < 0 {
				return nil, fmt.Errorf("capacity below declared minimum")
			}
		}
		return n.Num().Int64(), nil
	case "duration":
		d, err := time.ParseDuration(text)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("invalid duration")
		}
		return int64(d), nil
	case "path":
		if !strings.HasPrefix(text, "/") || strings.ContainsRune(text, 0) {
			return nil, fmt.Errorf("invalid absolute path")
		}
		// Do not Clean: lexical simplification can change symlink traversal.
		for _, part := range strings.Split(text, "/") {
			if part == ".." {
				return nil, fmt.Errorf("parent traversal is not allowed")
			}
		}
		return text, nil
	case "ip_or_cidr":
		if addr, err := netip.ParseAddr(text); err == nil {
			return addr.String(), nil
		}
		if prefix, err := netip.ParsePrefix(text); err == nil {
			return prefix.Masked().String(), nil
		}
		return nil, fmt.Errorf("invalid IP or CIDR")
	case "ip":
		if address, err := netip.ParseAddr(text); err == nil {
			return address.String(), nil
		}
		return nil, fmt.Errorf("invalid IP")
	case "tcp_address", "dns_server":
		if kind == "dns_server" {
			if address, err := netip.ParseAddr(text); err == nil {
				return net.JoinHostPort(address.String(), "53"), nil
			}
			if !strings.Contains(text, ":") {
				text = net.JoinHostPort(text, "53")
			}
		}
		host, port, err := net.SplitHostPort(text)
		if err != nil || host == "" || strings.ContainsAny(host, "/\\@?# \t\r\n\x00") {
			return nil, fmt.Errorf("invalid network endpoint")
		}
		n, valid := new(big.Int).SetString(port, 10)
		if !valid || !n.IsInt64() || n.Int64() < 1 || n.Int64() > 65535 {
			return nil, fmt.Errorf("invalid endpoint port")
		}
		return net.JoinHostPort(strings.ToLower(host), n.String()), nil
	case "http_url":
		parsed, err := url.Parse(text)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
			return nil, fmt.Errorf("invalid HTTP endpoint")
		}
		return parsed.String(), nil
	case "regex":
		if _, err := regexp.Compile(text); err != nil {
			return nil, fmt.Errorf("invalid regular expression")
		}
		return text, nil
	case "port":
		var n float64
		switch v := value.(type) {
		case int:
			n = float64(v)
		case int64:
			n = float64(v)
		case float64:
			n = v
		default:
			return nil, fmt.Errorf("invalid port")
		}
		if n < 1 || n > 65535 || n != float64(int64(n)) {
			return nil, fmt.Errorf("invalid port")
		}
		return int64(n), nil
	case "secret_ref", "version_constraint":
		return nil, fmt.Errorf("unsupported domain type")
	default:
		return nil, fmt.Errorf("unknown domain type")
	}
}

func validateOrderedParameters(definition map[string]any, values map[string]ResolvedParam) error {
	rules, _ := definition["x-readyspec-order"].([]any)
	for _, rule := range rules {
		pair, ok := rule.([]any)
		if !ok || len(pair) != 2 {
			return fmt.Errorf("invalid ordering definition")
		}
		minimumName, ok := pair[0].(string)
		if !ok {
			return fmt.Errorf("invalid ordering definition")
		}
		maximumName, ok := pair[1].(string)
		if !ok {
			return fmt.Errorf("invalid ordering definition")
		}
		minimum, hasMin := values[minimumName]
		maximum, hasMax := values[maximumName]
		if !hasMin || !hasMax || minimum.Value == nil || maximum.Value == nil {
			continue
		}
		comparison := 0
		if definition["x-readyspec-order-kind"] == "numeric_version" {
			a, ok := minimum.Value.(string)
			if !ok {
				return fmt.Errorf("invalid ordered version")
			}
			b, ok := maximum.Value.(string)
			if !ok {
				return fmt.Errorf("invalid ordered version")
			}
			var valid bool
			comparison, valid = CompareNumericVersions(a, b)
			if !valid {
				return fmt.Errorf("invalid ordered version")
			}
		} else {
			a, ok := new(big.Rat).SetString(fmt.Sprint(minimum.Value))
			if !ok {
				return fmt.Errorf("invalid ordered number")
			}
			b, ok := new(big.Rat).SetString(fmt.Sprint(maximum.Value))
			if !ok {
				return fmt.Errorf("invalid ordered number")
			}
			comparison = a.Cmp(b)
		}
		if comparison > 0 {
			return fmt.Errorf("inverted parameter range")
		}
	}
	return nil
}
