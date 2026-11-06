//go:build linux

package compiler_test

import (
	"testing"
)

func TestConfigCollectorHandlesJSONPointerEscapesAndArrays(t *testing.T) {
	for _, tt := range []struct {
		name, format, content, key string
		expected                   any
		pattern                    string
		status                     string
	}{
		{"escaped_slash", "json", `{"a/b":{"m~n":"value"}}`, "/a~1b/m~0n", "value", "", "PASS"},
		{"escaped_tilde_value_expected", "json", `{"m~n":"x/y"}`, "/m~0n", "x/y", "", "PASS"},
		{"array_index", "json", `{"apps":[{"name":"first"},{"name":"second"}]}`, "/apps/1/name", "second", "", "PASS"},
		{"array_missing_index", "json", `{"apps":[{"name":"first"}]}`, "/apps/1/name", nil, "", "FAIL"},
		{"array_leading_zero", "json", `{"apps":[{"name":"first"},{"name":"second"}]}`, "/apps/01/name", "second", "", "ERROR"},
		{"array_dash_index", "json", `{"apps":[1]}`, "/apps/-", nil, "", "ERROR"},
		{"invalid_escape", "json", `{"a":"b"}`, "/a~2b", nil, "", "ERROR"},
		{"trailing_tilde", "json", `{"a":"b"}`, "/a~", nil, "", "ERROR"},
		{"numeric_expected", "json", `{"count":3}`, "/count", float64(3), "", "PASS"},
		{"boolean_expected", "yaml", "enabled: true\n", "/enabled", true, "", "PASS"},
		{"yaml_array_escaped", "yaml", "servers:\n  - name: a/b\n", "/servers/0/name", "a/b", "", "PASS"},
		{"map_key_with_leading_zero", "json", `{"01":"literal"}`, "/01", "literal", "", "PASS"},
		{"numeric_value_vs_string_expected", "json", `{"count":3}`, "/count", "3", "", "FAIL"},
		{"duplicate_json_key", "json", `{"app":{"key":"a","key":"b"}}`, "/app/key", "a", "", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "config", tt.content)
			params := map[string]any{"path": "/config", "format": tt.format, "key": tt.key}
			if tt.pattern != "" {
				params["regex"] = tt.pattern
			} else {
				params["expected"] = tt.expected
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "D05", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if results[0].EvidenceMode != "CONFIG" {
				t.Fatal("configuration evidence must stay CONFIG")
			}
		})
	}
}
