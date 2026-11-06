package template

import "github.com/fleeto/readyspec/internal/semantic"

func addPrivilegedTemplates(entries map[string]map[string]any) {
	doc := map[string]any{
		"schema_version": "readyspec.template/v1", "template_id": "A03", "version": "1.0.0", "name": "SSH 服务端生效配置", "source": "BUILTIN", "domain": "A", "execution_mode": "PRIVILEGED", "evidence_mode": "EFFECTIVE_STATE",
		"params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"key", "expected"}, "properties": map[string]any{
			"key": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9]+$"}, "expected": map[string]any{"type": "string"}, "match_context": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.:@=/,-]+$"},
		}},
		"implementation": map[string]any{"kind": "BUILTIN", "compiler": "sshd_effective", "compiler_version": "1"},
	}
	digest, err := semantic.TemplateDigest(doc)
	if err != nil {
		panic(err)
	}
	doc["digest"] = digest
	entries["A03@1.0.0"] = doc
}
