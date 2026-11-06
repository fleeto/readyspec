// Package template owns immutable definitions of locally trusted checks.
package template

import (
	"encoding/json"
	"sort"

	"github.com/fleeto/readyspec/internal/semantic"
)

var registry = buildRegistry()

func buildRegistry() map[string]map[string]any {
	doc := map[string]any{
		"schema_version": "readyspec.template/v1", "template_id": "S01", "version": "1.0.0",
		"name": "路径存在与类型", "source": "BUILTIN", "domain": "S",
		"execution_mode": "STANDARD", "evidence_mode": "EXISTENCE",
		"params_schema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"path"},
			"properties": map[string]any{
				"path":     map[string]any{"type": "string", "minLength": 1, "pattern": "^/", "x-readyspec-type": "path", "title": "目标路径"},
				"exists":   map[string]any{"type": "boolean", "default": true, "title": "要求存在"},
				"filetype": map[string]any{"type": "string", "enum": []string{"file", "directory", "symlink"}, "title": "路径类型"},
			},
		},
		"implementation": map[string]any{"kind": "BUILTIN", "compiler": "path", "compiler_version": "1"},
	}
	digest, err := semantic.TemplateDigest(doc)
	if err != nil {
		panic(err)
	}
	doc["digest"] = digest
	entries := map[string]map[string]any{"S01@1.0.0": doc}
	for _, entry := range []struct {
		id, name, domain, evidence, param, kind string
		required                                []string
		properties                              map[string]any
	}{
		{"H02", "逻辑 CPU 数", "H", "METADATA", "min_cores", "cpu", []string{"min_cores"}, map[string]any{"min_cores": map[string]any{"type": "integer", "minimum": 1}}},
		{"H03", "主机总内存", "H", "METADATA", "min_memory", "memory", []string{"min_memory"}, map[string]any{"min_memory": map[string]any{"type": "string", "x-readyspec-type": "capacity"}}},
		{"S04", "目录可用空间", "S", "METADATA", "min_available", "disk", []string{"path", "min_available"}, map[string]any{"path": map[string]any{"type": "string", "x-readyspec-type": "path"}, "min_available": map[string]any{"type": "string", "x-readyspec-type": "capacity"}}},
	} {
		t := map[string]any{"schema_version": "readyspec.template/v1", "template_id": entry.id, "version": "1.0.0", "name": entry.name, "source": "BUILTIN", "domain": entry.domain, "execution_mode": "STANDARD", "evidence_mode": entry.evidence, "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": entry.required, "properties": entry.properties}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": entry.kind, "compiler_version": "1"}}
		digest, err := semantic.TemplateDigest(t)
		if err != nil {
			panic(err)
		}
		t["digest"] = digest
		entries[entry.id+"@1.0.0"] = t
	}
	addNativeTemplates(entries)
	addCollectorTemplates(entries)
	addNetworkTemplates(entries)
	addPrivilegedTemplates(entries)
	addAdvancedTemplates(entries)
	addRemainingTemplates(entries)
	addLogTemplates(entries)
	unitVariant := map[string]any{"schema_version": "readyspec.template/v1", "template_id": "R04", "version": "1.0.0-config", "name": "服务资源限制（配置）", "source": "BUILTIN", "domain": "R", "execution_mode": "STANDARD", "evidence_mode": "CONFIG", "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"unit", "limit", "minimum"}, "properties": map[string]any{"unit": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.@-]+$`}, "limit": map[string]any{"type": "string", "enum": []string{"NOFILE", "NPROC"}}, "minimum": map[string]any{"type": "integer", "minimum": 0}}}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": "R04", "compiler_version": "1"}}
	entries["R04@1.0.0-config"] = unitVariant
	for _, doc := range entries {
		schema := doc["params_schema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		switch doc["template_id"] {
		case "A01":
			properties["name"].(map[string]any)["pattern"] = `^[^\x00\r\n:]+$`
			schema["allOf"] = []any{
				map[string]any{
					"if": map[string]any{"properties": map[string]any{"exists": map[string]any{"const": false}}},
					"then": map[string]any{"not": map[string]any{"anyOf": []any{
						map[string]any{"required": []string{"uid"}},
						map[string]any{"required": []string{"gid"}},
						map[string]any{"required": []string{"shell"}},
					}}},
				},
			}
		case "A02":
			properties["name"].(map[string]any)["pattern"] = `^[^\x00\r\n:]+$`
			properties["members"].(map[string]any)["items"].(map[string]any)["pattern"] = `^[^\x00\r\n:]+$`
		case "S01":
			properties["follow_symlinks"] = map[string]any{"type": "boolean", "default": false}
			schema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"exists": map[string]any{"const": false}}}, "then": map[string]any{"not": map[string]any{"required": []string{"filetype"}}}}, map[string]any{"if": map[string]any{"properties": map[string]any{"follow_symlinks": map[string]any{"const": true}}}, "then": map[string]any{"not": map[string]any{"required": []string{"filetype"}, "properties": map[string]any{"filetype": map[string]any{"const": "symlink"}}}}}}
		case "H04":
			delete(properties["min_swap"].(map[string]any), "default")
			properties["min_swap"].(map[string]any)["x-readyspec-minimum"] = 1
			schema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"mode": map[string]any{"const": "minimum"}}}, "then": map[string]any{"required": []string{"min_swap"}}}}
		case "S02":
			schema["anyOf"] = []any{map[string]any{"required": []string{"mode"}}, map[string]any{"required": []string{"owner"}}, map[string]any{"required": []string{"group"}}}
		case "S05":
			schema["required"] = []string{"path"}
			properties["min_percent"] = map[string]any{"type": "number", "minimum": 0, "maximum": 100}
			schema["oneOf"] = []any{map[string]any{"required": []string{"min_available"}, "not": map[string]any{"required": []string{"min_percent"}}}, map[string]any{"required": []string{"min_percent"}, "not": map[string]any{"required": []string{"min_available"}}}}
		case "O03":
			schema["required"] = []string{"name"}
			properties["value"] = map[string]any{"type": []string{"string", "number"}}
			properties["minimum"] = map[string]any{"type": "number"}
			properties["maximum"] = map[string]any{"type": "number"}
			schema["oneOf"] = []any{map[string]any{"required": []string{"value"}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"minimum"}}, map[string]any{"required": []string{"maximum"}}}}}, map[string]any{"anyOf": []any{map[string]any{"required": []string{"minimum"}}, map[string]any{"required": []string{"maximum"}}}, "not": map[string]any{"required": []string{"value"}}}}
			schema["x-readyspec-order"] = []any{[]any{"minimum", "maximum"}}
		case "D05":
			schema["required"] = []string{"path", "format", "key"}
			properties["regex"] = map[string]any{"type": "string", "x-readyspec-type": "regex"}
			schema["oneOf"] = []any{map[string]any{"required": []string{"expected"}, "not": map[string]any{"required": []string{"regex"}}}, map[string]any{"required": []string{"regex"}, "not": map[string]any{"required": []string{"expected"}}}}
		case "R04":
			properties["bound"] = map[string]any{"type": "string", "enum": []string{"soft", "hard"}, "default": "soft"}
		case "N02":
			properties["ip"].(map[string]any)["items"].(map[string]any)["x-readyspec-type"] = "ip"
			properties["ip"].(map[string]any)["minItems"] = 1
		case "A04":
			schema["oneOf"] = []any{map[string]any{"properties": map[string]any{"framework": map[string]any{"const": "selinux"}, "mode": map[string]any{"enum": []string{"enforcing", "permissive", "disabled"}}}}, map[string]any{"properties": map[string]any{"framework": map[string]any{"const": "apparmor"}, "mode": map[string]any{"enum": []string{"enabled", "disabled", "enforcing", "permissive"}}}}}
		case "N03":
			properties["address"].(map[string]any)["x-readyspec-type"] = "tcp_address"
		case "N04":
			properties["server"].(map[string]any)["x-readyspec-type"] = "dns_server"
		case "N05":
			properties["url"].(map[string]any)["x-readyspec-type"] = "http_url"
			properties["verify_tls"] = map[string]any{"type": "boolean", "default": true}
			properties["follow_redirects"] = map[string]any{"type": "boolean", "default": false}
			properties["body_contains"] = map[string]any{"type": "string"}
			properties["body_regex"] = map[string]any{"type": "string", "x-readyspec-type": "regex"}
			properties["headers"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
		case "O01", "O02", "D03":
			schema["x-readyspec-order"] = []any{[]any{"minimum_version", "maximum_version"}}
			schema["x-readyspec-order-kind"] = "numeric_version"
		case "R02":
			schema["x-readyspec-order"] = []any{[]any{"minimum_count", "maximum_count"}}
		case "L01":
			schema["x-readyspec-order"] = []any{[]any{"minimum_seconds", "maximum_seconds"}}
		}
		implementation := doc["implementation"].(map[string]any)
		implementation["requires"] = requirements(doc["template_id"].(string))
		if doc["template_id"] == "R04" && doc["evidence_mode"] == "CONFIG" {
			implementation["requires"] = []string{"os:linux", "tool:systemctl"}
		}
		digest, err := semantic.TemplateDigest(doc)
		if err != nil {
			panic(err)
		}
		doc["digest"] = digest
	}
	return entries
}

func requirements(id string) []string {
	result := []string{"os:linux"}
	var tools []string
	switch id {
	case "H01", "O02":
		tools = []string{"uname"}
	case "H02":
		tools = []string{"getconf"}
	case "H03":
		tools = []string{"awk"}
		result = append(result, "fs:proc")
	case "H04", "R03", "L01":
		tools = []string{"cat"}
		result = append(result, "fs:proc")
	case "S04", "S05":
		tools = []string{"stat", "cat"}
		result = append(result, "fs:proc")
	case "N01", "N06":
		tools = []string{"ip"}
	case "N02":
		tools = []string{"ss"}
	case "N03", "N04":
		tools = []string{"python3"}
	case "N05":
		tools = []string{"python3"}
	case "S01":
		tools = []string{"python3"}
	case "R02":
		tools = []string{"ps"}
	case "R04":
		tools = []string{"cat"}
		result = append(result, "fs:proc")
	case "R01":
		tools = []string{"systemctl"}
	case "R05":
		tools = []string{"systemctl", "timedatectl"}
	case "A01", "A02":
		tools = []string{"python3"}
		result = append(result, "libc:glibc-nss")
	case "A03":
		tools = []string{"sshd"}
	case "A04":
		result = append(result, "fs:sys")
	case "D01":
		result = append(result, "package-manager")
	case "D02":
		tools = []string{"python3"}
	case "D03":
		tools = []string{"stat"}
		result = append(result, "runtime-query")
	case "D04":
		tools = []string{"readelf", "ldconfig", "python3"}
		result = append(result, "elf-library-cache")
	case "O01", "D05":
		tools = []string{"cat"}
	case "O03":
		tools = []string{"cat", "stat"}
		result = append(result, "fs:proc")
	case "O04":
		tools = []string{"cat"}
		result = append(result, "fs:proc")
	case "L02", "L03":
		tools = []string{"journalctl", "date", "awk", "find"}
		result = append(result, "fs:proc", "journal:system")
	}
	for _, tool := range tools {
		result = append(result, "tool:"+tool)
	}
	return result
}

// Lookup returns an independent snapshot; callers cannot mutate local trust.
func Lookup(id, version string) (map[string]any, bool) {
	doc, ok := registry[id+"@"+version]
	if !ok {
		return nil, false
	}
	raw, _ := json.Marshal(doc)
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	return copy, true
}

// Trusted compares calculated content as well as its declared digest.
func Trusted(doc map[string]any) bool {
	id, _ := doc["template_id"].(string)
	version, _ := doc["version"].(string)
	local, ok := registry[id+"@"+version]
	if !ok {
		return false
	}
	digest, err := semantic.TemplateDigest(doc)
	return err == nil && digest == local["digest"] && doc["digest"] == digest
}

func List() []map[string]any {
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		doc := registry[key]
		copy, _ := Lookup(doc["template_id"].(string), doc["version"].(string))
		result = append(result, copy)
	}
	return result
}
