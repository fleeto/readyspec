package compiler

import (
	"encoding/json"
	"strings"
)

func commandMetadataCommand(command string) string {
	return "python3 -c " + shellQuote(commandMetadataProbe) + " " + shellQuote(command)
}

const commandMetadataProbe = `import errno, json, os, stat, sys
command = sys.argv[1]
explicit = '/' in command
candidates = [os.path.abspath(command)] if explicit else [os.path.abspath(os.path.join(directory or os.curdir,command)) for directory in os.environ.get('PATH',os.defpath).split(os.pathsep)]
first_regular = None
blocked = None
for candidate in candidates:
    try:
        info = os.stat(candidate)
    except OSError as error:
        if error.errno in (errno.ENOENT,errno.ENOTDIR):
            continue
        if error.errno == errno.EACCES:
            blocked = errno.errorcode.get(error.errno,'EACCES')
            continue
        print(json.dumps({'error':'PATH_LOOKUP_FAILED','errno':errno.errorcode.get(error.errno,'UNKNOWN')}))
        raise SystemExit(0)
    kind = 'directory' if stat.S_ISDIR(info.st_mode) else 'file' if stat.S_ISREG(info.st_mode) else 'other'
    regular = stat.S_ISREG(info.st_mode)
    executable = regular and os.access(candidate,os.X_OK)
    result = {'exists':True,'path':candidate,'filetype':kind,'executable':executable,'mode':format(stat.S_IMODE(info.st_mode),'04o')}
    if executable:
        print(json.dumps(result)); raise SystemExit(0)
    if first_regular is None and regular:
        first_regular = result
    if explicit:
        print(json.dumps(result)); raise SystemExit(0)
if blocked is not None:
    print(json.dumps({'error':'PATH_LOOKUP_FAILED','errno':blocked}))
elif first_regular is not None:
    print(json.dumps(first_regular))
else:
    result = {'exists':False,'executable':False}
    if explicit:
        result['path'] = candidates[0]
    print(json.dumps(result))
`

func commandMetadataEvaluation(params map[string]any, text string) (string, map[string]any) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil || raw == nil {
		return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
	}
	summary := map[string]any{"command": params["command"], "execution_required": params["executable"]}
	if reason, exists := raw["error"]; exists {
		summary["error"] = reason
		summary["errno"] = raw["errno"]
		return "ERROR", summary
	}
	exists, ok := raw["exists"].(bool)
	if !ok {
		return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
	}
	summary["exists"] = exists
	executable, execOK := raw["executable"].(bool)
	if !execOK || (!exists && executable) {
		return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
	}
	if !exists {
		summary["executable"] = false
		if path, ok := raw["path"].(string); ok && strings.HasPrefix(path, "/") {
			summary["path"] = path
		}
		return "FAIL", summary
	}
	path, pathOK := raw["path"].(string)
	kind, kindOK := raw["filetype"].(string)
	mode, modeOK := raw["mode"].(string)
	if !pathOK || !strings.HasPrefix(path, "/") || !kindOK || (kind != "file" && kind != "directory" && kind != "other") || !modeOK || len(mode) != 4 || !execOK {
		return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
	}
	for _, digit := range mode {
		if digit < '0' || digit > '7' {
			return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
		}
	}
	if kind != "file" && executable {
		return "ERROR", map[string]any{"error": "INVALID_COMMAND_METADATA"}
	}
	summary["path"] = path
	summary["filetype"] = kind
	summary["mode"] = mode
	summary["executable"] = executable
	if kind != "file" {
		return "FAIL", summary
	}
	if params["executable"] == true && !executable {
		return "FAIL", summary
	}
	return "PASS", summary
}
