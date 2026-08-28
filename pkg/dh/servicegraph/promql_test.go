package servicegraph

import (
	"math"
	"strings"
	"testing"
)

// 断言与前端 src/dh/trace/dependencies/query.test.ts 对齐，两侧口径必须一致。

func TestToPromRange(t *testing.T) {
	cases := map[int64]string{
		86400: "1d",
		3600:  "1h",
		7200:  "2h",
		90:    "90s",
		60:    "1m",
		0:     "1s",
		-5:    "1s",
	}
	for seconds, want := range cases {
		if got := ToPromRange(seconds); got != want {
			t.Fatalf("ToPromRange(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestEscapePromLabel(t *testing.T) {
	if got := EscapePromLabel(`a"b\c`); got != `a\"b\\c` {
		t.Fatalf("EscapePromLabel = %q", got)
	}
}

func TestBuildQueriesGlobal(t *testing.T) {
	q := BuildQueries("1h", Scope{})
	if q.Total != "sum by (client, server, connection_type) (increase(traces_service_graph_request_total[1h]))" {
		t.Fatalf("total = %q", q.Total)
	}
	if !strings.Contains(q.Failed, "traces_service_graph_request_failed_total[1h]") {
		t.Fatalf("failed = %q", q.Failed)
	}
	if !strings.Contains(q.P95, "histogram_quantile(0.95") ||
		!strings.Contains(q.P95, "sum by (client, server, connection_type, le) (rate(traces_service_graph_request_server_seconds_bucket[1h]))") {
		t.Fatalf("p95 = %q", q.P95)
	}
}

func TestBuildQueriesScoped(t *testing.T) {
	q := BuildQueries("1h", Scope{Service: "turms-business-service", Env: "pre", Cluster: "k8s-trade-prod", Namespace: "pre-turms"})
	for _, want := range []string{
		`client="turms-business-service", client_deployment_environment_name="pre"`,
		`server="turms-business-service", server_deployment_environment_name="pre"`,
		`client_k8s_cluster_name="k8s-trade-prod"`,
		`client_k8s_namespace_name="pre-turms"`,
		" or ",
	} {
		if !strings.Contains(q.Total, want) {
			t.Fatalf("total %q missing %q", q.Total, want)
		}
	}
	if strings.Contains(q.Total, `env="pre"`) || strings.Contains(q.P95, `cluster="k8s-trade-prod"`) {
		t.Fatalf("scrape labels leaked: total=%q p95=%q", q.Total, q.P95)
	}
	if !strings.Contains(q.P95, `server_k8s_cluster_name="k8s-trade-prod"`) {
		t.Fatalf("p95 = %q", q.P95)
	}
	// or 两侧的 sum by 子句必须一致，否则 histogram_quantile 拿不到 le。
	if sides := strings.Split(q.P95, " or "); len(sides) != 2 ||
		!strings.Contains(sides[0], "sum by (client, server, connection_type, le)") ||
		!strings.Contains(sides[1], "sum by (client, server, connection_type, le)") {
		t.Fatalf("p95 = %q", q.P95)
	}
}

func TestSpanmetricsPeerQuery(t *testing.T) {
	if got := SpanmetricsPeerQuery("1h", Scope{Service: "svc"}); got != "" {
		t.Fatalf("want empty without env, got %q", got)
	}
	got := SpanmetricsPeerQuery("1h", Scope{Service: "svc", Env: "pre"})
	want := `sum by (span_name) (increase(traces_span_metrics_calls_total{service_name="svc", deployment_environment_name="pre", span_kind="SPAN_KIND_CLIENT"}[1h]))`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func sample(labels map[string]string, value float64) Sample {
	return Sample{Labels: labels, Value: value}
}

func TestMergeVectorsJoinsOnEdgeKey(t *testing.T) {
	edges := MergeVectors(
		[]Sample{sample(map[string]string{"client": "gateway", "server": "order", "connection_type": ""}, 200)},
		[]Sample{sample(map[string]string{"client": "gateway", "server": "order"}, 10)},
		[]Sample{sample(map[string]string{"client": "gateway", "server": "order"}, 0.25)},
	)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v", edges)
	}
	got := edges[0]
	if got.Client != "gateway" || got.Server != "order" || got.ConnectionType != "" ||
		got.RequestCount != 200 || got.FailedCount != 10 || got.ErrorRate != 0.05 ||
		got.P95Seconds == nil || *got.P95Seconds != 0.25 {
		t.Fatalf("edge = %+v", got)
	}
}

func TestMergeVectorsDropsZeroTrafficAndIncompleteLabels(t *testing.T) {
	edges := MergeVectors(
		[]Sample{
			sample(map[string]string{"client": "a", "server": "b", "connection_type": "database"}, 4),
			sample(map[string]string{"client": "a", "server": "b", "connection_type": ""}, 0),
			sample(map[string]string{"client": "", "server": "orphan"}, 9),
		},
		[]Sample{sample(map[string]string{"client": "ghost", "server": "gone"}, 0)},
		[]Sample{sample(map[string]string{"client": "only", "server": "p95"}, 0.1)},
	)
	if len(edges) != 1 || edges[0].ConnectionType != "database" || edges[0].RequestCount != 4 {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestMergeVectorsCapsErrorRateAndSkipsNaN(t *testing.T) {
	edges := MergeVectors(nil, []Sample{sample(map[string]string{"client": "a", "server": "b"}, 3)}, nil)
	if len(edges) != 1 || edges[0].RequestCount != 0 || edges[0].FailedCount != 3 || edges[0].ErrorRate != 0 {
		t.Fatalf("edges = %+v", edges)
	}

	nan := MergeVectors(
		[]Sample{sample(map[string]string{"client": "a", "server": "b"}, 10)},
		nil,
		[]Sample{sample(map[string]string{"client": "a", "server": "b"}, math.NaN())},
	)
	if len(nan) != 1 || nan[0].P95Seconds != nil {
		t.Fatalf("edges = %+v", nan)
	}
}

func TestMergeVectorsSortsByErrorRateThenP95ThenRequests(t *testing.T) {
	edges := MergeVectors(
		[]Sample{
			sample(map[string]string{"client": "a", "server": "low"}, 100),
			sample(map[string]string{"client": "a", "server": "high"}, 100),
			sample(map[string]string{"client": "a", "server": "slow"}, 100),
		},
		[]Sample{
			sample(map[string]string{"client": "a", "server": "high"}, 50),
		},
		[]Sample{
			sample(map[string]string{"client": "a", "server": "slow"}, 2),
			sample(map[string]string{"client": "a", "server": "low"}, 1),
		},
	)
	want := []string{"high", "slow", "low"}
	for i, name := range want {
		if edges[i].Server != name {
			t.Fatalf("order = %+v, want %v", edges, want)
		}
	}
}