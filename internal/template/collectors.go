package template

import "github.com/fleeto/readyspec/internal/semantic"

func addCollectorTemplates(entries map[string]map[string]any) {
	integer := func(min int) map[string]any { return map[string]any{"type": "integer", "minimum": min} }
	for _, entry := range []struct {
		id, name, domain, evidence string
		required                   []string
		properties                 map[string]any
	}{
		{"H01", "CPU 架构", "H", "METADATA", []string{"allowed_arch"}, map[string]any{"allowed_arch": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"enum": []string{"x86_64", "aarch64"}}}}},
		{"H04", "Swap 策略", "H", "EFFECTIVE_STATE", []string{"mode"}, map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"disabled", "enabled", "minimum"}}, "min_swap": map[string]any{"type": "string", "x-readyspec-type": "capacity", "default": "0 B"}}},
		{"O04", "内核模块状态", "O", "EFFECTIVE_STATE", []string{"name", "loaded"}, map[string]any{"name": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]+$"}, "loaded": map[string]any{"type": "boolean"}}},
		{"S05", "目录可用 inode", "S", "METADATA", []string{"path", "min_available"}, map[string]any{"path": map[string]any{"type": "string", "x-readyspec-type": "path"}, "min_available": integer(0)}},
		{"R03", "系统平均负载", "R", "METADATA", []string{"window", "maximum"}, map[string]any{"window": map[string]any{"type": "integer", "enum": []int{1, 5, 15}}, "maximum": map[string]any{"type": "number", "minimum": 0}}},
		{"L01", "系统运行时长", "L", "METADATA", []string{"minimum_seconds"}, map[string]any{"minimum_seconds": integer(0), "maximum_seconds": integer(0)}},
	} {
		doc := map[string]any{"schema_version": "readyspec.template/v1", "template_id": entry.id, "version": "1.0.0", "name": entry.name, "source": "BUILTIN", "domain": entry.domain, "execution_mode": "STANDARD", "evidence_mode": entry.evidence, "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": entry.required, "properties": entry.properties}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": entry.id, "compiler_version": "1"}}
		digest, err := semantic.TemplateDigest(doc)
		if err != nil {
			panic(err)
		}
		doc["digest"] = digest
		entries[entry.id+"@1.0.0"] = doc
	}
}
