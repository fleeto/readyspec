package compiler

import (
	"encoding/json"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

// pwd/grp hide some libc lookup failures as KeyError. Use the reentrant libc
// interfaces to retain errno, and diagnose missing results against configured
// NSS providers so a continued UNAVAIL/TRYAGAIN cannot prove absence.
const nssProbe = `import ctypes as C, errno, json, re, sys

class QueryError(Exception):
    def __init__(self, code, database, number=0):
        self.code, self.database, self.number = code, database, number

class Passwd(C.Structure):
    _fields_ = [('name',C.c_char_p),('password',C.c_char_p),('uid',C.c_uint),
        ('gid',C.c_uint),('gecos',C.c_char_p),('home',C.c_char_p),('shell',C.c_char_p)]

class Group(C.Structure):
    _fields_ = [('name',C.c_char_p),('password',C.c_char_p),('gid',C.c_uint),
        ('members',C.POINTER(C.c_char_p))]

libc = C.CDLL(None, use_errno=True)
policies = {}
modules = []
states = ['SUCCESS','NOTFOUND','UNAVAIL','TRYAGAIN']

def policy(database):
    if database in policies:
        return policies[database]
    try:
        with open('/etc/nsswitch.conf') as source:
            text = source.read(65537)
    except FileNotFoundError:
        text = ''
    except OSError as error:
        raise QueryError('SOURCE_UNREADABLE',database,error.errno or 0)
    if len(text)>65536:
        raise QueryError('CONFIG_TOO_LARGE',database)
    definition = None
    for line in text.replace('\\\n','').splitlines():
        line = line.split('#',1)[0].strip()
        name, separator, value = line.partition(':')
        if separator and name.strip()==database:
            if definition is not None:
                raise QueryError('AMBIGUOUS_CONFIG',database)
            definition = value.strip()
    if definition is None:
        definition = 'files'
    providers = []
    position = 0
    while position<len(definition):
        match = re.match(r'\s*(\[[^\]]*\]|[A-Za-z0-9_-]+)',definition[position:])
        if not match:
            if not definition[position:].strip():
                break
            raise QueryError('INVALID_CONFIG',database)
        token = match.group(1)
        position += match.end()
        if token.startswith('['):
            if not providers:
                raise QueryError('INVALID_CONFIG',database)
            for condition in token[1:-1].split():
                rule = re.fullmatch(r'(!?)(SUCCESS|NOTFOUND|UNAVAIL|TRYAGAIN)=(return|continue|merge)',condition,re.I)
                if not rule:
                    raise QueryError('UNSUPPORTED_CONFIG',database)
                negate, state, action = rule.groups()
                for candidate in states:
                    if (candidate==state.upper()) != bool(negate):
                        providers[-1][1][candidate] = action.lower()
        else:
            providers.append((token,{'SUCCESS':'return','NOTFOUND':'continue','UNAVAIL':'continue','TRYAGAIN':'continue'}))
    if not providers or len(providers)>32:
        raise QueryError('INVALID_CONFIG',database)
    if any(service in ('files','compat') for service, actions in providers):
        try:
            with open('/etc/'+database,'rb') as source:
                source.read(1)
        except OSError as error:
            raise QueryError('SOURCE_UNREADABLE',database,error.errno or 0)
    policies[database] = providers
    return providers

def text(value):
    return value.decode('utf-8','strict') if value else ''

def diagnose_absence(database,name,record_type,function):
    for service,actions in policy(database):
        symbol = '_nss_'+service+'_'+function
        try:
            module = libc if hasattr(libc,symbol) else C.CDLL('libnss_'+service+'.so.2',use_errno=True)
            modules.append(module)
            query = getattr(module,symbol)
        except (OSError,AttributeError):
            raise QueryError('PROVIDER_UNAVAILABLE',database)
        query.argtypes = [C.c_char_p,C.POINTER(record_type),C.c_void_p,C.c_size_t,C.POINTER(C.c_int)]
        query.restype = C.c_int
        size = 4096
        while True:
            record,buffer,number = record_type(),C.create_string_buffer(size),C.c_int(0)
            status = query(name,C.byref(record),buffer,size,C.byref(number))
            if status==-2 and number.value==errno.ERANGE and size<1048576:
                size *= 2
                continue
            break
        if status in (-2,-1):
            raise QueryError('LOOKUP_INCOMPLETE',database,number.value)
        if status==1:
            raise QueryError('LOOKUP_CHANGED',database)
        if status!=0:
            raise QueryError('UNKNOWN_PROVIDER_STATUS',database,number.value)
        if actions['NOTFOUND']=='return':
            break

def lookup(database,name):
    policy(database)
    if not isinstance(name,str) or not name or any(char in name for char in '\0\r\n:'):
        raise QueryError('INVALID_NAME',database)
    encoded = name.encode('utf-8')
    record_type,function = (Passwd,'getpwnam_r') if database=='passwd' else (Group,'getgrnam_r')
    query = getattr(libc,function)
    query.argtypes = [C.c_char_p,C.POINTER(record_type),C.c_void_p,C.c_size_t,C.POINTER(C.POINTER(record_type))]
    query.restype = C.c_int
    size = 4096
    while True:
        record,buffer,result = record_type(),C.create_string_buffer(size),C.POINTER(record_type)()
        code = query(encoded,C.byref(record),buffer,size,C.byref(result))
        if code==errno.ERANGE and size<1048576:
            size *= 2
            continue
        break
    if code:
        raise QueryError('LOOKUP_FAILED',database,code)
    if not result:
        diagnose_absence(database,encoded,record_type,function)
        return {'exists':False,'lookup_status':'NOTFOUND','source':'libc NSS'}
    if database=='passwd':
        return {'exists':True,'lookup_status':'SUCCESS','source':'libc NSS',
            'name':text(record.name),'uid':record.uid,'gid':record.gid,'shell':text(record.shell)}
    members = []
    if record.members:
        for index in range(100001):
            if not record.members[index]:
                break
            if index==100000:
                raise QueryError('GROUP_TOO_LARGE',database)
            members.append(text(record.members[index]))
    return {'exists':True,'lookup_status':'SUCCESS','source':'libc NSS',
        'name':text(record.name),'gid':record.gid,'supplementary_members':sorted(set(members))}

try:
    config = json.loads(sys.argv[1])
    if config['kind']=='A01':
        observed = lookup('passwd',config['name'])
    else:
        observed = lookup('group',config['name'])
        if observed['exists']:
            required = []
            for name in config['members']:
                user = lookup('passwd',name)
                user.pop('shell',None)
                user['requested'] = name
                required.append(user)
            observed['required_users'] = required
            observed['membership_scope'] = 'group_entry_and_required_users'
    print(json.dumps(observed))
except QueryError as error:
    print(json.dumps({'error':error.code,'database':error.database,'errno':error.number,'source':'libc NSS'}))
except (OSError,AttributeError,ValueError,UnicodeError):
    print(json.dumps({'error':'NSS_COLLECTION_ERROR','source':'libc NSS'}))
`

func nssCommand(id string, values map[string]plan.ResolvedParam) string {
	config := map[string]any{"kind": id, "name": values["name"].Value}
	if id == "A02" {
		config["members"] = values["members"].Value
	}
	raw, _ := json.Marshal(config)
	return "python3 -c " + shellQuote(nssProbe) + " " + shellQuote(string(raw))
}

type nssIdentity struct {
	Exists        *bool        `json:"exists"`
	Status        string       `json:"lookup_status"`
	Source        string       `json:"source"`
	Error         string       `json:"error"`
	Name          string       `json:"name"`
	UID           *json.Number `json:"uid"`
	GID           *json.Number `json:"gid"`
	Shell         string       `json:"shell"`
	Supplementary []string     `json:"supplementary_members"`
	Required      []struct {
		nssIdentity
		Requested string `json:"requested"`
	} `json:"required_users"`
}

func validNSSIdentity(identity nssIdentity, user bool) bool {
	if identity.Error != "" || identity.Source != "libc NSS" || identity.Exists == nil {
		return false
	}
	if !*identity.Exists {
		return identity.Status == "NOTFOUND"
	}
	if identity.Status != "SUCCESS" || identity.Name == "" || identity.GID == nil {
		return false
	}
	gid, err := identity.GID.Int64()
	if err != nil || gid < 0 || gid > 4294967295 {
		return false
	}
	if user {
		if identity.UID == nil {
			return false
		}
		uid, err := identity.UID.Int64()
		if err != nil || uid < 0 || uid > 4294967295 {
			return false
		}
	}
	return true
}

func nssEvaluation(id string, params map[string]any, text string) string {
	var observed nssIdentity
	if err := json.Unmarshal([]byte(text), &observed); err != nil || !validNSSIdentity(observed, id == "A01") {
		return "ERROR"
	}
	if id == "A01" {
		if *observed.Exists != params["exists"] {
			return "FAIL"
		}
		if !*observed.Exists {
			return "PASS"
		}
		for key, actual := range map[string]string{"uid": observed.UID.String(), "gid": observed.GID.String()} {
			if expected, ok := params[key]; ok {
				if status := exactNumericMatch(expected, actual, false); status != "PASS" {
					return status
				}
			}
		}
		if shell, ok := params["shell"]; ok && shell != observed.Shell {
			return "FAIL"
		}
		return "PASS"
	}
	if !*observed.Exists {
		return "FAIL"
	}
	if expected, ok := params["gid"]; ok {
		if status := exactNumericMatch(expected, observed.GID.String(), false); status != "PASS" {
			return status
		}
	}
	required, ok := params["members"].([]any)
	if !ok {
		return "ERROR"
	}
	seen := map[string]nssIdentity{}
	for _, user := range observed.Required {
		if _, duplicate := seen[user.Requested]; duplicate || !validNSSIdentity(user.nssIdentity, true) {
			return "ERROR"
		}
		seen[user.Requested] = user.nssIdentity
	}
	if len(seen) != len(required) {
		return "ERROR"
	}
	for _, value := range required {
		name, ok := value.(string)
		if !ok {
			return "ERROR"
		}
		user, ok := seen[name]
		if !ok {
			return "ERROR"
		}
		if !*user.Exists {
			return "FAIL"
		}
		member := user.GID.String() == observed.GID.String()
		for _, supplementary := range observed.Supplementary {
			if supplementary == name || supplementary == user.Name {
				member = true
			}
		}
		if !member {
			return "FAIL"
		}
	}
	return "PASS"
}

func nssEvidence(text string) any {
	var value any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return map[string]any{"error": "INVALID_NSS_OBSERVATION"}
	}
	return value
}
