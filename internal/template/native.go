package template

import "github.com/fleeto/readyspec/internal/semantic"

func addNativeTemplates(entries map[string]map[string]any) {
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	path := func() map[string]any { return map[string]any{"type": "string", "x-readyspec-type": "path"} }
	boolean := func() map[string]any { return map[string]any{"type": "boolean"} }
	for _, entry := range []struct {
		id, name, domain, evidence, kind string
		required                         []string
		properties                       map[string]any
	}{
		{"S02", "路径权限与属主", "S", "METADATA", "file_permissions", []string{"path"}, map[string]any{"path": path(), "mode": map[string]any{"type": "string", "pattern": "^0[0-7]{3}$"}, "owner": text(), "group": text()}},
		{"O03", "sysctl 内核参数", "O", "EFFECTIVE_STATE", "kernel_param", []string{"name", "value"}, map[string]any{"name": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.]+$"}, "value": text()}},
		{"R01", "系统服务状态", "R", "EFFECTIVE_STATE", "service", []string{"name", "running", "enabled"}, map[string]any{"name": text(), "running": boolean(), "enabled": boolean()}},
	} {
		doc := map[string]any{"schema_version": "readyspec.template/v1", "template_id": entry.id, "version": "1.0.0", "name": entry.name, "source": "BUILTIN", "domain": entry.domain, "execution_mode": "STANDARD", "evidence_mode": entry.evidence, "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": entry.required, "properties": entry.properties}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": entry.kind, "compiler_version": "1"}}
		digest, err := semantic.TemplateDigest(doc)
		if err != nil {
			panic(err)
		}
		doc["digest"] = digest
		entries[entry.id+"@1.0.0"] = doc
	}
}
