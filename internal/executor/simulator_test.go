package executor

import (
	"context"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
)

type phaseJournal struct{ phases []string }

func (j *phaseJournal) Phase(_ context.Context, _, _, phase string) error {
	j.phases = append(j.phases, phase)
	return nil
}
func (*phaseJournal) ExecutionStarted(context.Context, string, string, string) error      { return nil }
func (*phaseJournal) CleanupRegistered(context.Context, string, report.CleanupItem) error { return nil }
func (*phaseJournal) CleanupUpdated(context.Context, string, report.CleanupItem) error    { return nil }

func TestSimulatorReportsSimulationStagesWithoutClaimingSSHPhases(t *testing.T) {
	journal := &phaseJournal{}
	simulator := Simulator{Profiles: Profiles{Hosts: map[string]Profile{
		"node": {Preflight: "READY", Results: map[string]string{"check": "PASS"}},
	}}}
	host := HostInput{Inventory: inventory.Host{ID: "node"}}
	preflight := simulator.Preflight(context.Background(), PreflightRequest{RunID: "run", Host: host, Journal: journal})
	if preflight.Failure != nil {
		t.Fatalf("simulator preflight failed: %+v", preflight.Failure)
	}
	artifact := compiler.Artifact{ExecutionMode: "STANDARD", Items: []compiler.ItemResult{{
		RoleID: "app", ItemID: "check", Status: "PASS", TemplateID: "custom", TemplateVersion: "1", ExecutionMode: "STANDARD", EvidenceMode: "EXISTENCE",
	}}}
	outcome := simulator.Execute(context.Background(), ExecuteRequest{
		RunID: "run", Plan: &plan.Plan{}, Host: host, Artifacts: compiler.ExecutionArtifacts{Standard: artifact},
	}, journal)
	if outcome.Failure != nil || len(outcome.Results) != 1 || outcome.Results[0].Status != "PASS" {
		t.Fatalf("simulator execution failed: outcome=%+v", outcome)
	}
	want := []string{"SIMULATING_PREFLIGHT", "SIMULATING_STANDARD", "FINISHED"}
	if len(journal.phases) != len(want) {
		t.Fatalf("simulator phases = %v, want %v", journal.phases, want)
	}
	for i := range want {
		if journal.phases[i] != want[i] {
			t.Fatalf("simulator phases = %v, want %v", journal.phases, want)
		}
	}
}
