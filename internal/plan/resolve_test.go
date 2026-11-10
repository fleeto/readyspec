package plan_test

import (
	"encoding/json"
	"errors"
	"github.com/fleeto/readyspec/internal/plan"
	"os"
	"strings"
	"testing"
)

func TestSharedCapacityContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/capacity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Value string `json:"value"`
		Bytes int64  `json:"bytes"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	definition := map[string]any{"type": "object", "properties": map[string]any{"size": map[string]any{"type": "string", "x-readyspec-type": "capacity"}}}
	for _, fixture := range fixtures {
		values, err := plan.ResolveParameters(definition, plan.CheckItem{ItemID: "memory", Params: map[string]any{"size": fixture.Value}}, nil, nil)
		if fixture.Code == "" {
			if err != nil || values["size"].Value != fixture.Bytes {
				t.Fatalf("%s values=%+v err=%v", fixture.Value, values, err)
			}
			continue
		}
		var issue *plan.ParameterError
		if !errors.As(err, &issue) || issue.Code != fixture.Code {
			t.Fatalf("%s err=%v", fixture.Value, err)
		}
	}
}

func TestCapacityValidationLocatesSafeFieldWithoutEchoingInput(t *testing.T) {
	definition := map[string]any{"type": "object", "required": []any{"size"}, "properties": map[string]any{"size": map[string]any{"type": "string", "x-readyspec-type": "capacity"}}}
	for _, tc := range []struct {
		value any
		code  string
	}{
		{"4G-private-fixture", "CAPACITY_FORMAT"}, {"0.1 B", "CAPACITY_RANGE"}, {"9223372036854775808 B", "CAPACITY_RANGE"}, {nil, "REQUIRED"},
	} {
		item := plan.CheckItem{ItemID: "memory", Params: map[string]any{}}
		if tc.value != nil {
			item.Params["size"] = tc.value
		}
		_, err := plan.ResolveParameters(definition, item, nil, nil)
		var issue *plan.ParameterError
		if !errors.As(err, &issue) || issue.Code != tc.code || issue.Field != "/params/size" || issue.ItemID != "memory" {
			t.Fatalf("value=%v issue=%+v err=%v", tc.value, issue, err)
		}
		if strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("validation echoed submitted data")
		}
	}
}

func TestParameterFieldUsesJSONPointerAndPreservesExplicitNull(t *testing.T) {
	definition := map[string]any{"type": "object", "required": []string{"a/b~c"}, "properties": map[string]any{"a/b~c": map[string]any{"type": []string{"string", "null"}, "x-readyspec-type": "capacity", "default": "4 GiB"}}}
	item := plan.CheckItem{ItemID: "sample", Params: map[string]any{"a/b~c": "4G"}}
	_, err := plan.ResolveParameters(definition, item, nil, nil)
	var issue *plan.ParameterError
	if !errors.As(err, &issue) || issue.Field != "/params/a~1b~0c" {
		t.Fatalf("incorrect pointer: %v", err)
	}
	item.Params = nil
	item.ExplicitNullParams = []string{"a/b~c"}
	values, err := plan.ResolveParameters(definition, item, nil, nil)
	if err != nil || values["a/b~c"].Value != nil || values["a/b~c"].Source != "item" {
		t.Fatalf("explicit null replaced by default: %+v %v", values, err)
	}
}

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
