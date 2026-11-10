package publicview

import (
	"encoding/json"
	"github.com/fleeto/readyspec/internal/plan"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
)

func TestDisplayMetadataUsesFrozenNamesAndOnlyWhitelistedFields(t *testing.T) {
	run := runstore.Run{Acceptance: runstore.Acceptance{RunID: "run-test", PlanDigest: "digest"}, Plan: &plan.Plan{
		Roles:             []plan.Role{{RoleID: "app", Items: []plan.CheckItem{{ItemID: "memory", Name: "Frozen memory", Importance: "WARNING", Enabled: true, TemplateID: "H03", TemplateVersion: "1", TemplateDigest: "snapshot", Params: map[string]any{"secret": "private-fixture"}}}}},
		EmbeddedTemplates: []plan.CheckTemplate{{TemplateID: "H03", Version: "1", Digest: "snapshot", Name: "Memory template"}},
	}}
	view := DisplayMetadataValue(run)
	encoded, _ := json.Marshal(view)
	if len(view.Items) != 1 || view.Items[0].ItemName != "Frozen memory" || view.Items[0].TemplateName != "Memory template" || view.Items[0].Importance != "WARNING" || !view.Items[0].Enabled || strings.Contains(string(encoded), "private-fixture") {
		t.Fatalf("incorrect display metadata: %s", encoded)
	}
	run.Plan.EmbeddedTemplates[0].Digest = "other"
	if DisplayMetadataValue(run).Items[0].TemplateName != "" {
		t.Fatal("used mismatched template")
	}
}

func TestRunValueRedactsPrivateCleanupMaterialWithoutMutatingSource(t *testing.T) {
	secret := "owner-token-fixture"
	data, err := json.Marshal(report.CleanupItem{CleanupID: "cleanup-a", HostID: "node-a", OwnerToken: secret})
	if err != nil {
		t.Fatal(err)
	}
	run := runstore.Run{
		Acceptance: runstore.Acceptance{RunID: "run-fixture", IdempotencyKey: "private-idempotency-key"},
		Events: []runstore.Event{
			{Kind: "CLEANUP_REGISTERED", HostID: "node-a", Data: data},
			{Kind: "FUTURE_UNKNOWN_EVENT", Data: json.RawMessage(`{"secret":"owner-token-fixture"}`)},
		},
		Report: &report.RunReport{CleanupItems: []report.CleanupItem{{CleanupID: "cleanup-a", OwnerToken: secret}}},
	}
	public := RunValue(run)
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), run.Acceptance.IdempotencyKey) {
		t.Fatalf("public view leaked private material: %s", encoded)
	}
	if !strings.Contains(string(run.Events[0].Data), secret) || run.Report.CleanupItems[0].OwnerToken != secret {
		t.Fatal("public projection mutated the internal recovery material")
	}
	if len(public.Events) != 2 || len(public.Events[1].Data) != 0 {
		t.Fatalf("unknown event data was not omitted from public view: %+v", public.Events)
	}
}
