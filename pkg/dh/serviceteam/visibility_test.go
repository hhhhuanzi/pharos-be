package serviceteam

import "testing"

func TestNormalizeNames(t *testing.T) {
	got := NormalizeNames([]string{" turms ", "", "rome-sec", "turms", "  "})
	if len(got) != 2 || got[0] != "turms" || got[1] != "rome-sec" {
		t.Fatalf("got %+v", got)
	}
}

func TestIsOpsRole(t *testing.T) {
	cases := []struct {
		roles []string
		want  bool
	}{
		{nil, false},
		{[]string{"Standard"}, false},
		{[]string{"Admin"}, false},
		{[]string{"SRE"}, true},
		{[]string{"sre"}, true},
		{[]string{"Sre"}, true},
		{[]string{"  SRE  "}, true},
		{[]string{"运维"}, true},
		{[]string{"Standard", "SRE"}, true},
		{[]string{"Guest", "运维"}, true},
		{[]string{"Ops"}, false},
	}
	for _, c := range cases {
		if got := IsOpsRole(c.roles); got != c.want {
			t.Fatalf("IsOpsRole(%v)=%v want %v", c.roles, got, c.want)
		}
	}
}

func TestCanViewAllAndManage(t *testing.T) {
	if !CanViewAll(true, nil, false) {
		t.Fatal("admin should view all")
	}
	if !CanViewAll(false, []string{"sre"}, false) {
		t.Fatal("SRE role should view all")
	}
	if !CanViewAll(false, nil, true) {
		t.Fatal("view-all perm should view all")
	}
	if CanViewAll(false, []string{"Standard"}, false) {
		t.Fatal("standard should not view all")
	}
	if !CanManage(false, []string{"运维"}, false) {
		t.Fatal("运维 role should manage")
	}
	if !CanManage(false, nil, true) {
		t.Fatal("manage perm should manage")
	}
	if CanManage(false, []string{"Guest"}, false) {
		t.Fatal("guest should not manage")
	}
}

func TestResolveBindingsPrefersExactThenServiceLevel(t *testing.T) {
	bindings := []Binding{
		{ServiceName: "turms-business-service", Env: "", UserGroupID: 1},
		{ServiceName: "turms-business-service", Env: "", UserGroupID: 4},
		{ServiceName: "turms-business-service", Env: "prod", UserGroupID: 2},
		{ServiceName: "trade-api", Env: "test", UserGroupID: 3},
	}

	prod := ResolveBindings(bindings, "turms-business-service", "prod")
	if len(prod) != 1 || prod[0].UserGroupID != 2 {
		t.Fatalf("exact env should win and stay single-row, got %+v", prod)
	}
	testEnv := ResolveBindings(bindings, "turms-business-service", "test")
	if len(testEnv) != 2 {
		t.Fatalf("missing env should fall back to all service-level teams, got %+v", testEnv)
	}
	empty := ResolveBindings(bindings, "turms-business-service", "")
	if len(empty) != 2 {
		t.Fatalf("empty env should hit service-level multi, got %+v", empty)
	}
	if b := ResolveBinding(bindings, "trade-api", "prod"); b != nil {
		t.Fatalf("no service-level fallback, got %+v", b)
	}
	if b := ResolveBinding(bindings, "missing", ""); b != nil {
		t.Fatalf("unknown service, got %+v", b)
	}
}

func TestCanSeeAnyTeam(t *testing.T) {
	bindings := []Binding{
		{ServiceName: "turms-business-service", Env: "", UserGroupID: 10},
		{ServiceName: "turms-business-service", Env: "", UserGroupID: 20},
	}
	first := GroupIDSet([]int64{10})
	second := GroupIDSet([]int64{20})
	other := GroupIDSet([]int64{99})

	if !CanSee(true, other, bindings, "unbound", "") {
		t.Fatal("viewAll must see unbound")
	}
	if CanSee(false, first, bindings, "unbound", "prod") {
		t.Fatal("regular must not see unbound")
	}
	if !CanSee(false, first, bindings, "turms-business-service", "prod") {
		t.Fatal("first team member should see shared service")
	}
	if !CanSee(false, second, bindings, "turms-business-service", "") {
		t.Fatal("second team member should also see shared service")
	}
	if CanSee(false, other, bindings, "turms-business-service", "") {
		t.Fatal("other team must not see")
	}
}

func TestFilterRefs(t *testing.T) {
	bindings := []Binding{
		{ServiceName: "turms-business-service", Env: "", UserGroupID: 10},
	}
	refs := []Ref{
		{Name: "turms-business-service", Env: "prod"},
		{Name: "trade-api", Env: "prod"},
		{Name: "orphan", Env: ""},
	}
	got := FilterRefs(false, GroupIDSet([]int64{10}), bindings, refs)
	if len(got) != 1 || got[0].Name != "turms-business-service" {
		t.Fatalf("got %+v", got)
	}
	all := FilterRefs(true, GroupIDSet([]int64{10}), bindings, refs)
	if len(all) != 3 {
		t.Fatalf("viewAll should keep all, got %+v", all)
	}
}
