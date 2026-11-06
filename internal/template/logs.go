package template

import "github.com/fleeto/readyspec/internal/semantic"

func addLogTemplates(entries map[string]map[string]any) {
	for _, id := range []string{"L02", "L03"} {
		properties := map[string]any{"lookback_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 2592000}, "allow_since_boot": map[string]any{"type": "boolean", "default": false}}
		required := []string{"lookback_seconds"}
		name := "内核 OOM 与致命事件历史"
		if id == "L02" {
			properties["max_events"] = map[string]any{"type": "integer", "minimum": 0, "default": 0}
		} else {
			name = "系统服务异常重启历史"
			properties["unit"] = map[string]any{"type": "string", "minLength": 1}
			required = append(required, "unit")
			properties["max_failures"] = map[string]any{"type": "integer", "minimum": 0, "default": 0}
			properties["max_restarts"] = map[string]any{"type": "integer", "minimum": 0, "default": 0}
		}
		doc := map[string]any{"schema_version": "readyspec.template/v1", "template_id": id, "version": "1.0.0", "name": name, "source": "BUILTIN", "domain": "L", "execution_mode": "STANDARD", "evidence_mode": "LOG_HISTORY", "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": id, "compiler_version": "1"}}
		digest, err := semantic.TemplateDigest(doc)
		if err != nil {
			panic(err)
		}
		doc["digest"] = digest
		entries[id+"@1.0.0"] = doc
	}
}
