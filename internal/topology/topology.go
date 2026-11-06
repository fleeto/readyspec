// Package topology evaluates supplied observations without collecting data.
package topology

import (
	"github.com/fleeto/readyspec/internal/plan"
)

type Host struct {
	ID        string
	Arch      string
	MachineID string
	BootID    string
	HostKey   string
	Roles     []string
}

type Result struct {
	Code   string `json:"code"`
	HostID string `json:"host_id,omitempty"`
	RoleID string `json:"role_id,omitempty"`
	Status string `json:"status"`
}

type ItemObservation struct {
	HostID string
	RoleID string
	ItemID string
}

func Check(p *plan.Plan, hosts []Host, scope string, observations []ItemObservation) []Result {
	results := []Result{}
	identities := map[[3]string]bool{}
	hostIDs := map[string]bool{}
	roles := map[string]plan.Role{}
	for _, role := range p.Roles {
		roles[role.RoleID] = role
	}
	counts := map[string]int{}
	seenItems := map[[3]string]int{}
	for _, o := range observations {
		seenItems[[3]string{o.HostID, o.RoleID, o.ItemID}]++
	}
	for _, host := range hosts {
		arch := "FAIL"
		if host.Arch == "" {
			arch = "ERROR"
		}
		for _, allowed := range p.TopologyPolicy.AllowedArch {
			if host.Arch == allowed {
				arch = "PASS"
			}
		}
		results = append(results, Result{Code: "ARCH", HostID: host.ID, Status: arch})
		identity := [3]string{host.MachineID, host.BootID, host.HostKey}
		status := "PASS"
		if host.ID == "" || hostIDs[host.ID] || host.MachineID == "" || host.BootID == "" || host.HostKey == "" || identities[identity] {
			status = "ERROR"
		}
		hostIDs[host.ID] = true
		identities[identity] = true
		results = append(results, Result{Code: "IDENTITY", HostID: host.ID, Status: status})
		seenRoles := map[string]bool{}
		for _, id := range host.Roles {
			role, exists := roles[id]
			if !exists || seenRoles[id] {
				results = append(results, Result{Code: "ROLE_BINDING", HostID: host.ID, RoleID: id, Status: "ERROR"})
				continue
			}
			seenRoles[id] = true
			counts[id]++
			if len(host.Roles) > 1 && !role.AllowColocation {
				results = append(results, Result{Code: "COLOCATION", HostID: host.ID, RoleID: id, Status: "FAIL"})
			}
			for _, item := range role.Items {
				if !item.Enabled || item.Importance != "REQUIRED" {
					continue
				}
				coverage := "PASS"
				if seenItems[[3]string{host.ID, id, item.ItemID}] != 1 {
					coverage = "ERROR"
				}
				results = append(results, Result{Code: "COVERAGE", HostID: host.ID, RoleID: id, Status: coverage})
			}
		}
	}
	for _, role := range p.Roles {
		status := "PASS"
		if scope != "FULL" {
			status = "ERROR"
		} else if counts[role.RoleID] < role.MinNodes || (role.MaxNodes != nil && counts[role.RoleID] > *role.MaxNodes) {
			status = "FAIL"
		}
		results = append(results, Result{Code: "ROLE_COUNT", RoleID: role.RoleID, Status: status})
	}
	return results
}

type VerdictInput struct {
	PlanState string
	Mode      string
	Scope     string
	Executor  string
	Status    string
	Required  []string
	Topology  []string
}

func Verdict(input VerdictInput) string {
	states := append(append([]string{}, input.Required...), input.Topology...)
	for _, state := range states {
		if state == "FAIL" {
			return "NOT_READY"
		}
	}
	if input.PlanState != "RELEASED" || input.Mode != "GATE" || input.Scope != "FULL" || input.Executor != "native_ssh" || input.Status != "COMPLETED" || len(input.Required) == 0 || len(input.Topology) == 0 {
		return "UNKNOWN"
	}
	for _, state := range states {
		if state != "PASS" {
			return "UNKNOWN"
		}
	}
	return "READY"
}
