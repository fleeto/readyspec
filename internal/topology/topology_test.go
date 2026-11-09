package topology_test

import (
	"reflect"
	"testing"

	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/topology"
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

func TestVerdictFailurePrecedenceAndReadinessRequirements(t *testing.T) {
	base := topology.VerdictInput{
		PlanState: "RELEASED", Mode: "GATE", Scope: "FULL", Executor: "native_ssh", Status: "COMPLETED",
		Required: []string{"PASS"}, Topology: []string{"PASS"},
	}
	tests := []struct {
		name   string
		change func(*topology.VerdictInput)
		want   string
	}{
		{name: "complete evidence is ready", want: "READY"},
		{name: "topology failure outranks ineligible run", change: func(v *topology.VerdictInput) { v.Scope = "PARTIAL"; v.Topology = []string{"FAIL"} }, want: "NOT_READY"},
		{name: "required failure outranks incomplete evidence", change: func(v *topology.VerdictInput) { v.Status = "FAILED"; v.Required = []string{"FAIL"} }, want: "NOT_READY"},
		{name: "error evidence is unknown", change: func(v *topology.VerdictInput) { v.Topology = []string{"ERROR"} }, want: "UNKNOWN"},
		{name: "draft plan cannot pass", change: func(v *topology.VerdictInput) { v.PlanState = "DRAFT" }, want: "UNKNOWN"},
		{name: "trial cannot pass", change: func(v *topology.VerdictInput) { v.Mode = "TRIAL" }, want: "UNKNOWN"},
		{name: "partial scope cannot pass", change: func(v *topology.VerdictInput) { v.Scope = "PARTIAL" }, want: "UNKNOWN"},
		{name: "simulator cannot pass", change: func(v *topology.VerdictInput) { v.Executor = "mock_simulator" }, want: "UNKNOWN"},
		{name: "partial run cannot pass", change: func(v *topology.VerdictInput) { v.Status = "PARTIAL" }, want: "UNKNOWN"},
		{name: "missing required evidence is unknown", change: func(v *topology.VerdictInput) { v.Required = nil }, want: "UNKNOWN"},
		{name: "missing topology evidence is unknown", change: func(v *topology.VerdictInput) { v.Topology = nil }, want: "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.Required = append([]string(nil), base.Required...)
			input.Topology = append([]string(nil), base.Topology...)
			if tt.change != nil {
				tt.change(&input)
			}
			if got := topology.Verdict(input); got != tt.want {
				t.Fatalf("Verdict()=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestVerdictWithReasonsKeepsVerdictOrderingAndReportsMultipleCauses(t *testing.T) {
	input := topology.VerdictInput{
		PlanState: "DRAFT", Mode: "TRIAL", Scope: "PARTIAL", Executor: "mock_simulator", Status: "FAILED",
		Required: []string{"FAIL", "ERROR"}, Topology: []string{"FAIL", "ERROR"},
	}
	verdict, reasons := topology.VerdictWithReasons(input)
	if verdict != "NOT_READY" {
		t.Fatalf("verdict changed: %q", verdict)
	}
	want := []string{"REQUIRED_CHECK_FAILED", "TOPOLOGY_FAILED", "REQUIRED_EVIDENCE_ERROR", "TOPOLOGY_EVIDENCE_ERROR", "TRIAL_NOT_GATE", "PARTIAL_SCOPE", "SIMULATOR_BACKEND", "RUN_NOT_COMPLETED", "PLAN_NOT_RELEASED"}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("reasons=%v want=%v", reasons, want)
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
