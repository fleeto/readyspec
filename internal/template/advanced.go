package template

import "github.com/fleeto/readyspec/internal/semantic"

func addAdvancedTemplates(entries map[string]map[string]any) {
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	version := func() map[string]any { return map[string]any{"type": "string", "pattern": `^[0-9]+(\.[0-9]+){0,3}$`} }
	count := func() map[string]any { return map[string]any{"type": "integer", "minimum": 0} }
	for _, entry := range []struct {
		id, name, domain, evidence string
		required                   []string
		properties                 map[string]any
	}{
		{"O02", "内核版本", "O", "METADATA", []string{"minimum_version"}, map[string]any{"minimum_version": version(), "maximum_version": version()}},
		{"R02", "进程存在与数量", "R", "EFFECTIVE_STATE", []string{"name", "minimum_count"}, map[string]any{"name": text(), "minimum_count": count(), "maximum_count": count()}},
		{"R04", "进程或服务资源限制", "R", "EFFECTIVE_STATE", []string{"pid", "limit", "minimum"}, map[string]any{"pid": map[string]any{"type": "integer", "minimum": 1}, "limit": map[string]any{"type": "string", "enum": []string{"NOFILE", "NPROC"}}, "minimum": count()}},
		{"D02", "可执行命令检查", "D", "EXISTENCE", []string{"command"}, map[string]any{"command": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_./+-]+$`}, "executable": map[string]any{"type": "boolean", "default": true}}},
		{"D03", "语言运行时版本", "D", "METADATA", []string{"runtime", "minimum_version"}, map[string]any{"runtime": map[string]any{"type": "string", "enum": []string{"python", "node", "java"}}, "path": map[string]any{"type": "string", "x-readyspec-type": "path"}, "minimum_version": version(), "maximum_version": version()}},
		{"N06", "目标路由决策", "N", "EFFECTIVE_STATE", []string{"target", "interface"}, map[string]any{"target": map[string]any{"type": "string", "x-readyspec-type": "ip"}, "interface": text(), "gateway": map[string]any{"type": "string", "x-readyspec-type": "ip"}, "source": map[string]any{"type": "string", "x-readyspec-type": "ip"}}},
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
