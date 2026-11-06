package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/template"
)

const minimalPlan = `schema_version: readyspec.plan/v1
plan_id: demo
name: Demo
software_name: demo
software_version: '1.0'
version: '1.0.0'
state: DRAFT
topology_policy:
  allowed_arch: [x86_64]
roles:
  - role_id: web
    name: Web
    min_nodes: 1
    max_nodes: 2
    allow_colocation: false
    items: []
embedded_templates: []
template_lock: {}
`

func TestLoadsNewPlanVocabulary(t *testing.T) {
	p, err := plan.Load(strings.NewReader(minimalPlan))
	if err != nil {
		t.Fatal(err)
	}
	if p.PlanID != "demo" || p.State != "DRAFT" || len(p.Roles) != 1 || !strings.HasPrefix(p.Digest, "sha256:") {
		t.Fatalf("unexpected plan: %+v", p)
	}
}

func TestBuiltinSnapshotCannotSelectCompilerAfterTampering(t *testing.T) {
	doc, err := safeyaml.Parse(strings.NewReader(minimalPlan))
	if err != nil {
		t.Fatal(err)
	}
	builtin, _ := template.Lookup("S01", "1.0.0")
	doc["embedded_templates"] = []any{builtin}
	doc["template_lock"] = map[string]any{"S01@1.0.0": builtin["digest"]}
	raw, _ := json.Marshal(doc)
	if _, err := plan.Load(strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	builtin["name"] = "Forged"
	builtin["digest"], _ = semantic.TemplateDigest(builtin)
	doc["template_lock"] = map[string]any{"S01@1.0.0": builtin["digest"]}
	raw, _ = json.Marshal(doc)
	if _, err := plan.Load(strings.NewReader(string(raw))); err == nil {
		t.Fatal("altered builtin selected local implementation")
	}
}

func planWithTemplate(t *testing.T) map[string]any {
	t.Helper()
	doc, err := safeyaml.Parse(strings.NewReader(minimalPlan))
	if err != nil {
		t.Fatal(err)
	}
	template := map[string]any{
		"schema_version": "readyspec.template/v1", "template_id": "custom", "version": "1",
		"name": "Custom", "source": "PLAN_LOCAL", "domain": "R", "execution_mode": "STANDARD",
		"evidence_mode": "ACTIVE_PROBE", "params_schema": map[string]any{"type": "object"},
		"implementation": map[string]any{"kind": "CUSTOM_SHELL", "command": "true", "expect_exit_code": 0},
	}
	digest, err := semantic.TemplateDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	template["digest"] = digest
	doc["embedded_templates"] = []any{template}
	doc["template_lock"] = map[string]any{"custom@1": digest}
	doc["roles"].([]any)[0].(map[string]any)["items"] = []any{map[string]any{
		"item_id": "custom-check", "name": "Custom check", "template_id": "custom", "template_version": "1",
		"template_digest": digest, "importance": "REQUIRED", "enabled": true, "params": map[string]any{},
		"explicit_null_params": []any{}, "overridable_params": []any{},
	}}
	return doc
}

func TestValidatesSelfContainedTemplateReferences(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"missing snapshot", func(d map[string]any) { d["embedded_templates"] = []any{} }, false},
		{"missing lock", func(d map[string]any) { d["template_lock"] = map[string]any{} }, false},
		{"tampered snapshot", func(d map[string]any) { d["embedded_templates"].([]any)[0].(map[string]any)["name"] = "Tampered" }, false},
		{"duplicate snapshot", func(d map[string]any) {
			d["embedded_templates"] = append(d["embedded_templates"].([]any), d["embedded_templates"].([]any)[0])
		}, false},
		{"forged privilege", func(d map[string]any) {
			d["embedded_templates"].([]any)[0].(map[string]any)["execution_mode"] = "PRIVILEGED"
		}, false},
		{"unknown implementation", func(d map[string]any) {
			d["embedded_templates"].([]any)[0].(map[string]any)["implementation"] = map[string]any{"kind": "UNKNOWN"}
		}, false},
		{"conflicting explicit null", func(d map[string]any) {
			item := d["roles"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
			item["params"] = map[string]any{"path": "/data"}
			item["explicit_null_params"] = []any{"path"}
		}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := planWithTemplate(t)
			tt.mutate(doc)
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			_, err = plan.Load(strings.NewReader(string(raw)))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
		})
	}
}

func TestBuiltinLabelAndSelfSignedDigestDoNotGrantPrivilege(t *testing.T) {
	doc := planWithTemplate(t)
	template := doc["embedded_templates"].([]any)[0].(map[string]any)
	template["source"] = "BUILTIN"
	template["execution_mode"] = "PRIVILEGED"
	digest, err := semantic.TemplateDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	template["digest"] = digest
	doc["template_lock"] = map[string]any{"custom@1": digest}
	doc["roles"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["template_digest"] = digest
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Load(strings.NewReader(string(raw))); err == nil {
		t.Fatal("a self-declared builtin template acquired privilege without a local trusted registry match")
	}
}

func TestRejectsInvalidPlanContracts(t *testing.T) {
	for _, tt := range []struct{ name, input string }{
		{"legacy state", strings.Replace(minimalPlan, "state: DRAFT", "state: VALIDATED", 1)},
		{"legacy item field", strings.Replace(minimalPlan, "items: []", "instances: []", 1)},
		{"credential field", minimalPlan + "ssh_password: secret\n"},
		{"invalid topology", strings.Replace(minimalPlan, "max_nodes: 2", "max_nodes: 0", 1)},
		{"inverted topology", strings.Replace(minimalPlan, "min_nodes: 1", "min_nodes: 3", 1)},
		{"removed OS policy", strings.Replace(minimalPlan, "  allowed_arch:", "  allowed_os: [ubuntu]\n  allowed_arch:", 1)},
		{"forged digest", minimalPlan + "digest: sha256:" + strings.Repeat("0", 64) + "\n"},
		{"duplicate roles", strings.Replace(minimalPlan, "embedded_templates:", "  - {role_id: web, name: Duplicate, min_nodes: 1, max_nodes: 2, allow_colocation: false, items: []}\nembedded_templates:", 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := plan.Load(strings.NewReader(tt.input)); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestChecksTemplateParameterSchemaAndEffectiveValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any, map[string]any)
		valid  bool
	}{
		{"defaults satisfy required parameters", func(s, i map[string]any) {}, true},
		{"explicit value", func(s, i map[string]any) { i["params"] = map[string]any{"retries": 2, "mode": "safe"} }, true},
		{"wrong type", func(s, i map[string]any) { i["params"] = map[string]any{"retries": "2"} }, false},
		{"below minimum", func(s, i map[string]any) { i["params"] = map[string]any{"retries": 0} }, false},
		{"above maximum", func(s, i map[string]any) { i["params"] = map[string]any{"retries": 6} }, false},
		{"invalid enum", func(s, i map[string]any) { i["params"] = map[string]any{"mode": "unsafe"} }, false},
		{"missing required without default", func(s, i map[string]any) {
			delete(s["properties"].(map[string]any)["retries"].(map[string]any), "default")
		}, false},
		{"explicit null blocks default", func(s, i map[string]any) { i["explicit_null_params"] = []any{"retries"} }, false},
		{"nullable explicit null", func(s, i map[string]any) { i["explicit_null_params"] = []any{"optional"} }, true},
		{"unknown value", func(s, i map[string]any) { i["params"] = map[string]any{"unknown": 1} }, false},
		{"unknown null", func(s, i map[string]any) { i["explicit_null_params"] = []any{"unknown"} }, false},
		{"unknown override", func(s, i map[string]any) { i["overridable_params"] = []any{"unknown"} }, false},
		{"known override", func(s, i map[string]any) { i["overridable_params"] = []any{"retries"} }, true},
		{"invalid schema", func(s, i map[string]any) { s["properties"].(map[string]any)["retries"] = map[string]any{"type": 123} }, false},
		{"external reference", func(s, i map[string]any) {
			s["properties"].(map[string]any)["retries"] = map[string]any{"$ref": "https://invalid.example/private-schema"}
		}, false},
		{"local reference", func(s, i map[string]any) {
			s["$defs"] = map[string]any{"retry": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}}
			s["properties"].(map[string]any)["retries"] = map[string]any{"$ref": "#/$defs/retry", "default": 3}
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := planWithTemplate(t)
			template := doc["embedded_templates"].([]any)[0].(map[string]any)
			item := doc["roles"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
			schema := map[string]any{"type": "object", "required": []any{"retries"}, "properties": map[string]any{
				"retries":  map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "default": 3},
				"mode":     map[string]any{"type": "string", "enum": []any{"safe", "fast"}, "default": "safe"},
				"optional": map[string]any{"type": []any{"string", "null"}, "default": "inherited"},
			}}
			tt.change(schema, item)
			template["params_schema"] = schema
			digest, err := semantic.TemplateDigest(template)
			if err != nil {
				t.Fatal(err)
			}
			template["digest"] = digest
			doc["template_lock"] = map[string]any{"custom@1": digest}
			item["template_digest"] = digest
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			_, err = plan.Load(strings.NewReader(string(raw)))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "unsafe") {
				t.Fatal("parameter value leaked in validation error")
			}
		})
	}
}
