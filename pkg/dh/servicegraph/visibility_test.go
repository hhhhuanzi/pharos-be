package servicegraph

import (
	"testing"

	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
)

func edge(client, server, connectionType string) Edge {
	return Edge{Client: client, Server: server, ConnectionType: connectionType, RequestCount: 10}
}

func canSeeIn(names ...string) CanSeeFunc {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return func(name string) bool {
		_, ok := set[name]
		return ok
	}
}

// canSeeByBindings 走真实判权链路：dh_service_team 行 -> CanSeeAnyEnv。
func canSeeByBindings(myGroups []int64, bindings []serviceteam.Binding) CanSeeFunc {
	groups := serviceteam.GroupIDSet(myGroups)
	return func(name string) bool {
		return CanSeeAnyEnv(false, groups, bindings, name)
	}
}

func edgeKeys(edges []Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.Client+"->"+e.Server+"|"+e.ConnectionType)
	}
	return out
}

func assertKeys(t *testing.T, got []Edge, want ...string) {
	t.Helper()
	keys := edgeKeys(got)
	if len(keys) != len(want) {
		t.Fatalf("edges = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("edges = %v, want %v", keys, want)
		}
	}
}

func assertServices(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("visible services = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("visible services = %v, want %v", got, want)
		}
	}
}

// 只有 user 入边、没有任何出边的叶子服务，绑定在我的团队上就必须留在图里。
func TestFilterKeepsBoundLeafServiceWithOnlyUserEntryEdge(t *testing.T) {
	bindings := []serviceteam.Binding{{ServiceName: "admin", Env: "", UserGroupID: 7}}
	res := Filter(FilterInput{
		Edges:  []Edge{edge("user", "admin", "virtual_node")},
		CanSee: canSeeByBindings([]int64{7}, bindings),
	})
	assertKeys(t, res.Edges, "user->admin|virtual_node")
	assertServices(t, res.VisibleServices, "admin")
}

func TestFilterDropsLeafServiceBoundToForeignTeam(t *testing.T) {
	bindings := []serviceteam.Binding{{ServiceName: "admin", Env: "", UserGroupID: 9}}
	foreign := Filter(FilterInput{
		Edges:  []Edge{edge("user", "admin", "virtual_node")},
		CanSee: canSeeByBindings([]int64{7}, bindings),
	})
	assertKeys(t, foreign.Edges)
	assertServices(t, foreign.VisibleServices)

	unbound := Filter(FilterInput{
		Edges:  []Edge{edge("user", "admin", "virtual_node")},
		CanSee: canSeeByBindings([]int64{7}, nil),
	})
	assertKeys(t, unbound.Edges)
}

func TestFilterDropsEdgesWithNoVouchingEnd(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("mine", "peer", ""),
			edge("foreign-a", "foreign-b", ""),
		},
		CanSee: canSeeIn("mine"),
	})
	assertKeys(t, res.Edges, "mine->peer|")
	assertServices(t, res.VisibleServices, "mine")
}

func TestFilterKeepsEdgeWhenEitherSideVouches(t *testing.T) {
	both := Filter(FilterInput{
		Edges: []Edge{
			edge("mine", "foreign", ""),
			edge("foreign", "mine", ""),
		},
		CanSee: canSeeIn("mine"),
	})
	assertKeys(t, both.Edges, "mine->foreign|", "foreign->mine|")

	clientSide := Filter(FilterInput{Edges: []Edge{edge("mine", "foreign", "")}, CanSee: canSeeIn("mine")})
	assertKeys(t, clientSide.Edges, "mine->foreign|")

	serverSide := Filter(FilterInput{Edges: []Edge{edge("foreign", "mine", "")}, CanSee: canSeeIn("mine")})
	assertKeys(t, serverSide.Edges, "foreign->mine|")
}

func TestFilterVirtualNodesFollowTheirEdges(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("mine", "mysql", "database"),
			edge("mine", "redis", "database"),
			edge("foreign", "clickhouse", "database"),
			edge("foreign", "mongodb", "database"),
		},
		CanSee: canSeeIn("mine"),
	})
	assertKeys(t, res.Edges, "mine->mysql|database", "mine->redis|database")
	assertServices(t, res.VisibleServices, "mine")
}

func TestFilterUnknownAndUserNodesCannotVouch(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("user", "mine", "virtual_node"),
			edge("user", "foreign", "virtual_node"),
			edge("mine", "unknown", "virtual_node"),
			edge("foreign", "unknown", "virtual_node"),
			edge("mine", "peer", ""),
		},
		CanSee: canSeeIn("mine"),
	})
	assertKeys(t, res.Edges, "user->mine|virtual_node", "mine->unknown|virtual_node", "mine->peer|")
	assertServices(t, res.VisibleServices, "mine")
}

// unknown 同时有入边和出边（按形状会被当成服务）时，仍然担保不了任何边。
func TestFilterUnknownWithInAndOutEdgesStillCannotVouch(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("foreign", "unknown", "virtual_node"),
			edge("unknown", "orphan-db", "database"),
			edge("unknown", "foreign-b", ""),
		},
		CanSee: canSeeIn("mine"),
	})
	assertKeys(t, res.Edges)
	assertServices(t, res.VisibleServices)
}

func TestFilterViewAllKeepsEverything(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("mine", "peer", ""),
			edge("foreign-a", "foreign-b", ""),
			edge("unknown", "orphan-db", "database"),
		},
		ViewAll: true,
		CanSee:  canSeeIn(),
	})
	assertKeys(t, res.Edges, "mine->peer|", "foreign-a->foreign-b|", "unknown->orphan-db|database")
	assertServices(t, res.VisibleServices, "foreign-a", "foreign-b", "mine", "orphan-db", "peer", "unknown")
}

func TestFilterUnboundServiceIsInvisible(t *testing.T) {
	bindings := []serviceteam.Binding{{ServiceName: "mine", Env: "", UserGroupID: 7}}
	res := Filter(FilterInput{
		Edges: []Edge{
			edge("mine", "peer", ""),
			edge("unbound-a", "unbound-b", ""),
		},
		CanSee: canSeeByBindings([]int64{7}, bindings),
	})
	assertKeys(t, res.Edges, "mine->peer|")
	assertServices(t, res.VisibleServices, "mine")
}

func TestFilterTruncates(t *testing.T) {
	res := Filter(FilterInput{
		Edges: []Edge{
			{Client: "mine", Server: "a", RequestCount: 100, FailedCount: 50, ErrorRate: 0.5},
			{Client: "mine", Server: "b", RequestCount: 100, ErrorRate: 0},
		},
		CanSee:   canSeeIn("mine"),
		MaxEdges: 1,
	})
	assertKeys(t, res.Edges, "mine->a|")
	if !res.Truncated {
		t.Fatal("want truncated")
	}
}

func TestCanSeeAnyEnvOrsOverEnvBindings(t *testing.T) {
	bindings := []serviceteam.Binding{
		{ServiceName: "svc", Env: "prod", UserGroupID: 3},
		{ServiceName: "other", Env: "", UserGroupID: 9},
	}
	groups := serviceteam.GroupIDSet([]int64{3})

	if serviceteam.CanSee(false, groups, bindings, "svc", "") {
		t.Fatal("env=\"\" should not match an env-scoped binding")
	}
	if !CanSeeAnyEnv(false, groups, bindings, "svc") {
		t.Fatal("any-env OR should see svc")
	}
	if CanSeeAnyEnv(false, groups, bindings, "other") {
		t.Fatal("other belongs to a foreign group")
	}
	if !CanSeeAnyEnv(true, nil, nil, "anything") {
		t.Fatal("viewAll sees everything")
	}
}
