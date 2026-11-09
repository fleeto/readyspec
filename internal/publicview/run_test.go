package publicview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
)

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
