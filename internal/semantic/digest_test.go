package semantic_test

import (
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/semantic"
)

func TestCanonicalJSONUsesJCSNumberAndUTF16Ordering(t *testing.T) {
	got, err := semantic.CanonicalJSON(map[string]any{"\ue000": 1, "😀": 2, "numbers": []any{1e30, 4.50, 2e-3, 1e-27}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"numbers":[1e+30,4.5,0.002,1e-27],"😀":2,"` + "\ue000" + `":1}`
	if string(got) != want {
		t.Fatalf("canonical JSON = %s, want %s", got, want)
	}
}

func TestPlanDigestIgnoresMetadataAndTemplateOrderWithoutMutatingInput(t *testing.T) {
	first, err := safeyaml.Parse(strings.NewReader("name: demo\ndigest: old\nupdated_at: yesterday\nrelease_manifest: {}\nembedded_templates:\n- {template_id: z, version: '1'}\n- {template_id: a, version: '1'}\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := safeyaml.Parse(strings.NewReader("name: demo\nembedded_templates:\n- {version: '1', template_id: a}\n- {version: '1', template_id: z}\n"))
	a, err := semantic.PlanDigest(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := semantic.PlanDigest(second)
	if err != nil || a != b {
		t.Fatalf("equivalent plans have different digests: %s, %s, %v", a, b, err)
	}
	if first["embedded_templates"].([]any)[0].(map[string]any)["template_id"] != "z" {
		t.Fatal("digest calculation mutated the input")
	}
	second["name"] = "changed"
	c, _ := semantic.PlanDigest(second)
	if c == a {
		t.Fatal("semantic change did not change the digest")
	}
}
