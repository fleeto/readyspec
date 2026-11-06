package plan_test

import (
	"github.com/fleeto/readyspec/internal/plan"
	"testing"
)

func TestResolveOverridesTracksSourceAndRejectsGroupConflict(t *testing.T) {
	definition := map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "default": "/data"}}}
	item := plan.CheckItem{ItemID: "data", Params: map[string]any{}, OverridableParams: []string{"path"}}
	groups := map[string]map[string]any{"web": {"path": "/web"}, "db": {"path": "/db"}}
	if _, err := plan.ResolveParameters(definition, item, groups, nil); err == nil {
		t.Fatal("conflicting groups accepted")
	}
	values, err := plan.ResolveParameters(definition, item, groups, map[string]any{"path": "/host"})
	if err != nil {
		t.Fatal(err)
	}
	if values["path"].Value != "/host" || values["path"].Source != "host" {
		t.Fatalf("unexpected resolution: %+v", values)
	}
	item.OverridableParams = nil
	if _, err := plan.ResolveParameters(definition, item, nil, map[string]any{"path": "/host"}); err == nil {
		t.Fatal("closed parameter overridden")
	}
}
