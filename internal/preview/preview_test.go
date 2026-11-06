package preview_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/template"
)

func TestPreviewBindsRolesAndAppliesHostOverrideWithoutChangingPlan(t *testing.T) {
	doc, _ := template.Lookup("S01", "1.0.0")
	raw, _ := json.Marshal(doc)
	var snapshot plan.CheckTemplate
	_ = json.Unmarshal(raw, &snapshot)
	item := plan.CheckItem{ItemID: "path", Name: "Path", Enabled: true, Importance: "REQUIRED", TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot.Digest, Params: map[string]any{"path": "/original"}, OverridableParams: []string{"path"}}
	p := &plan.Plan{EmbeddedTemplates: []plan.CheckTemplate{snapshot}, Roles: []plan.Role{{RoleID: "app", MinNodes: 1, Items: []plan.CheckItem{item}}}}
	hosts, err := inventory.Parse("node=10.0.0.1,unused=10.0.0.2", "quick")
	if err != nil {
		t.Fatal(err)
	}
	request := preview.Input{Hosts: hosts, RoleMap: map[string][]string{"app": {"node"}}, Overrides: []preview.Override{{Scope: "host", Target: "node", ItemID: "path", Parameter: "path", Value: json.RawMessage(`"/host"`)}}}
	result, err := preview.Build(p, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.HostCount != 1 || result.CheckCount != 1 || len(result.Hosts) != 1 || !strings.Contains(string(result.Hosts[0].Artifacts.Standard.Config), "/host") {
		t.Fatalf("unexpected preview %+v", result)
	}
	if item.Params["path"] != "/original" {
		t.Fatal("preview modified plan")
	}
	request.Overrides[0].Target = "unused"
	if _, err := preview.Build(p, request); err == nil {
		t.Fatal("ineffective override accepted")
	}
	request.Overrides = nil
	request.RoleMap["app"] = []string{"missing"}
	if _, err := preview.Build(p, request); err == nil {
		t.Fatal("unknown host binding accepted")
	}
}
