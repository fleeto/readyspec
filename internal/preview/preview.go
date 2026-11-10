// Package preview assembles a static execution preview without network or process access.
package preview

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
)

type Override struct {
	Scope     string          `json:"scope"`
	Target    string          `json:"target"`
	ItemID    string          `json:"item_id"`
	Parameter string          `json:"parameter"`
	Value     json.RawMessage `json:"value"`
}

type Input struct {
	Hosts     []inventory.Host    `json:"hosts"`
	RoleMap   map[string][]string `json:"role_map"`
	Overrides []Override          `json:"field_overrides,omitempty"`
}

type HostPreview struct {
	Host      inventory.Host              `json:"host"`
	Roles     []string                    `json:"roles"`
	Artifacts compiler.ExecutionArtifacts `json:"artifacts"`
}

type Result struct {
	HostCount               int           `json:"host_count"`
	CheckCount              int           `json:"candidate_check_count"`
	PrivilegedCount         int           `json:"privileged_check_count"`
	CustomShellCount        int           `json:"custom_shell_check_count"`
	ActiveProbeCount        int           `json:"active_probe_check_count"`
	Hosts                   []HostPreview `json:"hosts"`
	NetworkAccessed         bool          `json:"network_accessed"`
	ConfiguredRootHosts     []string      `json:"configured_root_hosts"`
	IdentityUnverifiedHosts []string      `json:"identity_unverified_hosts"`
}

func Build(p *plan.Plan, input Input) (Result, error) {
	result := Result{Hosts: []HostPreview{}, ConfiguredRootHosts: []string{}, IdentityUnverifiedHosts: []string{}}
	hosts := map[string]inventory.Host{}
	for _, host := range input.Hosts {
		if host.ID == "" || host.Address == "" || host.Port < 1 || host.Port > 65535 {
			return result, fmt.Errorf("PLAN_ERROR: invalid inventory host")
		}
		if _, exists := hosts[host.ID]; exists {
			return result, fmt.Errorf("PLAN_ERROR: duplicate inventory host")
		}
		hosts[host.ID] = host
	}
	roles := map[string]plan.Role{}
	items := map[string]plan.CheckItem{}
	snapshots := map[string]plan.CheckTemplate{}
	for _, snapshot := range p.EmbeddedTemplates {
		snapshots[snapshot.TemplateID+"@"+snapshot.Version] = snapshot
	}
	for _, role := range p.Roles {
		roles[role.RoleID] = role
		for _, item := range role.Items {
			items[item.ItemID] = item
		}
	}
	bindings := map[string][]string{}
	for roleID, targets := range input.RoleMap {
		if _, exists := roles[roleID]; !exists {
			return result, fmt.Errorf("PLAN_ERROR: unknown role binding")
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if _, exists := hosts[target]; !exists {
				return result, &plan.ParameterError{Code: "UNKNOWN_HOST", Field: "/role_map", RoleID: roleID}
			}
			if seen[target] {
				return result, fmt.Errorf("PLAN_ERROR: duplicate host binding")
			}
			seen[target] = true
			bindings[target] = append(bindings[target], roleID)
		}
	}
	for _, role := range p.Roles {
		count := len(input.RoleMap[role.RoleID])
		if count < role.MinNodes || (role.MaxNodes != nil && count > *role.MaxNodes) {
			return result, fmt.Errorf("PLAN_ERROR: node count violates role %s", role.RoleID)
		}
	}
	for _, bound := range bindings {
		if len(bound) > 1 {
			for _, roleID := range bound {
				if !roles[roleID].AllowColocation {
					return result, fmt.Errorf("PLAN_ERROR: colocation prohibited by role %s", roleID)
				}
			}
		}
	}
	groupOverrides := map[string]map[string]map[string]map[string]any{}
	hostOverrides := map[string]map[string]map[string]any{}
	seenOverrides := map[[4]string]bool{}
	for _, override := range input.Overrides {
		item, exists := items[override.ItemID]
		if !exists || !item.Enabled {
			return result, fmt.Errorf("OVERRIDE_REJECTED: unknown or disabled check item")
		}
		if override.Parameter == "" || len(override.Value) == 0 {
			return result, fmt.Errorf("OVERRIDE_REJECTED: missing parameter or value")
		}
		identity := [4]string{override.Scope, override.Target, override.ItemID, override.Parameter}
		if seenOverrides[identity] {
			return result, fmt.Errorf("OVERRIDE_CONFLICT: duplicate field override")
		}
		seenOverrides[identity] = true
		var value any
		if err := json.Unmarshal(override.Value, &value); err != nil {
			return result, fmt.Errorf("OVERRIDE_REJECTED: invalid JSON value")
		}
		applicable := false
		for hostID, bound := range bindings {
			matches := false
			for _, roleID := range bound {
				for _, candidate := range roles[roleID].Items {
					if candidate.ItemID == override.ItemID {
						matches = true
					}
				}
			}
			if !matches {
				continue
			}
			targetMatches := false
			if override.Scope == "host" {
				targetMatches = hostID == override.Target
			} else if override.Scope == "group" {
				for _, group := range hosts[hostID].Groups {
					if group == override.Target {
						targetMatches = true
					}
				}
			} else {
				return result, fmt.Errorf("OVERRIDE_REJECTED: invalid scope")
			}
			if !targetMatches {
				continue
			}
			applicable = true
			if override.Scope == "host" {
				if hostOverrides[hostID] == nil {
					hostOverrides[hostID] = map[string]map[string]any{}
				}
				if hostOverrides[hostID][override.ItemID] == nil {
					hostOverrides[hostID][override.ItemID] = map[string]any{}
				}
				hostOverrides[hostID][override.ItemID][override.Parameter] = value
			} else {
				if groupOverrides[hostID] == nil {
					groupOverrides[hostID] = map[string]map[string]map[string]any{}
				}
				if groupOverrides[hostID][override.ItemID] == nil {
					groupOverrides[hostID][override.ItemID] = map[string]map[string]any{}
				}
				if groupOverrides[hostID][override.ItemID][override.Target] == nil {
					groupOverrides[hostID][override.ItemID][override.Target] = map[string]any{}
				}
				groupOverrides[hostID][override.ItemID][override.Target][override.Parameter] = value
			}
		}
		if !applicable {
			return result, fmt.Errorf("OVERRIDE_REJECTED: override does not target a bound check")
		}
	}
	ids := []string{}
	for id := range bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if hosts[id].Username == "root" {
			result.ConfiguredRootHosts = append(result.ConfiguredRootHosts, id)
		}
		result.IdentityUnverifiedHosts = append(result.IdentityUnverifiedHosts, id)
		bound := bindings[id]
		sort.Strings(bound)
		artifacts, err := compiler.CompileExecutionModes(p, compiler.HostInput{Roles: bound, GroupOverrides: groupOverrides[id], HostOverrides: hostOverrides[id]})
		if err != nil {
			return result, err
		}
		result.Hosts = append(result.Hosts, HostPreview{Host: hosts[id], Roles: bound, Artifacts: artifacts})
		for _, roleID := range bound {
			for _, item := range roles[roleID].Items {
				if !item.Enabled {
					continue
				}
				result.CheckCount++
				snapshot := snapshots[item.TemplateID+"@"+item.TemplateVersion]
				if snapshot.ExecutionMode == "PRIVILEGED" {
					result.PrivilegedCount++
				}
				if snapshot.Implementation["kind"] == "CUSTOM_SHELL" {
					result.CustomShellCount++
				}
				if snapshot.EvidenceMode == "ACTIVE_PROBE" {
					result.ActiveProbeCount++
				}
			}
		}
	}
	result.HostCount = len(result.Hosts)
	return result, nil
}
