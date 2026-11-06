package template

import "github.com/fleeto/readyspec/internal/semantic"

func addRemainingTemplates(entries map[string]map[string]any) {
	str := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	list := func() map[string]any { return map[string]any{"type": "array", "uniqueItems": true, "items": str()} }
	path := func() map[string]any { return map[string]any{"type": "string", "x-readyspec-type": "path"} }
	for _, entry := range []struct {
		id, name, domain, evidence string
		required                   []string
		properties                 map[string]any
	}{
		{"O01", "发行版与版本", "O", "METADATA", []string{"allowed_ids"}, map[string]any{"allowed_ids": list(), "minimum_version": map[string]any{"type": "string", "pattern": `^[0-9]+(\.[0-9]+){0,3}$`}, "maximum_version": map[string]any{"type": "string", "pattern": `^[0-9]+(\.[0-9]+){0,3}$`}}},
		{"S03", "挂载点属性", "S", "EFFECTIVE_STATE", []string{"path"}, map[string]any{"path": path(), "filesystem": str(), "required_options": list(), "forbidden_options": list()}},
		{"N01", "网络接口与地址", "N", "EFFECTIVE_STATE", []string{"name", "up", "addresses"}, map[string]any{"name": str(), "up": map[string]any{"type": "boolean"}, "addresses": map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string", "x-readyspec-type": "ip_or_cidr"}}}},
		{"R05", "时间同步状态", "R", "EFFECTIVE_STATE", []string{"allowed_services"}, map[string]any{"synchronized": map[string]any{"type": "boolean", "default": true}, "allowed_services": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"enum": []string{"chronyd", "chrony", "systemd-timesyncd", "ntpd", "ntp"}}}}},
		{"A01", "系统用户属性", "A", "METADATA", []string{"name", "exists"}, map[string]any{"name": str(), "exists": map[string]any{"type": "boolean"}, "uid": map[string]any{"type": "integer", "minimum": 0}, "gid": map[string]any{"type": "integer", "minimum": 0}, "shell": path()}},
		{"A02", "用户组与成员", "A", "METADATA", []string{"name", "members"}, map[string]any{"name": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.-]+$`}, "gid": map[string]any{"type": "integer", "minimum": 0}, "members": list()}},
		{"A04", "SELinux / AppArmor 状态", "A", "EFFECTIVE_STATE", []string{"framework", "mode"}, map[string]any{"framework": map[string]any{"type": "string", "enum": []string{"selinux", "apparmor"}}, "mode": map[string]any{"type": "string", "enum": []string{"enforcing", "permissive", "enabled", "disabled"}}}},
		{"D01", "系统软件包", "D", "METADATA", []string{"name", "installed", "manager"}, map[string]any{"name": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_.+:-]+$`}, "installed": map[string]any{"type": "boolean"}, "manager": map[string]any{"type": "string", "enum": []string{"dpkg", "rpm"}}, "minimum_version": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9.:+~^_-]+$`}, "maximum_version": map[string]any{"type": "string", "pattern": `^[A-Za-z0-9.:+~^_-]+$`}}},
		{"D04", "静态二进制依赖（ELF）", "D", "STATIC_BINARY", []string{"path", "architecture", "required_libraries"}, map[string]any{"path": path(), "architecture": map[string]any{"type": "string", "enum": []string{"x86_64", "aarch64"}}, "required_libraries": list()}},
		{"D05", "环境与配置文件键值", "D", "CONFIG", []string{"path", "format", "key", "expected"}, map[string]any{"path": path(), "format": map[string]any{"type": "string", "enum": []string{"env", "ini", "json", "yaml"}}, "key": str(), "expected": map[string]any{}}},
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
