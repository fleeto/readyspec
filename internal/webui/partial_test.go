package webui

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
)

func TestAnomalousHostsSelectsItemAndHostFailures(t *testing.T) {
	value := report.RunReport{Hosts: []report.HostResult{
		{HostID: "pass", ItemResults: []compiler.ItemResult{{Status: "PASS"}}},
		{HostID: "item-error", ItemResults: []compiler.ItemResult{{Status: "ERROR"}}},
		{HostID: "host-error", Failures: []report.Failure{{Code: "SSH_FAILED"}}},
		{HostID: "skip", ItemResults: []compiler.ItemResult{{Status: "SKIP"}}},
	}}
	got := anomalousHosts(value)
	want := map[string]bool{"item-error": true, "host-error": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anomalous host set = %#v, want %#v", got, want)
	}
}

func TestValidatePartialSelectionBindsSourcePlanAndFrozenHostIdentity(t *testing.T) {
	host := inventory.Host{ID: "node-a", Address: "192.0.2.1", Port: 22, Groups: []string{"app"}}
	parent := runstore.Run{
		Config: runstore.RunConfig{PlanDigest: "sha256:plan", Inventory: []inventory.Host{host}},
		Report: &report.RunReport{Hosts: []report.HostResult{
			{HostID: "node-a", ItemResults: []compiler.ItemResult{{Status: "FAIL"}}},
			{HostID: "node-b", ItemResults: []compiler.ItemResult{{Status: "PASS"}}},
		}},
	}
	for _, testCase := range []struct {
		name      string
		digest    string
		hosts     []inventory.Host
		requested []string
		wantError string
	}{
		{name: "eligible failure host", digest: "sha256:plan", hosts: []inventory.Host{host}, requested: []string{"node-a"}},
		{name: "different plan", digest: "sha256:other", hosts: []inventory.Host{host}, requested: []string{"node-a"}, wantError: "PARTIAL_SOURCE_RUN_UNAVAILABLE"},
		{name: "passing host", digest: "sha256:plan", hosts: []inventory.Host{host}, requested: []string{"node-b"}, wantError: "PARTIAL_HOST_NOT_ELIGIBLE"},
		{name: "changed host address", digest: "sha256:plan", hosts: []inventory.Host{{ID: "node-a", Address: "192.0.2.9", Port: 22, Groups: []string{"app"}}}, requested: []string{"node-a"}, wantError: "PARTIAL_INVENTORY_CHANGED"},
		{name: "duplicate host", digest: "sha256:plan", hosts: []inventory.Host{host}, requested: []string{"node-a", "node-a"}, wantError: "PARTIAL_HOST_NOT_ELIGIBLE"},
		{name: "empty selection", digest: "sha256:plan", hosts: []inventory.Host{host}, wantError: "PARTIAL_HOSTS_REQUIRED"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			selected := map[string]bool{}
			filtered, gotError := validatePartialSelection(parent, testCase.digest, testCase.hosts, testCase.requested, selected)
			if gotError != testCase.wantError {
				t.Fatalf("validation error = %q, want %q", gotError, testCase.wantError)
			}
			if gotError == "" && (len(filtered) != 1 || filtered[0].ID != "node-a" || !selected["node-a"]) {
				t.Fatalf("unexpected successful projection: hosts=%+v selection=%v", filtered, selected)
			}
		})
	}
}

func TestFilterPartialInputsProjectsInventoryBindingsAndOverrides(t *testing.T) {
	hosts := []inventory.Host{
		{ID: "node-a", Address: "192.0.2.1", Port: 22, Groups: []string{"app"}},
		{ID: "node-b", Address: "192.0.2.2", Port: 22, Groups: []string{"db"}},
	}
	roleMap := map[string][]string{"app": {"node-a", "node-b"}, "db": {"node-b"}}
	overrides := []preview.Override{
		{Scope: "host", Target: "node-a"},
		{Scope: "host", Target: "node-b"},
		{Scope: "group", Target: "app"},
		{Scope: "group", Target: "db"},
	}
	projectedHosts, projectedRoles, projectedOverrides := filterPartialInputs(hosts, roleMap, overrides, map[string]bool{"node-a": true})
	if len(projectedHosts) != 1 || projectedHosts[0].ID != "node-a" {
		t.Fatalf("unexpected projected inventory: %+v", projectedHosts)
	}
	if !reflect.DeepEqual(projectedRoles, map[string][]string{"app": {"node-a"}}) {
		t.Fatalf("unexpected role projection: %#v", projectedRoles)
	}
	if len(projectedOverrides) != 2 || projectedOverrides[0].Target != "node-a" || projectedOverrides[1].Target != "app" {
		t.Fatalf("unexpected override projection: %+v", projectedOverrides)
	}
	if _, err := json.Marshal(projectedHosts); err != nil {
		t.Fatal(err)
	}
}

func TestRunProjectionReplaysOnlyPhasePrefix(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	run := runstore.Run{Status: "RUNNING", Plan: &plan.Plan{PlanID: "projection", Name: "Projection", Version: "1.0.0"}, Events: []runstore.Event{
		{Sequence: 1, Kind: "PHASE", HostID: "node-a", Status: "CONNECTING", At: base},
		{Sequence: 2, Kind: "PHASE", HostID: "node-a", Status: "DISCOVERING", At: base.Add(time.Second)},
		{Sequence: 3, Kind: "PHASE", HostID: "node-a", Mode: "STANDARD", Status: "RUNNING_STANDARD", At: base.Add(2 * time.Second)},
		{Sequence: 4, Kind: "PHASE", HostID: "node-b", Status: "CONNECTING", At: base.Add(3 * time.Second)},
	}}
	projection := projectRun(run, 3)
	phases, ok := projection["host_phases"].([]hostPhaseProjection)
	if !ok {
		t.Fatalf("unexpected phase projection type: %T", projection["host_phases"])
	}
	if len(phases) != 2 || phases[0].HostID != "node-a" || phases[0].Phase != "DISCOVERING" || phases[1].Phase != "RUNNING" || phases[1].Mode != "STANDARD" {
		t.Fatalf("projection did not respect the event prefix: %+v", phases)
	}
	transitions, ok := projection["stage_transitions"].([]stageTransitionProjection)
	if !ok || len(transitions) != 3 {
		t.Fatalf("unexpected transition prefix: %#v", projection["stage_transitions"])
	}
	if transitions[0].Attempt != 1 || transitions[0].StartedAt == nil || transitions[0].EndedAt == nil || !transitions[0].EndedAt.Equal(base.Add(time.Second)) {
		t.Fatalf("transition start/end projection is incorrect: %+v", transitions[0])
	}
	if transitions[2].EndedAt != nil {
		t.Fatalf("current phase should not have a fabricated end time: %+v", transitions[2])
	}
}

func TestRunProjectionMarksUnconfiguredExecutionModesOmitted(t *testing.T) {
	run := runstore.Run{
		Plan:   &plan.Plan{PlanID: "omitted", EmbeddedTemplates: []plan.CheckTemplate{}, Roles: []plan.Role{}},
		Config: runstore.RunConfig{Inventory: []inventory.Host{{ID: "node-a"}}},
	}
	projection := projectRun(run, 0)
	transitions := projection["stage_transitions"].([]stageTransitionProjection)
	if len(transitions) != 2 {
		t.Fatalf("expected both unused execution modes to be explicit, got %+v", transitions)
	}
	for _, transition := range transitions {
		if !transition.Omitted || transition.Phase != "OMITTED" || transition.Reason != "NO_ENABLED_ITEMS" || transition.StartedAt != nil || transition.EndedAt != nil {
			t.Fatalf("omitted mode fabricated execution timestamps: %+v", transition)
		}
	}
}

func TestRunProjectionTreatsFinishedAsBoundaryAndPreservesHostModeStages(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	run := runstore.Run{Plan: &plan.Plan{PlanID: "stages", Name: "Stages", Version: "1.0.0"}, Events: []runstore.Event{
		{Sequence: 1, Kind: "PHASE", HostID: "node-a", Status: "PREPARING", At: base},
		{Sequence: 2, Kind: "EXECUTION_STARTED", HostID: "node-a", Mode: "STANDARD", At: base.Add(time.Second)},
		{Sequence: 3, Kind: "PHASE", HostID: "node-a", Mode: "STANDARD", Attempt: 1, Status: "RUNNING_STANDARD", At: base.Add(2 * time.Second)},
		{Sequence: 4, Kind: "PHASE", HostID: "node-a", Mode: "STANDARD", Attempt: 1, Status: "COLLECTING_STANDARD", At: base.Add(3 * time.Second)},
		{Sequence: 5, Kind: "PHASE", HostID: "node-a", Status: "FINISHED", At: base.Add(4 * time.Second)},
	}}
	projection := projectRun(run, 5)
	transitions := projection["stage_transitions"].([]stageTransitionProjection)
	if len(transitions) != 3 {
		t.Fatalf("FINISHED must close the prior stage, not appear as a stage: %+v", transitions)
	}
	if transitions[0].Phase != "PREPARING" || transitions[0].EndedAt == nil || !transitions[0].EndedAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("preparation boundary is wrong: %+v", transitions[0])
	}
	if transitions[1].Phase != "RUNNING" || transitions[1].Mode != "STANDARD" || transitions[1].Attempt != 1 || transitions[1].EndedAt == nil || !transitions[1].EndedAt.Equal(base.Add(3*time.Second)) {
		t.Fatalf("execution boundary is wrong: %+v", transitions[1])
	}
	if transitions[2].Phase != "COLLECTING" || transitions[2].Mode != "STANDARD" || transitions[2].EndedAt == nil || !transitions[2].EndedAt.Equal(base.Add(4*time.Second)) {
		t.Fatalf("terminal boundary is wrong: %+v", transitions[2])
	}
}
