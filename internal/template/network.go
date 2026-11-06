package template

import "github.com/fleeto/readyspec/internal/semantic"

func addNetworkTemplates(entries map[string]map[string]any) {
	str := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	list := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true}
	}
	for _, entry := range []struct {
		id, name, evidence string
		required           []string
		properties         map[string]any
	}{
		{"N02", "本地端口占用状态", "EFFECTIVE_STATE", []string{"protocol", "port", "listening"}, map[string]any{"protocol": map[string]any{"type": "string", "enum": []string{"tcp", "tcp6", "udp", "udp6"}}, "port": map[string]any{"type": "integer", "x-readyspec-type": "port", "minimum": 1, "maximum": 65535}, "listening": map[string]any{"type": "boolean"}, "ip": list()}},
		{"N03", "远端 TCP 连通性", "ACTIVE_PROBE", []string{"address", "reachable"}, map[string]any{"address": str(), "reachable": map[string]any{"type": "boolean"}, "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000, "default": 1000}}},
		{"N04", "DNS 域名解析", "ACTIVE_PROBE", []string{"name", "server", "record_type", "addrs"}, map[string]any{"name": str(), "server": str(), "record_type": map[string]any{"type": "string", "enum": []string{"A", "AAAA", "CNAME", "MX", "NS", "PTR", "TXT", "SRV"}}, "addrs": list(), "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000, "default": 1000}}},
		{"N05", "HTTP(S) 端点探测", "ACTIVE_PROBE", []string{"url", "status"}, map[string]any{"url": map[string]any{"type": "string", "pattern": "^https?://"}, "status": map[string]any{"type": "integer", "minimum": 100, "maximum": 599}, "method": map[string]any{"type": "string", "enum": []string{"GET", "HEAD"}, "default": "GET"}, "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000, "default": 1000}}},
	} {
		doc := map[string]any{"schema_version": "readyspec.template/v1", "template_id": entry.id, "version": "1.0.0", "name": entry.name, "source": "BUILTIN", "domain": "N", "execution_mode": "STANDARD", "evidence_mode": entry.evidence, "params_schema": map[string]any{"type": "object", "additionalProperties": false, "required": entry.required, "properties": entry.properties}, "implementation": map[string]any{"kind": "BUILTIN", "compiler": entry.id, "compiler_version": "1"}}
		digest, err := semantic.TemplateDigest(doc)
		if err != nil {
			panic(err)
		}
		doc["digest"] = digest
		entries[entry.id+"@1.0.0"] = doc
	}
}
