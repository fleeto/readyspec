package compiler

import (
	"encoding/json"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

// dnsProbe inspects the response header for the same query the Goss DNS
// resource performs. Goss only exposes "resolvable", which cannot distinguish
// NXDOMAIN from SERVFAIL/REFUSED or a truncated answer.
const dnsProbe = `import json, socket, struct, sys, time
config = json.loads(sys.argv[1])
start = time.monotonic()
result = {'rcode': -1, 'truncated': False, 'timed_out': False}
qtypes = {'A':1,'NS':2,'CNAME':5,'PTR':12,'MX':15,'TXT':16,'AAAA':28,'SRV':33}
try:
    name = config['name']
    if config['record_type'] == 'PTR':
        try:
            name = '.'.join(str(b) for b in reversed(socket.inet_pton(socket.AF_INET,name))) + '.in-addr.arpa'
        except OSError:
            hexstr = socket.inet_pton(socket.AF_INET6,name).hex()
            name = '.'.join(reversed(hexstr)) + '.ip6.arpa'
    qname = b''.join(bytes([len(part)]) + part.encode('ascii') for part in name.strip('.').split('.')) + b'\x00'
    packet = struct.pack('>HHHHHH', 0x1234, 0x0100, 1, 0, 0, 0) + qname + struct.pack('>HH', qtypes[config['record_type']], 1)
    host, port = config['server'].rsplit(':', 1)
    host = host.strip('[]')
    family = socket.AF_INET6 if ':' in host else socket.AF_INET
    sock = socket.socket(family, socket.SOCK_DGRAM)
    sock.settimeout(config['timeout_ms']/1000.0)
    sock.sendto(packet, (host, int(port)))
    data, _ = sock.recvfrom(4096)
    if len(data) < 12:
        raise ValueError('short response')
    flags = struct.unpack('>H', data[2:4])[0]
    result['rcode'] = flags & 0xf
    result['truncated'] = bool(flags & 0x0200)
except socket.timeout:
    result['timed_out'] = True
except (OSError, ValueError, KeyError) as error:
    result['error'] = type(error).__name__
result['duration_ms'] = (time.monotonic() - start) * 1000
print(json.dumps(result))
`

func dnsRcodeCommand(values map[string]plan.ResolvedParam) string {
	config := map[string]any{
		"name":        values["name"].Value,
		"record_type": values["record_type"].Value,
		"server":      values["server"].Value,
		"timeout_ms":  values["timeout_ms"].Value,
	}
	raw, _ := json.Marshal(config)
	return "python3 -c " + shellQuote(dnsProbe) + " " + shellQuote(string(raw))
}

func dnsRcodeEvaluation(text string) (string, map[string]any) {
	var observed struct {
		Rcode     *int    `json:"rcode"`
		Truncated bool    `json:"truncated"`
		TimedOut  bool    `json:"timed_out"`
		Error     string  `json:"error"`
		Duration  float64 `json:"duration_ms"`
	}
	summary := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &observed); err != nil || observed.Rcode == nil {
		summary["error"] = "INVALID_DNS_HEADER"
		return "ERROR", summary
	}
	summary["rcode"] = *observed.Rcode
	summary["truncated"] = observed.Truncated
	summary["duration_ms"] = observed.Duration
	if observed.Error != "" {
		summary["error"] = "DNS_QUERY_FAILED"
		return "ERROR", summary
	}
	if observed.TimedOut {
		summary["error"] = "DNS_QUERY_TIMED_OUT"
		return "ERROR", summary
	}
	if observed.Truncated {
		summary["error"] = "DNS_TRUNCATED_RESPONSE"
		return "ERROR", summary
	}
	// NOERROR and NXDOMAIN carry usable answers or a genuine negative result;
	// the Goss DNS resource decides the record set.
	if *observed.Rcode != 0 && *observed.Rcode != 3 {
		summary["error"] = "DNS_SERVER_FAILURE"
		return "ERROR", summary
	}
	return "PASS", summary
}
