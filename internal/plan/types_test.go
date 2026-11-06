package plan_test

import (
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
	"testing"
)

func TestUnsupportedTemplateParameterCombinationsAreRejected(t *testing.T) {
	for _, tt := range []struct {
		id     string
		params map[string]any
	}{
		{"A04", map[string]any{"framework": "selinux", "mode": "enabled"}},
	} {
		doc, _ := template.Lookup(tt.id, "1.0.0")
		if _, err := plan.ResolveParameters(doc["params_schema"].(map[string]any), plan.CheckItem{ItemID: "sample", Params: tt.params}, nil, nil); err == nil {
			t.Fatalf("%s accepted unsupported combination", tt.id)
		}
	}
}

func TestNSSNamesCannotBeTruncatedAndAbsenceCannotAssertAttributes(t *testing.T) {
	for _, tt := range []struct {
		id     string
		params map[string]any
	}{
		{"A01", map[string]any{"name": "app\x00other", "exists": true}},
		{"A01", map[string]any{"name": "app", "exists": false, "uid": float64(1000)}},
		{"A02", map[string]any{"name": "group\nother", "members": []any{}}},
		{"A02", map[string]any{"name": "group", "members": []any{"app\x00other"}}},
	} {
		doc, _ := template.Lookup(tt.id, "1.0.0")
		if _, err := plan.ResolveParameters(doc["params_schema"].(map[string]any), plan.CheckItem{ItemID: "identity", Params: tt.params}, nil, nil); err == nil {
			t.Fatalf("%s accepted ambiguous NSS input", tt.id)
		}
	}
}

func TestCapacityOverridesCompareNormalizedValues(t *testing.T) {
	definition := map[string]any{"type": "object", "properties": map[string]any{"size": map[string]any{"type": "string", "x-readyspec-type": "capacity"}}}
	item := plan.CheckItem{ItemID: "memory", Params: map[string]any{"size": "8 GiB"}, OverridableParams: []string{"size"}}
	values, err := plan.ResolveParameters(definition, item, map[string]map[string]any{"a": {"size": "8GiB"}, "b": {"size": "8192 MiB"}}, nil)
	if err != nil || values["size"].Value != int64(8589934592) {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	item.Params["size"] = "8 Gibberish"
	if _, err := plan.ResolveParameters(definition, item, nil, nil); err == nil {
		t.Fatal("invalid capacity accepted")
	}
}

func TestDomainConstraintsRejectInvertedRangesAndInvalidArrayMembers(t *testing.T) {
	definition := map[string]any{"type": "object", "properties": map[string]any{"minimum": map[string]any{"type": "number"}, "maximum": map[string]any{"type": "number"}, "addresses": map[string]any{"type": "array", "items": map[string]any{"type": "string", "x-readyspec-type": "ip_or_cidr"}}}, "x-readyspec-order": []any{[]any{"minimum", "maximum"}}}
	item := plan.CheckItem{ItemID: "range", Params: map[string]any{"minimum": float64(4), "maximum": float64(2)}}
	if _, err := plan.ResolveParameters(definition, item, nil, nil); err == nil {
		t.Fatal("inverted range accepted")
	}
	item.Params = map[string]any{"addresses": []any{"invalid-address"}}
	if _, err := plan.ResolveParameters(definition, item, nil, nil); err == nil {
		t.Fatal("invalid address in array accepted")
	}
}
