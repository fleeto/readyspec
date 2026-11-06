package compiler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

// Goss v0.4.9 has no bound for HTTP body readers. A controlled Python standard
// library probe supplies bounded observations to a Goss command resource.
// The extra byte is only an overflow sentinel and is never accepted as evidence.
const boundedHTTPProbe = `import base64, http.client, json, socket, ssl, sys, urllib.error, urllib.parse, urllib.request
config = json.loads(sys.argv[1])
limit = 1048576
header_limit = 65536
header_bytes = 0
redirects = 0
class ProbeError(Exception):
    pass
class HeaderReader:
    def __init__(self, source):
        self.source = source
    def __getattr__(self, name):
        return getattr(self.source, name)
    def readline(self, size=-1):
        global header_bytes
        remaining = header_limit - header_bytes
        line = self.source.readline(min(remaining + 1, size) if size >= 0 else remaining + 1)
        header_bytes += len(line)
        if header_bytes > header_limit:
            raise ProbeError('HEADERS_TOO_LARGE')
        return line
class BoundedResponse(http.client.HTTPResponse):
    def begin(self):
        original = self.fp
        self.fp = HeaderReader(original)
        try:
            super().begin()
        finally:
            if self.fp is not None:
                self.fp = original
http.client.HTTPConnection.response_class = BoundedResponse
http.client.HTTPSConnection.response_class = BoundedResponse
class RedirectPolicy(urllib.request.HTTPRedirectHandler):
    def http_error_302(self, req, fp, code, msg, headers):
        global redirects
        if not config['follow_redirects']:
            return None
        location = headers.get('Location') or headers.get('URI')
        if not location:
            return None
        fp.close()
        if redirects >= 10:
            raise ProbeError('TOO_MANY_REDIRECTS')
        target = urllib.parse.urljoin(req.full_url, location)
        if urllib.parse.urlsplit(target).scheme not in ('http', 'https'):
            raise ProbeError('INVALID_REDIRECT')
        redirects += 1
        # Close intermediate bodies rather than allowing urllib to drain an
        # unbounded redirect response. GET and HEAD keep their declared method.
        return self.parent.open(urllib.request.Request(target, method=config['method']), timeout=req.timeout)
    http_error_301 = http_error_303 = http_error_307 = http_error_308 = http_error_302
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
context = ssl.create_default_context() if config['verify_tls'] else ssl._create_unverified_context()
handlers = [urllib.request.HTTPSHandler(context=context), RedirectPolicy()]
opener = urllib.request.build_opener(*handlers)
try:
    request = urllib.request.Request(config['url'], method=config['method'])
    try:
        response = opener.open(request, timeout=config['timeout_ms'] / 1000.0)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        length = response.headers.get('Content-Length')
        remaining = limit - header_bytes
        if config['collect_body'] and config['method'] != 'HEAD' and length is not None and int(length) > remaining:
            raise OverflowError()
        body = response.read(remaining + 1) if config['collect_body'] else b''
        if len(body) + header_bytes > limit:
            raise OverflowError()
        if config['collect_body'] and config['method'] != 'HEAD' and length is not None and len(body) != int(length):
            raise ValueError('INCOMPLETE_BODY')
        headers = {}
        for name, value in response.headers.items():
            headers.setdefault(name.lower(), []).append(value)
        print(json.dumps({'status': response.code, 'headers': headers,
            'body_base64': base64.b64encode(body).decode('ascii'),
            'header_bytes': header_bytes, 'redirects': redirects,
            'tls_verified': config['verify_tls'] and response.geturl().startswith('https:')}))
except ProbeError as error:
    print(json.dumps({'error':str(error)}))
except OverflowError:
    print(json.dumps({'error': 'RESPONSE_TOO_LARGE'}))
except (socket.timeout, TimeoutError):
    print(json.dumps({'error': 'TIMEOUT'}))
except urllib.error.URLError as error:
    print(json.dumps({'error': 'TIMEOUT' if isinstance(error.reason,(socket.timeout,TimeoutError)) else 'TLS_ERROR' if isinstance(error.reason, ssl.SSLError) else 'NETWORK_ERROR'}))
except (OSError, ValueError, http.client.HTTPException):
    print(json.dumps({'error': 'HTTP_COLLECTION_ERROR'}))
`

// A supervising process owns the deadline: per-socket timeouts reset on reads
// and redirects, and cannot bound a blocking resolver. subprocess.run kills and
// reaps the worker on expiry, closing its network sockets before reporting.
const httpDeadlineProbe = `import json, subprocess, sys, time
config = json.loads(sys.argv[1])
start = time.monotonic()
try:
    result = subprocess.run([sys.executable,'-c',sys.argv[2],sys.argv[1]],
        stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=config['timeout_ms']/1000.0)
    observed = json.loads(result.stdout) if result.returncode==0 else {'error':'HTTP_COLLECTION_ERROR'}
except subprocess.TimeoutExpired:
    observed = {'error':'TIMEOUT'}
except (OSError,ValueError):
    observed = {'error':'HTTP_COLLECTION_ERROR'}
observed['duration_ms'] = (time.monotonic()-start)*1000
print(json.dumps(observed))
`

func boundedHTTPCommand(values map[string]plan.ResolvedParam) string {
	config := map[string]any{}
	for _, name := range []string{"url", "method", "verify_tls", "follow_redirects", "timeout_ms"} {
		config[name] = values[name].Value
	}
	_, contains := values["body_contains"]
	_, pattern := values["body_regex"]
	config["collect_body"] = contains || pattern
	raw, _ := json.Marshal(config)
	return "python3 -c " + shellQuote(httpDeadlineProbe) + " " + shellQuote(string(raw)) + " " + shellQuote(boundedHTTPProbe)
}

type boundedHTTPObservation struct {
	Status      int                 `json:"status"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body_base64"`
	Error       string              `json:"error"`
	TLSVerified bool                `json:"tls_verified"`
	HeaderBytes int                 `json:"header_bytes"`
	Redirects   int                 `json:"redirects"`
	DurationMS  float64             `json:"duration_ms"`
}

func boundedHTTPEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"body_limit_bytes": 1048576, "response_limit_bytes": 1048576, "header_limit_bytes": 65536, "redirect_limit": 10, "timeout_ms": params["timeout_ms"]}
	var observed boundedHTTPObservation
	if err := json.Unmarshal([]byte(text), &observed); err != nil {
		return "ERROR", summary
	}
	summary["duration_ms"] = observed.DurationMS
	if observed.Error != "" {
		summary["error"] = observed.Error
		return "ERROR", summary
	}
	body, err := base64.StdEncoding.DecodeString(observed.Body)
	if err != nil || len(body)+observed.HeaderBytes > 1048576 || observed.Status < 100 || observed.Status > 599 || observed.HeaderBytes < 0 || observed.HeaderBytes > 65536 || observed.Redirects < 0 || observed.Redirects > 10 || observed.DurationMS < 0 {
		return "ERROR", summary
	}
	summary["status"] = observed.Status
	summary["body_bytes"] = len(body)
	summary["tls_verified"] = observed.TLSVerified
	summary["header_bytes"] = observed.HeaderBytes
	summary["response_bytes"] = observed.HeaderBytes + len(body)
	summary["redirects"] = observed.Redirects
	summary["body"] = redactedEvidence(string(body))
	if fmt.Sprint(observed.Status) != fmt.Sprint(params["status"]) {
		return "FAIL", summary
	}
	if contains, ok := params["body_contains"].(string); ok && !strings.Contains(string(body), contains) {
		return "FAIL", summary
	}
	if pattern, ok := params["body_regex"].(string); ok {
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return "ERROR", summary
		}
		if !expression.Match(body) {
			return "FAIL", summary
		}
	}
	if headers, ok := params["headers"].(map[string]any); ok {
		for name, value := range headers {
			matched := false
			for _, actual := range observed.Headers[strings.ToLower(name)] {
				if actual == fmt.Sprint(value) {
					matched = true
				}
			}
			if !matched {
				return "FAIL", summary
			}
		}
	}
	return "PASS", summary
}
