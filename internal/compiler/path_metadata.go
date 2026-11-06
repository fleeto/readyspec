package compiler

import "encoding/json"

// Keep link text separate from the selected node; shell substitution would
// corrupt newline-bearing targets. Metadata adds no target-access assertion.
const pathMetadataProbe = `import errno, json, os, stat, sys
path = sys.argv[1]
follow = sys.argv[2] == 'true'
result = {'follow_symlinks':follow}
def kind(mode):
    return 'symlink' if stat.S_ISLNK(mode) else 'directory' if stat.S_ISDIR(mode) else 'file' if stat.S_ISREG(mode) else 'other'
try:
    node = os.lstat(path)
    result.update(node_exists=True,node_type=kind(node.st_mode))
    if stat.S_ISLNK(node.st_mode):
        result['link_target'] = os.readlink(path)
        try:
            target = os.stat(path)
            result.update(target_exists=True,target_type=kind(target.st_mode))
        except OSError as error:
            if error.errno in (errno.ENOENT,errno.ENOTDIR):
                result['target_exists'] = False
            else:
                result['target_error'] = errno.errorcode.get(error.errno,'UNKNOWN')
                if follow:
                    result['error'] = 'TARGET_UNAVAILABLE'
except OSError as error:
    if error.errno in (errno.ENOENT,errno.ENOTDIR):
        result['node_exists'] = False
    else:
        result.update(error='PATH_UNAVAILABLE',errno=errno.errorcode.get(error.errno,'UNKNOWN'))
print(json.dumps(result))
`

func pathMetadataEvaluation(text string) (string, map[string]any) {
	var summary map[string]any
	if err := json.Unmarshal([]byte(text), &summary); err != nil || summary == nil {
		return "ERROR", map[string]any{"error": "INVALID_PATH_METADATA"}
	}
	if _, failed := summary["error"]; failed {
		return "ERROR", summary
	}
	exists, ok := summary["node_exists"].(bool)
	if !ok {
		return "ERROR", summary
	}
	follow, ok := summary["follow_symlinks"].(bool)
	if !ok {
		return "ERROR", summary
	}
	if exists {
		kind, ok := summary["node_type"].(string)
		if !ok || (kind != "file" && kind != "directory" && kind != "symlink" && kind != "other") {
			return "ERROR", summary
		}
		if kind == "symlink" {
			if target, ok := summary["link_target"].(string); !ok || target == "" {
				return "ERROR", summary
			}
			if _, failed := summary["target_error"]; failed {
				if follow {
					return "ERROR", summary
				}
			} else {
				targetExists, ok := summary["target_exists"].(bool)
				if !ok {
					return "ERROR", summary
				}
				if targetExists {
					targetKind, ok := summary["target_type"].(string)
					if !ok || (targetKind != "file" && targetKind != "directory" && targetKind != "other") {
						return "ERROR", summary
					}
				}
			}
		}
	}
	return "PASS", summary
}
