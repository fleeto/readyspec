package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

// Inspect library headers as data. Never invoke the loader, ldd, the target,
// or library initializers. A cached name alone proves neither ABI nor existence.
const elfProbe = `import json, os, re, struct, subprocess, sys

class ProbeError(Exception):
    pass

def header(path):
    with open(path,'rb') as source:
        raw = source.read(64)
        if len(raw)<20 or raw[:4]!=b'\x7fELF' or raw[4] not in (1,2) or raw[5] not in (1,2) or raw[6]!=1:
            raise ValueError('INVALID_ELF')
        wide = raw[4]==2
        size = 64 if wide else 52
        if len(raw)<size:
            raise ValueError('INVALID_ELF')
        order = '<' if raw[5]==1 else '>'
        kind,machine,version = struct.unpack_from(order+'HHI',raw,16)
        phoff = struct.unpack_from(order+('Q' if wide else 'I'),raw,32 if wide else 28)[0]
        ehsize,phentsize,phnum = struct.unpack_from(order+'HHH',raw,52 if wide else 40)
        length = os.fstat(source.fileno()).st_size
        expected = 56 if wide else 32
        if version!=1 or ehsize!=size or phnum==0 or phnum==65535 or phentsize!=expected or phoff<size or phoff+phnum*phentsize>length:
            raise ValueError('INVALID_ELF')
        load, pie, interpreter = False, False, False
        dynamic = []
        for index in range(phnum):
            source.seek(phoff+index*phentsize)
            segment = source.read(phentsize)
            if len(segment)!=phentsize:
                raise ValueError('INVALID_ELF')
            tag = struct.unpack_from(order+'I',segment)[0]
            if wide:
                offset,filesize,memsize = struct.unpack_from(order+'Q',segment,8)[0],struct.unpack_from(order+'Q',segment,32)[0],struct.unpack_from(order+'Q',segment,40)[0]
            else:
                offset,filesize,memsize = struct.unpack_from(order+'I',segment,4)[0],struct.unpack_from(order+'I',segment,16)[0],struct.unpack_from(order+'I',segment,20)[0]
            if offset+filesize>length or (tag==1 and filesize>memsize):
                raise ValueError('INVALID_ELF')
            if tag==1:
                load = True
            if tag==3:
                interpreter = True
            if tag==2:
                dynamic.append((offset,filesize))
        if not load:
            raise ValueError('INVALID_ELF')
        for offset,filesize in dynamic:
            width = 16 if wide else 8
            if filesize%width or filesize>1048576:
                raise ValueError('INVALID_ELF')
            source.seek(offset)
            entries = source.read(filesize)
            if len(entries)!=filesize:
                raise ValueError('INVALID_ELF')
            terminated = False
            for tag,value in struct.iter_unpack(order+('QQ' if wide else 'II'),entries):
                if tag==0:
                    terminated = True
                    break
                if tag==0x6ffffffb and value & 0x08000000:
                    pie = True
            if not terminated:
                raise ValueError('INVALID_ELF')
    return {'class':32 if raw[4]==1 else 64,'byte_order':'little' if raw[5]==1 else 'big',
        'machine':machine,'type':kind,'pie':pie,'interpreter':interpreter}

def run(arguments,code):
    try:
        result = subprocess.run(arguments,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,
            timeout=8,env=dict(os.environ,LC_ALL='C'))
    except (OSError,subprocess.TimeoutExpired):
        raise ProbeError(code)
    if result.returncode or len(result.stdout)>8388608:
        raise ProbeError(code)
    return result.stdout.decode('utf-8','strict')

try:
    path = sys.argv[1]
    target = header(path)
    observed = run(['readelf','-h','-d','--',path],'READELF_FAILED')
    if not re.search(r'^\s*Class:\s+ELF'+str(target['class'])+r'\s*$',observed,re.M):
        raise ProbeError('INVALID_READELF_OUTPUT')
    if 'Dynamic section' not in observed and 'There is no dynamic section in this file.' not in observed:
        raise ProbeError('INVALID_READELF_OUTPUT')
    needed = []
    for line in observed.splitlines():
        if '(NEEDED)' not in line:
            continue
        match = re.search(r'\(NEEDED\)\s+Shared library: \[(.+)\]\s*$',line)
        if not match:
            raise ProbeError('INVALID_NEEDED_RECORD')
        needed.append(match.group(1))
    needed = sorted(set(needed))
    if len(needed)>4096:
        raise ProbeError('TOO_MANY_DEPENDENCIES')
    entries = []
    if needed:
        cache = run(['ldconfig','-p'],'CACHE_QUERY_FAILED')
        count = re.match(r'([0-9]+) libs found in cache',cache)
        if not count:
            raise ProbeError('INVALID_CACHE_OUTPUT')
        parsed = []
        for line in cache.splitlines():
            match = re.match(r'^\s*(.+?)\s+\(([^)]+)\)\s+=>\s+(/.+)\s*$',line)
            if match:
                parsed.append(match.groups())
        if len(parsed)!=int(count.group(1)):
            raise ProbeError('INCOMPLETE_CACHE_OUTPUT')
        for name,flags,library in parsed:
            if name not in needed:
                continue
            entry = {'name':name,'flags':flags,'path':library}
            try:
                entry['header'] = header(library)
                entry['status'] = 'OK'
            except FileNotFoundError:
                entry['status'] = 'MISSING'
            except (ValueError,IsADirectoryError):
                entry['status'] = 'INVALID_ELF'
            except OSError as error:
                entry['status'] = 'UNREADABLE'
                entry['errno'] = error.errno or 0
            entries.append(entry)
    print(json.dumps({'header':target,'needed':needed,'readelf':observed,
        'cache_queried':bool(needed),'cache_entries':entries}))
except ProbeError as error:
    print(json.dumps({'error':str(error)}))
except (OSError,ValueError,UnicodeError):
    print(json.dumps({'error':'ELF_COLLECTION_ERROR'}))
`

func elfCommand(values map[string]plan.ResolvedParam) string {
	return "python3 -c " + shellQuote(elfProbe) + " " + shellQuote(values["path"].Value.(string))
}

type elfHeader struct {
	Class       int    `json:"class"`
	ByteOrder   string `json:"byte_order"`
	Machine     int    `json:"machine"`
	Type        int    `json:"type"`
	PIE         bool   `json:"pie"`
	Interpreter bool   `json:"interpreter"`
}

type elfCacheEntry struct {
	Name   string     `json:"name"`
	Flags  string     `json:"flags"`
	Path   string     `json:"path"`
	Status string     `json:"status"`
	Header *elfHeader `json:"header,omitempty"`
	Errno  int        `json:"errno,omitempty"`
}

type elfObservation struct {
	Header       *elfHeader      `json:"header,omitempty"`
	Needed       []string        `json:"needed"`
	Readelf      string          `json:"readelf,omitempty"`
	CacheQueried bool            `json:"cache_queried"`
	Entries      []elfCacheEntry `json:"cache_entries"`
	Error        string          `json:"error,omitempty"`
}

func validELFHeader(header *elfHeader) bool {
	return header != nil && (header.Class == 32 || header.Class == 64) && (header.ByteOrder == "little" || header.ByteOrder == "big") && header.Machine > 0 && header.Type > 0
}

func elfArchitecture(header *elfHeader) string {
	switch header.Machine {
	case 62:
		return "x86_64"
	case 183:
		return "aarch64"
	case 3:
		return "i386"
	case 40:
		return "arm"
	default:
		return fmt.Sprintf("machine:%d", header.Machine)
	}
}

func cacheMatchesELF(flags string, header *elfHeader) bool {
	flags = strings.ToLower(flags)
	switch header.Machine {
	case 62:
		if header.Class == 64 {
			return strings.Contains(flags, "x86-64")
		}
		return strings.Contains(flags, "x32")
	case 183:
		return header.Class == 64 && strings.Contains(flags, "aarch64")
	default:
		return false
	}
}

func elfEvaluation(params map[string]any, text string) (string, map[string]any) {
	var observed elfObservation
	if err := json.Unmarshal([]byte(text), &observed); err != nil {
		return "ERROR", map[string]any{"error": "INVALID_ELF_OBSERVATION"}
	}
	summary := map[string]any{"header": observed.Header, "needed": observed.Needed, "readelf": observed.Readelf, "cache_entries": observed.Entries, "cache_queried": observed.CacheQueried}
	if observed.Error != "" {
		summary["error"] = observed.Error
		return "ERROR", summary
	}
	if !validELFHeader(observed.Header) || observed.Needed == nil || observed.Readelf == "" {
		return "ERROR", summary
	}
	arch := elfArchitecture(observed.Header)
	summary["architecture"] = arch
	if arch != params["architecture"] || (observed.Header.Type != 2 && observed.Header.Type != 3) {
		return "FAIL", summary
	}
	needed := map[string]bool{}
	for _, library := range observed.Needed {
		if library == "" {
			return "ERROR", summary
		}
		needed[library] = true
	}
	required, ok := params["required_libraries"].([]any)
	if !ok {
		return "ERROR", summary
	}
	for _, value := range required {
		library, ok := value.(string)
		if !ok {
			return "ERROR", summary
		}
		if !needed[library] {
			return "FAIL", summary
		}
	}
	if len(needed) > 0 && !observed.CacheQueried {
		return "ERROR", summary
	}
	resolved, bad, uncertain := map[string]bool{}, false, false
	for _, entry := range observed.Entries {
		if !needed[entry.Name] {
			return "ERROR", summary
		}
		if !strings.HasPrefix(entry.Path, "/") || entry.Flags == "" {
			return "ERROR", summary
		}
		if !cacheMatchesELF(entry.Flags, observed.Header) {
			continue
		}
		switch entry.Status {
		case "OK":
			if !validELFHeader(entry.Header) {
				return "ERROR", summary
			}
			if entry.Header.Type != 3 || entry.Header.PIE || entry.Header.Interpreter || entry.Header.Class != observed.Header.Class || entry.Header.ByteOrder != observed.Header.ByteOrder || entry.Header.Machine != observed.Header.Machine {
				bad = true
			} else {
				resolved[entry.Name] = true
			}
		case "MISSING", "INVALID_ELF":
			bad = true
		case "UNREADABLE":
			uncertain = true
		default:
			return "ERROR", summary
		}
	}
	if uncertain {
		return "ERROR", summary
	}
	if bad {
		return "FAIL", summary
	}
	for library := range needed {
		if !resolved[library] {
			return "FAIL", summary
		}
	}
	return "PASS", summary
}
