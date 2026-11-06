package safeyaml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/safeyaml"
)

func TestRejectsDuplicateKeys(t *testing.T) {
	_, err := safeyaml.Parse(strings.NewReader("role:\n  name: web\n  name: db\n"))
	if err == nil || !strings.Contains(err.Error(), "YAML_DUPLICATE_KEY") {
		t.Fatalf("expected duplicate-key rejection, got %v", err)
	}
}

func TestRejectsUnsafeDocuments(t *testing.T) {
	for _, tt := range []struct{ name, input, code string }{
		{"multiple documents", "a: 1\n---\nb: 2\n", "YAML_MULTIPLE_DOCUMENTS"},
		{"custom tag", "a: !command echo\n", "YAML_TAG"},
		{"tagged mapping", "a: !command {b: c}\n", "YAML_TAG"},
		{"recursive alias", "a: &a [*a]\n", "YAML_CYCLE"},
		{"nonfinite number", "a: .nan\n", "YAML_NUMBER"},
		{"unsafe integer", "a: 9007199254740993\n", "YAML_NUMBER"},
		{"invalid utf8", "a: \xff\n", "YAML_INVALID_UTF8"},
		{"empty document", "", "YAML_EMPTY"},
		{"non-string key", "1: a\n", "YAML_KEY_TYPE"},
		{"deep document", "a: " + strings.Repeat("[", 100) + "0" + strings.Repeat("]", 100), "YAML_DEPTH_LIMIT"},
		{"alias limit", "a: &a [1]\nb: [" + strings.Repeat("*a,", 101) + "]", "YAML_ALIAS_LIMIT"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := safeyaml.Parse(strings.NewReader(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.code) {
				t.Fatalf("expected %s, got %v", tt.code, err)
			}
		})
	}
}

func TestBoundsInputBeforeDecoding(t *testing.T) {
	_, err := safeyaml.Parse(bytes.NewReader(bytes.Repeat([]byte(" "), 10*1024*1024+1)))
	if err == nil || !strings.Contains(err.Error(), "YAML_SIZE_EXCEEDED") {
		t.Fatalf("expected input size rejection, got %v", err)
	}
}

func TestBoundsExpandedAliasBytes(t *testing.T) {
	input := "a: &a '" + strings.Repeat("x", 1024*1024) + "'\nb: [" + strings.Repeat("*a,", 16) + "]\n"
	_, err := safeyaml.Parse(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "YAML_EXPANSION_LIMIT") {
		t.Fatalf("expected alias byte expansion rejection, got %v", err)
	}
}

func TestParsesJSONCompatibleValuesAndBoundedAliases(t *testing.T) {
	doc, err := safeyaml.Parse(strings.NewReader("a: &a [1, true, null, 文本]\nb: *a\nupdated_at: 2026-11-04\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc["updated_at"] != "2026-11-04" || len(doc["b"].([]any)) != 4 {
		t.Fatalf("unexpected parsed document: %#v", doc)
	}
}
