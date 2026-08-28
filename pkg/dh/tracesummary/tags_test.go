package tracesummary

import "testing"

// 断言逐条来自 pharos-fe src/dh/trace/spanSemantics.test.ts 的 resolveSpanKind 用例。
func TestResolveSpanKind(t *testing.T) {
	cases := []struct {
		name string
		tags []Tag
		want SpanKind
	}{
		{"clickhouse db.system -> db", []Tag{{"db.system", "clickhouse"}}, KindDB},
		{"redis db.system -> cache", []Tag{{"db.system", "redis"}}, KindCache},
		{"memcached db.type -> cache", []Tag{{"db.type", "memcached"}}, KindCache},
		{"layer Cache -> cache", []Tag{{"layer", "Cache"}}, KindCache},
		{"http url.full without http.* -> web", []Tag{{"url.full", "http://svc.internal/instances"}}, KindWeb},
		{"nacos url -> nacos", []Tag{
			{"url.full", "http://172.22.0.27:8848/nacos/v1/cs/configs/listener"},
			{"http.request.method", "POST"},
			{"http.response.status_code", "200"},
			{"span.kind", "client"},
			{"thread.name", "com.alibaba.nacos.client.Worker.longPolling"},
		}, KindNacos},
		{"thread.name nacos -> nacos", []Tag{{"thread.name", "com.alibaba.nacos.client.Worker.longPolling"}}, KindNacos},
		{"server.port 8848 + http -> nacos", []Tag{
			{"server.port", "8848"},
			{"http.request.method", "POST"},
		}, KindNacos},
		{"host:8848 with nacos openapi path -> nacos", []Tag{
			{"url.full", "http://172.22.0.27:8848/v1/cs/configs/listener"},
			{"http.request.method", "POST"},
		}, KindNacos},
		{"generic /v1/cs without nacos host -> web", []Tag{
			{"http.target", "/v1/cs/configs"},
			{"http.request.method", "GET"},
		}, KindWeb},
		{"clickhouse wins over nacos-looking url", []Tag{
			{"db.system", "clickhouse"},
			{"url.full", "http://172.22.0.27:8848/nacos/v1/cs/configs/listener"},
		}, KindDB},
		{"messaging.system kafka -> messaging", []Tag{{"messaging.system", "kafka"}}, KindMessaging},
		{"messaging.system rabbitmq -> messaging", []Tag{{"messaging.system", "rabbitmq"}}, KindMessaging},
		{"rpc.system grpc -> rpc", []Tag{{"rpc.system", "grpc"}}, KindRPC},
		{"layer MQ -> messaging", []Tag{{"layer", "MQ"}}, KindMessaging},
		{"layer RPCFramework -> rpc", []Tag{{"layer", "RPCFramework"}}, KindRPC},
		{"span.kind consumer + messaging.* -> messaging", []Tag{
			{"span.kind", "consumer"},
			{"messaging.operation", "process"},
		}, KindMessaging},
		{"span.kind producer + messaging.* -> messaging", []Tag{
			{"span.kind", "producer"},
			{"messaging.destination.name", "orders"},
		}, KindMessaging},
		{"span.kind consumer alone -> internal", []Tag{{"span.kind", "consumer"}}, KindInternal},
		{"span.kind producer alone -> internal", []Tag{{"span.kind", "producer"}}, KindInternal},
		{"messaging.operation alone -> internal", []Tag{{"messaging.operation", "process"}}, KindInternal},
		{"db wins over http", []Tag{
			{"db.system", "mysql"},
			{"http.method", "GET"},
		}, KindDB},
		{"nil tags -> internal", nil, KindInternal},
		{"empty tags -> internal", []Tag{}, KindInternal},
		{"span.kind server -> internal", []Tag{{"span.kind", "server"}}, KindInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSpanKind(tc.tags); got != tc.want {
				t.Fatalf("ResolveSpanKind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTagValueSkipsBlankAndMissing(t *testing.T) {
	tags := []Tag{{"http.method", "  "}, {"http.request.method", "POST"}}
	if got := TagValue(tags, httpMethodKeys); got != "POST" {
		t.Fatalf("TagValue = %q, want POST", got)
	}
	if got := TagValue(nil, httpMethodKeys); got != "" {
		t.Fatalf("TagValue(nil) = %q, want empty", got)
	}
	if got := TagValue([]Tag{{"http.method", " GET "}}, httpMethodKeys); got != "GET" {
		t.Fatalf("TagValue trims, got %q", got)
	}
}

// 断言逐条来自 pharos-fe src/dh/trace/summaryFields.test.ts。
func TestResolveRootInterface(t *testing.T) {
	got := ResolveRootInterface("HTTP GET", []Tag{
		{"http.method", "GET"},
		{"http.url", "https://api.example.com/orders?id=1"},
	})
	if got != "GET /orders?id=1" {
		t.Fatalf("method+path = %q", got)
	}

	got = ResolveRootInterface("Mysql/query", []Tag{{"db.statement", "SELECT * FROM orders"}})
	if got != "SELECT * FROM orders" {
		t.Fatalf("sql only = %q", got)
	}

	got = ResolveRootInterface("query", []Tag{
		{"http.method", "POST"},
		{"http.target", "/query"},
		{"db.statement", "SELECT 1"},
	})
	if got != "POST /query SELECT 1" {
		t.Fatalf("method+path+sql = %q", got)
	}

	if got = ResolveRootInterface("GET /orders", []Tag{}); got != "GET /orders" {
		t.Fatalf("fallback to operation = %q", got)
	}
	if got = ResolveRootInterface("", nil); got != "" {
		t.Fatalf("no span = %q", got)
	}
}
