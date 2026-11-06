package inventory_test

import (
	"github.com/fleeto/readyspec/internal/inventory"
	"strings"
	"testing"
)

func TestQuickInventoryRejectsDuplicateEndpoints(t *testing.T) {
	hosts, err := inventory.Parse("web=10.0.0.1,db=[2001:db8::1]:2222", "quick")
	if err != nil || len(hosts) != 2 || hosts[0].ID != "db" || hosts[0].Port != 2222 {
		t.Fatalf("hosts=%+v err=%v", hosts, err)
	}
	if _, err := inventory.Parse("a=10.0.0.1,b=10.0.0.1:22", "quick"); err == nil {
		t.Fatal("duplicate endpoint accepted")
	}
}

func TestAnsibleFormatsPreserveGroupMembership(t *testing.T) {
	for format, source := range map[string]string{
		"ini":  "[web]\nnode ansible_host=10.0.0.1 ansible_port=2222\n[prod:children]\nweb\n",
		"yaml": "all:\n  children:\n    prod:\n      children:\n        web:\n          hosts:\n            node:\n              ansible_host: 10.0.0.1\n              ansible_port: 2222\n",
	} {
		hosts, err := inventory.Parse(source, format)
		if err != nil || len(hosts) != 1 || hosts[0].Port != 2222 {
			t.Fatalf("%s: %+v %v", format, hosts, err)
		}
		found := false
		for _, group := range hosts[0].Groups {
			if group == "prod" {
				found = true
			}
		}
		if !found {
			t.Fatalf("parent group missing: %+v", hosts)
		}
	}
}

func TestStaticGroupConnectionDefaultsAndHostPrecedence(t *testing.T) {
	for format, source := range map[string]string{
		"ini":  "[all:vars]\nansible_user=root\nansible_port=2222\n[web:vars]\nansible_user=app\n[web]\nnode ansible_host=10.0.0.1\n[db]\nnode\n",
		"yaml": "all:\n  vars: {ansible_user: root, ansible_port: 2222}\n  children:\n    web:\n      vars: {ansible_user: app}\n      hosts:\n        node: {ansible_host: 10.0.0.1}\n    db:\n      hosts: {node: null}\n",
	} {
		hosts, err := inventory.Parse(source, format)
		if err != nil || len(hosts) != 1 || hosts[0].Username != "app" || hosts[0].Port != 2222 || hosts[0].Address != "10.0.0.1" {
			t.Fatalf("%s: %+v %v", format, hosts, err)
		}
	}
	source := "[web:vars]\nansible_user=web\n[db:vars]\nansible_user=db\n[web]\nnode ansible_host=10.0.0.1\n[db]\nnode\n"
	if _, err := inventory.Parse(source, "ini"); err == nil {
		t.Fatal("conflicting sibling group users accepted")
	}
	hosts, err := inventory.Parse(strings.Replace(source, "node ansible_host=10.0.0.1", "node ansible_host=10.0.0.1 ansible_user=host", 1), "ini")
	if err != nil || hosts[0].Username != "host" {
		t.Fatalf("explicit host user did not disambiguate: %+v %v", hosts, err)
	}
}

func TestConnectionDefaultsRespectAncestorsAndExplicitHostFields(t *testing.T) {
	source := "[prod:vars]\nansible_port=2222\nansible_user=parent\n[web:vars]\nansible_port=2200\n[prod:children]\nweb\n[web]\nnode ansible_host=10.0.0.1 ansible_port=22\nother ansible_host=10.0.0.2\n"
	hosts, err := inventory.Parse(source, "ini")
	if err != nil || len(hosts) != 2 || hosts[0].Port != 22 || hosts[1].Port != 2200 || hosts[0].Username != "parent" {
		t.Fatalf("%+v %v", hosts, err)
	}
	for _, source := range []string{
		"[web:vars]\nansible_password=secret\n[web]\nnode\n",
		"[web:vars]\nansible_port=0\n[web]\nnode\n",
		"[web:vars]\nansible_user={{ lookup }}\n[web]\nnode\n",
		"[web]\nnode ansible_host=10.0.0.1\nnode ansible_host=10.0.0.2\n",
	} {
		if _, err := inventory.Parse(source, "ini"); err == nil {
			t.Fatal("invalid connection defaults accepted")
		}
	}
}
