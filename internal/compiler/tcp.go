package compiler

import (
	"encoding/json"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

const tcpProbe = `import json, socket, sys, time
config = json.loads(sys.argv[1])
start = time.monotonic()
result = {'reachable': False}
try:
    with socket.create_connection((config['host'], config['port']), timeout=config['timeout_ms']/1000.0):
        result['reachable'] = True
        result['reason'] = 'CONNECTED'
except socket.gaierror:
    result['reason'] = 'DNS_ERROR'
except ConnectionRefusedError:
    result['reason'] = 'REFUSED'
except (socket.timeout, TimeoutError):
    result['reason'] = 'TIMEOUT'
except OSError:
    result['reason'] = 'NETWORK_ERROR'
result['duration_ms'] = (time.monotonic() - start) * 1000
print(json.dumps(result))
`

func tcpCommand(values map[string]plan.ResolvedParam) string {
	address := values["address"].Value.(string)
	// The domain validator has already checked and normalized host:port.
	index := strings.LastIndex(address, ":")
	config := map[string]any{"host": strings.Trim(address[:index], "[]"), "port": json.Number(address[index+1:]), "timeout_ms": values["timeout_ms"].Value}
	raw, _ := json.Marshal(config)
	return "python3 -c " + shellQuote(tcpProbe) + " " + shellQuote(string(raw))
}

func tcpEvaluation(params map[string]any, text string) string {
	var observed struct {
		Reachable *bool    `json:"reachable"`
		Reason    string   `json:"reason"`
		Duration  *float64 `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(text), &observed); err != nil || observed.Reachable == nil || observed.Duration == nil || *observed.Duration < 0 {
		return "ERROR"
	}
	if observed.Reason != "CONNECTED" && observed.Reason != "REFUSED" {
		return "ERROR"
	}
	if (*observed.Reachable) != (observed.Reason == "CONNECTED") {
		return "ERROR"
	}
	if *observed.Reachable == params["reachable"] {
		return "PASS"
	}
	return "FAIL"
}
