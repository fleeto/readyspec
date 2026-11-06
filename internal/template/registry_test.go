package template_test

import (
	"testing"

	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/template"
)

func TestBuiltinIdentityRequiresExactLocalContent(t *testing.T) {
	doc, ok := template.Lookup("S01", "1.0.0")
	if !ok || !template.Trusted(doc) {
		t.Fatal("S01 must be available as a trusted template")
	}
	doc["name"] = "Changed"
	doc["digest"], _ = semantic.TemplateDigest(doc)
	if template.Trusted(doc) {
		t.Fatal("re-signing altered content must not grant builtin trust")
	}
	original, _ := template.Lookup("S01", "1.0.0")
	if original["name"] == "Changed" {
		t.Fatal("caller modified registry content")
	}
}
