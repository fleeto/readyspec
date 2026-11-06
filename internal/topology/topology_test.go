package topology_test

import (
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/topology"
	"testing"
)

func TestVerdictRequiresReleasedRealFullCompletedGate(t *testing.T) {
	input := topology.VerdictInput{PlanState: "RELEASED", Mode: "GATE", Scope: "FULL", Executor: "native_ssh", Status: "COMPLETED", Required: []string{"PASS"}, Topology: []string{"PASS"}}
	if got := topology.Verdict(input); got != "READY" {
		t.Fatal(got)
	}
	input.Executor = "mock_simulator"
	if got := topology.Verdict(input); got != "UNKNOWN" {
		t.Fatal(got)
	}
	input.Required = []string{"FAIL"}
	if got := topology.Verdict(input); got != "NOT_READY" {
		t.Fatal(got)
	}
	input.Required = nil
	if got := topology.Verdict(input); got != "UNKNOWN" {
		t.Fatal(got)
	}
}

func TestIdentityConflictAndPartialCoverageCannotPass(t *testing.T) {
	p := &plan.Plan{TopologyPolicy: plan.TopologyPolicy{AllowedArch: []string{"x86_64"}}, Roles: []plan.Role{{RoleID: "app", MinNodes: 1}}}
	hosts := []topology.Host{{ID: "a", Arch: "x86_64", MachineID: "same", BootID: "boot", HostKey: "key", Roles: []string{"app"}}, {ID: "b", Arch: "x86_64", MachineID: "same", BootID: "boot", HostKey: "key", Roles: []string{"app"}}}
	results := topology.Check(p, hosts, "FULL", nil)
	found := false
	for _, r := range results {
		if r.Code == "IDENTITY" && r.Status == "ERROR" {
			found = true
		}
	}
	if !found {
		t.Fatalf("duplicate identity passed: %+v", results)
	}
	results = topology.Check(p, hosts[:1], "PARTIAL", nil)
	found = false
	for _, r := range results {
		if r.Code == "ROLE_COUNT" && r.Status == "ERROR" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial topology passed: %+v", results)
	}
}
