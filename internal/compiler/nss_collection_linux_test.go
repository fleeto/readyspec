//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

// A real NSS provider loaded by libc inside the sandbox. It supplies remote
// identities that do not exist in /etc/passwd or /etc/group and reports actual
// NSS UNAVAIL/TRYAGAIN statuses, rather than replacing the collector command.
const nssFixtureSource = `
#include <errno.h>
#include <grp.h>
#include <nss.h>
#include <pwd.h>
#include <string.h>

static enum nss_status failure(const char *name, int *errnop) {
  if (!strcmp(name, "failure")) { *errnop=EIO; return NSS_STATUS_UNAVAIL; }
  if (!strcmp(name, "retry")) { *errnop=EAGAIN; return NSS_STATUS_TRYAGAIN; }
  if (!strcmp(name, "denied")) { *errnop=EACCES; return NSS_STATUS_UNAVAIL; }
  *errnop=0; return NSS_STATUS_NOTFOUND;
}

enum nss_status _nss_fixture_getpwnam_r(const char *name, struct passwd *out,
    char *buffer, size_t length, int *errnop) {
  (void)buffer; (void)length;
  unsigned uid=0, gid=0;
  if (!strcmp(name,"large") || !strcmp(name,"too_large")) {
    if (length<16384 || !strcmp(name,"too_large")) { *errnop=ERANGE; return NSS_STATUS_TRYAGAIN; }
    uid=2105; gid=2100;
  }
  else if (!strcmp(name,"largeid")) { uid=2100000000; gid=2100000001; }
  else if (!strcmp(name,"remote") || !strcmp(name,"remote@realm")) { uid=2101; gid=2100; }
  else if (!strcmp(name,"primary")) { uid=2102; gid=2100; }
  else if (!strcmp(name,"extra")) { uid=2103; gid=2101; }
  else if (!strcmp(name,"outsider")) { uid=2104; gid=2101; }
  else return failure(name,errnop);
  *out=(struct passwd){ .pw_name=(char *)name, .pw_passwd="must-not-leak", .pw_uid=uid,
    .pw_gid=gid, .pw_gecos="private-contact", .pw_dir="/private-home", .pw_shell="/bin/sh" };
  *errnop=0; return NSS_STATUS_SUCCESS;
}

enum nss_status _nss_fixture_getgrnam_r(const char *name, struct group *out,
    char *buffer, size_t length, int *errnop) {
  (void)buffer; (void)length;
  static char *members[]={"extra","missing",NULL};
  if (!strcmp(name,"largegroup") && length<16384) { *errnop=ERANGE; return NSS_STATUS_TRYAGAIN; }
  if (strcmp(name,"remotegroup") && strcmp(name,"largegroup") && strcmp(name,"largeidgroup") && strcmp(name,"DOMAIN\\remote group")) return failure(name,errnop);
  *out=(struct group){ .gr_name=(char *)name, .gr_passwd="must-not-leak", .gr_gid=(!strcmp(name,"largeidgroup") ? 2100000001 : 2100),
    .gr_mem=members };
  *errnop=0; return NSS_STATUS_SUCCESS;
}
`

func buildNSSFixture(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Fatal("NSS collection tests require a C compiler")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.c")
	module := filepath.Join(dir, "libnss_fixture.so.2")
	if err := os.WriteFile(source, []byte(nssFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cc", "-shared", "-fPIC", "-Wall", "-Werror", "-o", module, source).CombinedOutput(); err != nil {
		t.Fatalf("build NSS fixture: %v %s", err, out)
	}
	raw, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func collectWithNSSFixture(t *testing.T, root string, a compiler.Artifact) []compiler.ItemResult {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := sandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json")
	for i, arg := range cmd.Args {
		if arg == "--" {
			args := append([]string{}, cmd.Args[:i]...)
			args = append(args, "--setenv", "LD_LIBRARY_PATH", "/tools")
			cmd.Args = append(args, cmd.Args[i:]...)
			break
		}
	}
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		t.Fatal(err)
	}
	results, err := compiler.Normalize(a, out)
	if err != nil {
		t.Fatalf("normalize %v output=%s", err, out)
	}
	return results
}

func TestNSSCollectorsQueryProvidersAndClassifyFailures(t *testing.T) {
	module := buildNSSFixture(t)
	for _, tt := range []struct {
		name, id, policy, unreadable string
		params                       map[string]any
		status                       string
	}{
		{"remote_user", "A01", "files fixture", "", map[string]any{"name": "remote", "exists": true, "uid": float64(2101), "gid": float64(2100), "shell": "/bin/sh"}, "PASS"},
		{"remote_user_wrong_uid", "A01", "files fixture", "", map[string]any{"name": "remote", "exists": true, "uid": float64(2102)}, "FAIL"},
		{"remote_large_uid", "A01", "files fixture", "", map[string]any{"name": "largeid", "exists": true, "uid": float64(2100000000), "gid": float64(2100000001)}, "PASS"},
		{"remote_large_gid", "A02", "files fixture", "", map[string]any{"name": "largeidgroup", "gid": float64(2100000001), "members": []any{"largeid"}}, "PASS"},
		{"missing_user_required", "A01", "files fixture", "", map[string]any{"name": "missing", "exists": true}, "FAIL"},
		{"missing_user_expected", "A01", "files fixture", "", map[string]any{"name": "missing", "exists": false}, "PASS"},
		{"unavailable_user_not_absent", "A01", "files fixture", "", map[string]any{"name": "failure", "exists": false}, "ERROR"},
		{"unavailable_required_user", "A01", "files fixture", "", map[string]any{"name": "failure", "exists": true}, "ERROR"},
		{"retry_user_not_absent", "A01", "files fixture", "", map[string]any{"name": "retry", "exists": false}, "ERROR"},
		{"denied_user_not_absent", "A01", "files fixture", "", map[string]any{"name": "denied", "exists": false}, "ERROR"},
		{"passwd_unreadable", "A01", "files", "etc/passwd", map[string]any{"name": "missing", "exists": false}, "ERROR"},
		{"primary_and_supplementary", "A02", "files fixture", "", map[string]any{"name": "remotegroup", "gid": float64(2100), "members": []any{"primary", "extra"}}, "PASS"},
		{"wrong_group_gid", "A02", "files fixture", "", map[string]any{"name": "remotegroup", "gid": float64(2101), "members": []any{}}, "FAIL"},
		{"outsider_is_not_member", "A02", "files fixture", "", map[string]any{"name": "remotegroup", "members": []any{"outsider"}}, "FAIL"},
		{"missing_group", "A02", "files fixture", "", map[string]any{"name": "missing", "members": []any{}}, "FAIL"},
		{"group_query_failure", "A02", "files fixture", "", map[string]any{"name": "failure", "members": []any{}}, "ERROR"},
		{"group_query_retry", "A02", "files fixture", "", map[string]any{"name": "retry", "members": []any{}}, "ERROR"},
		{"required_user_query_failure", "A02", "files fixture", "", map[string]any{"name": "remotegroup", "members": []any{"failure"}}, "ERROR"},
		{"group_database_unreadable", "A02", "files", "etc/group", map[string]any{"name": "missing", "members": []any{}}, "ERROR"},
		{"member_database_unreadable", "A02", "files fixture", "etc/passwd", map[string]any{"name": "remotegroup", "members": []any{"primary"}}, "ERROR"},
		{"masked_user_failure", "A01", "fixture files", "", map[string]any{"name": "failure", "exists": false}, "ERROR"},
		{"masked_group_failure", "A02", "fixture files", "", map[string]any{"name": "failure", "members": []any{}}, "ERROR"},
		{"masked_member_failure", "A02", "fixture files", "", map[string]any{"name": "remotegroup", "members": []any{"failure"}}, "ERROR"},
		{"authoritative_notfound_policy", "A01", "files [NOTFOUND=return] fixture", "", map[string]any{"name": "remote", "exists": false}, "PASS"},
		{"negated_status_policy_failure", "A01", "fixture [!UNAVAIL=return] files", "", map[string]any{"name": "failure", "exists": false}, "ERROR"},
		{"namespaced_user", "A01", "files fixture", "", map[string]any{"name": "remote@realm", "exists": true, "uid": float64(2101)}, "PASS"},
		{"namespaced_group", "A02", "files fixture", "", map[string]any{"name": "DOMAIN\\remote group", "members": []any{"primary", "extra"}}, "PASS"},
		{"user_buffer_growth", "A01", "files fixture", "", map[string]any{"name": "large", "exists": true, "uid": float64(2105)}, "PASS"},
		{"user_buffer_limit", "A01", "files fixture", "", map[string]any{"name": "too_large", "exists": false}, "ERROR"},
		{"group_buffer_growth", "A02", "files fixture", "", map[string]any{"name": "largegroup", "members": []any{"primary"}}, "PASS"},
		{"stale_supplementary_member", "A02", "files fixture", "", map[string]any{"name": "remotegroup", "members": []any{"missing"}}, "FAIL"},
		{"nss_config_unreadable", "A01", "files fixture", "etc/nsswitch.conf", map[string]any{"name": "missing", "exists": false}, "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			if err := os.WriteFile(filepath.Join(root, "tools/libnss_fixture.so.2"), module, 0700); err != nil {
				t.Fatal(err)
			}
			fixtureFile(t, root, "etc/nsswitch.conf", "passwd: "+tt.policy+"\ngroup: "+tt.policy+"\n")
			if tt.unreadable != "" {
				if err := os.Chmod(filepath.Join(root, tt.unreadable), 0000); err != nil {
					t.Fatal(err)
				}
			}
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectWithNSSFixture(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				raw, _ := json.Marshal(results)
				t.Fatalf("want %s got %s", tt.status, raw)
			}
			raw, _ := json.Marshal(results)
			for _, secret := range []string{"must-not-leak", "private-contact", "private-home"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("unrelated NSS field leaked: %s", secret)
				}
			}
			if tt.status == "ERROR" {
				code := fmt.Sprint(evidenceProperty(t, results, "error"))
				if code == "" {
					t.Fatal("typed NSS failure evidence missing")
				}
				if tt.unreadable != "" && code != "SOURCE_UNREADABLE" {
					t.Fatalf("unreadable database misclassified: %s", code)
				}
				if strings.HasPrefix(tt.name, "masked_") && code != "LOOKUP_INCOMPLETE" {
					t.Fatalf("continued NSS failure must remain incomplete, got %s", code)
				}
			}
			if tt.name == "remote_user" {
				if actual := evidenceProperty(t, results, "uid"); actual == nil {
					t.Fatal("NSS identity evidence missing")
				}
			}
			if tt.name == "primary_and_supplementary" {
				users, ok := evidenceProperty(t, results, "required_users").([]any)
				if !ok || len(users) != 2 {
					t.Fatal("required user query evidence missing")
				}
				gids := map[string]string{}
				for _, value := range users {
					user := value.(map[string]any)
					gids[user["requested"].(string)] = fmt.Sprint(user["gid"])
				}
				if gids["primary"] != "2100" || gids["extra"] != "2101" {
					t.Fatalf("primary/extra identities lost: %v", gids)
				}
				members := fmt.Sprint(evidenceProperty(t, results, "supplementary_members"))
				if !strings.Contains(members, "extra") {
					t.Fatal("supplementary group entry lost")
				}
			}
		})
	}
}

func TestLocalNSSCollectorsDoNotTreatMissingDatabaseAsMissingIdentity(t *testing.T) {
	for _, tt := range []struct {
		id, database string
		params       map[string]any
	}{
		{"A01", "passwd", map[string]any{"name": "missing", "exists": false}},
		{"A02", "group", map[string]any{"name": "missing", "members": []any{}}},
	} {
		t.Run(tt.id, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "etc/nsswitch.conf", "passwd: files\ngroup: files\n")
			if err := os.Remove(filepath.Join(root, "etc", tt.database)); err != nil {
				t.Fatal(err)
			}
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectWithNSSFixture(t, root, a)
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("missing source must not establish absence: %+v", results)
			}
			if code := evidenceProperty(t, results, "error"); code != "SOURCE_UNREADABLE" {
				t.Fatalf("wrong failure classification %v", code)
			}
		})
	}
}
