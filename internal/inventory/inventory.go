// Package inventory parses bounded static inventory data without plugins or network access.
package inventory

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/safeyaml"
)

type Host struct {
	ID       string   `json:"id"`
	Address  string   `json:"address"`
	Port     int      `json:"port"`
	Groups   []string `json:"groups"`
	Username string   `json:"username,omitempty"`
}

func Parse(source, format string) ([]Host, error) {
	if len(source) > 10*1024*1024 {
		return nil, fmt.Errorf("INVENTORY_ERROR: input too large")
	}
	hosts := map[string]*Host{}
	explicitAddresses, explicitPorts := map[string]bool{}, map[string]bool{}
	groupVars := map[string]map[string]string{}
	setGroupVar := func(group, key, value string) error {
		if key != "ansible_user" && key != "ansible_port" {
			return fmt.Errorf("INVENTORY_ERROR: unsupported group variable")
		}
		if value == "" || strings.ContainsAny(value, "{} \t\r\n\x00") {
			return fmt.Errorf("INVENTORY_ERROR: invalid group variable")
		}
		if key == "ansible_port" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("INVENTORY_ERROR: invalid group port")
			}
			value = strconv.Itoa(n)
		}
		if groupVars[group] == nil {
			groupVars[group] = map[string]string{}
		}
		if old, exists := groupVars[group][key]; exists && old != value {
			return fmt.Errorf("INVENTORY_ERROR: conflicting group variable")
		}
		groupVars[group][key] = value
		return nil
	}
	usernames := map[string]string{}
	setUser := func(id, user string) error {
		if user == "" || strings.ContainsAny(user, "{} \t\r\n\x00") {
			return fmt.Errorf("INVENTORY_ERROR: invalid login user")
		}
		if previous, exists := usernames[id]; exists && previous != user {
			return fmt.Errorf("INVENTORY_ERROR: conflicting login user")
		}
		usernames[id] = user
		return nil
	}
	children := map[string][]string{}
	declaredGroups := map[string]bool{"all": true, "ungrouped": true}
	add := func(id, address string, port int, groups []string) error {
		addressSet, portSet := address != "", port != 0
		if !addressSet {
			address = id
		}
		if !portSet {
			port = 22
		}
		if id == "" || address == "" || port < 1 || port > 65535 || strings.ContainsAny(id+address, " \t\r\n\x00") || strings.ContainsAny(id+address, "{}") {
			return fmt.Errorf("INVENTORY_ERROR: invalid host")
		}
		if strings.ContainsAny(address, "/\\@?#") {
			return fmt.Errorf("INVENTORY_ERROR: invalid host address")
		}
		address = strings.Trim(address, "[]")
		if strings.Contains(address, ":") {
			if _, err := netip.ParseAddr(address); err != nil {
				return fmt.Errorf("INVENTORY_ERROR: invalid IPv6 address")
			}
		}
		if ip := net.ParseIP(address); ip != nil {
			address = ip.String()
		} else {
			address = strings.ToLower(address)
		}
		if existing := hosts[id]; existing != nil {
			if (addressSet && explicitAddresses[id] && existing.Address != address) || (portSet && explicitPorts[id] && existing.Port != port) {
				return fmt.Errorf("INVENTORY_ERROR: conflicting host definition")
			}
			if addressSet {
				existing.Address = address
				explicitAddresses[id] = true
			}
			if portSet {
				existing.Port = port
				explicitPorts[id] = true
			}
			existing.Groups = append(existing.Groups, groups...)
			return nil
		}
		hosts[id] = &Host{ID: id, Address: address, Port: port, Groups: append([]string{"all"}, groups...)}
		explicitAddresses[id], explicitPorts[id] = addressSet, portSet
		if len(hosts) > 10000 {
			return fmt.Errorf("INVENTORY_ERROR: host limit exceeded")
		}
		return nil
	}
	parseEndpoint := func(endpoint string) (string, int, error) {
		if address, port, err := net.SplitHostPort(endpoint); err == nil {
			n, err := strconv.Atoi(port)
			return address, n, err
		}
		if strings.Contains(endpoint, ":") && net.ParseIP(endpoint) == nil {
			return "", 0, fmt.Errorf("INVENTORY_ERROR: invalid endpoint")
		}
		return endpoint, 22, nil
	}
	switch format {
	case "quick":
		for _, entry := range strings.Split(source, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			id, endpoint, ok := strings.Cut(entry, "=")
			if !ok {
				id = entry
				endpoint = entry
			}
			address, port, err := parseEndpoint(endpoint)
			if err != nil {
				return nil, err
			}
			if hosts[id] != nil {
				return nil, fmt.Errorf("INVENTORY_ERROR: duplicate host name")
			}
			if err := add(id, address, port, nil); err != nil {
				return nil, err
			}
		}
	case "ini":
		group, section := "ungrouped", "hosts"
		for _, line := range strings.Split(source, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"), ":")
				group = parts[0]
				declaredGroups[group] = true
				if len(declaredGroups) > 1024 {
					return nil, fmt.Errorf("INVENTORY_ERROR: group limit exceeded")
				}
				section = "hosts"
				if len(parts) > 1 {
					section = parts[1]
				}
				if len(parts) > 2 || strings.ContainsAny(group, "{} \t\r\n\x00") || group == "" || (section != "hosts" && section != "children" && section != "vars") {
					return nil, fmt.Errorf("INVENTORY_ERROR: unsupported INI section")
				}
				continue
			}
			fields := strings.Fields(line)
			if section == "vars" {
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					return nil, fmt.Errorf("INVENTORY_ERROR: invalid group variable")
				}
				if err := setGroupVar(group, strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
					return nil, err
				}
				continue
			}
			if section == "children" {
				if len(fields) != 1 {
					return nil, fmt.Errorf("INVENTORY_ERROR: invalid child group")
				}
				children[group] = append(children[group], fields[0])
				continue
			}
			address := ""
			port := 0
			for _, field := range fields[1:] {
				key, value, ok := strings.Cut(field, "=")
				if !ok {
					return nil, fmt.Errorf("INVENTORY_ERROR: invalid variable")
				}
				switch key {
				case "ansible_host":
					if value == "" {
						return nil, fmt.Errorf("INVENTORY_ERROR: invalid address")
					}
					address = value
				case "ansible_port":
					n, err := strconv.Atoi(value)
					if err != nil || n < 1 || n > 65535 {
						return nil, fmt.Errorf("INVENTORY_ERROR: invalid port")
					}
					port = n
				case "ansible_user":
					if err := setUser(fields[0], value); err != nil {
						return nil, err
					}
				default:
					return nil, fmt.Errorf("INVENTORY_ERROR: unsupported variable %s", key)
				}
			}
			if err := add(fields[0], address, port, []string{group}); err != nil {
				return nil, err
			}
		}
	case "yaml":
		doc, err := safeyaml.Parse(strings.NewReader(source))
		if err != nil {
			return nil, err
		}
		var walk func(string, any, int) error
		walk = func(group string, value any, depth int) error {
			if group == "" || strings.ContainsAny(group, " \t\r\n\x00{}") {
				return fmt.Errorf("INVENTORY_ERROR: invalid group")
			}
			declaredGroups[group] = true
			if len(declaredGroups) > 1024 {
				return fmt.Errorf("INVENTORY_ERROR: group limit exceeded")
			}
			if depth > 64 {
				return fmt.Errorf("INVENTORY_ERROR: group nesting too deep")
			}
			if value == nil {
				return nil
			}
			body, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("INVENTORY_ERROR: invalid group")
			}
			for key := range body {
				if key != "hosts" && key != "children" && key != "vars" {
					return fmt.Errorf("INVENTORY_ERROR: unsupported group field %s", key)
				}
			}
			if vars, exists := body["vars"]; exists {
				fields, ok := vars.(map[string]any)
				if !ok {
					return fmt.Errorf("INVENTORY_ERROR: group vars must be a map")
				}
				for key, value := range fields {
					switch value.(type) {
					case string, int, int64, float64:
					default:
						return fmt.Errorf("INVENTORY_ERROR: invalid group variable")
					}
					if err := setGroupVar(group, key, fmt.Sprint(value)); err != nil {
						return err
					}
				}
			}
			if entries, exists := body["hosts"]; exists && entries != nil {
				entries, ok := entries.(map[string]any)
				if !ok {
					return fmt.Errorf("INVENTORY_ERROR: hosts must be a map")
				}
				for id, vars := range entries {
					address, port := "", 0
					if vars != nil {
						fields, ok := vars.(map[string]any)
						if !ok {
							return fmt.Errorf("INVENTORY_ERROR: invalid host variables")
						}
						for key, value := range fields {
							switch key {
							case "ansible_host":
								s, ok := value.(string)
								if !ok || s == "" {
									return fmt.Errorf("INVENTORY_ERROR: invalid address")
								}
								address = s
							case "ansible_port":
								n, err := strconv.Atoi(fmt.Sprint(value))
								if err != nil || n < 1 || n > 65535 {
									return fmt.Errorf("INVENTORY_ERROR: invalid port")
								}
								port = n
							case "ansible_user":
								user, ok := value.(string)
								if !ok {
									return fmt.Errorf("INVENTORY_ERROR: invalid login user")
								}
								if err := setUser(id, user); err != nil {
									return err
								}
							default:
								return fmt.Errorf("INVENTORY_ERROR: unsupported host variable %s", key)
							}
						}
					}
					if err := add(id, address, port, []string{group}); err != nil {
						return err
					}
				}
			}
			if entries, exists := body["children"]; exists && entries != nil {
				entries, ok := entries.(map[string]any)
				if !ok {
					return fmt.Errorf("INVENTORY_ERROR: children must be a map")
				}
				for name, child := range entries {
					children[group] = append(children[group], name)
					if err := walk(name, child, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for group, value := range doc {
			if err := walk(group, value, 0); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("INVENTORY_ERROR: unsupported format")
	}
	var contains func(string, string, map[string]bool) (bool, error)
	memo := map[[2]string]bool{}
	computed := map[[2]string]bool{}
	contains = func(parent, target string, seen map[string]bool) (bool, error) {
		if len(seen) > 64 {
			return false, fmt.Errorf("INVENTORY_ERROR: group nesting too deep")
		}
		if seen[parent] {
			return false, fmt.Errorf("INVENTORY_ERROR: cyclic groups")
		}
		key := [2]string{parent, target}
		if computed[key] {
			return memo[key], nil
		}
		seen[parent] = true
		defer delete(seen, parent)
		found := false
		for _, child := range children[parent] {
			if child == target {
				found = true
			}
			nested, err := contains(child, target, seen)
			if err != nil {
				return false, err
			}
			found = found || nested
		}
		computed[key] = true
		memo[key] = found
		return found, nil
	}
	for parent := range children {
		for _, child := range children[parent] {
			if !declaredGroups[child] {
				return nil, fmt.Errorf("INVENTORY_ERROR: undefined child group")
			}
		}
		if _, err := contains(parent, "", map[string]bool{}); err != nil {
			return nil, err
		}
	}
	result := make([]Host, 0, len(hosts))
	endpoints := map[string]bool{}
	for _, host := range hosts {
		host.Username = usernames[host.ID]
		groups := map[string]bool{}
		for _, g := range host.Groups {
			groups[g] = true
			for parent := range children {
				yes, _ := contains(parent, g, map[string]bool{})
				if yes {
					groups[parent] = true
				}
			}
		}
		for _, key := range []string{"ansible_user", "ansible_port"} {
			if key == "ansible_user" && host.Username != "" || key == "ansible_port" && explicitPorts[host.ID] {
				continue
			}
			value, found := "", false
			for group := range groups {
				candidate, exists := groupVars[group][key]
				if !exists {
					continue
				}
				shadowed := false
				for descendant := range groups {
					if descendant == group {
						continue
					}
					if _, exists := groupVars[descendant][key]; !exists {
						continue
					}
					nested, _ := contains(group, descendant, map[string]bool{})
					if group == "all" || nested {
						shadowed = true
						break
					}
				}
				if shadowed {
					continue
				}
				if found && value != candidate {
					return nil, fmt.Errorf("INVENTORY_ERROR: conflicting group connection defaults")
				}
				value, found = candidate, true
			}
			if found {
				if key == "ansible_user" {
					host.Username = value
				} else {
					host.Port, _ = strconv.Atoi(value)
				}
			}
		}
		endpoint := net.JoinHostPort(host.Address, strconv.Itoa(host.Port))
		if endpoints[endpoint] {
			return nil, fmt.Errorf("INVENTORY_ERROR: duplicate endpoint")
		}
		endpoints[endpoint] = true
		host.Groups = nil
		for group := range groups {
			host.Groups = append(host.Groups, group)
		}
		sort.Strings(host.Groups)
		result = append(result, *host)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
