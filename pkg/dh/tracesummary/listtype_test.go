package tracesummary

import "testing"

// resolveRootType 在前端是「单 span 便捷入口」：ResolveTraceKind 传一个 span。
func resolveRootType(tags []Tag, operationName string) ListType {
	return ResolveTraceKind([]KindSpan{{Tags: tags, OperationName: operationName}})
}

// 断言逐条来自 pharos-fe src/dh/trace/listType.test.ts 的 resolveRootType 用例。
func TestResolveRootTypeFromTags(t *testing.T) {
	cases := []struct {
		name      string
		tags      []Tag
		operation string
		want      ListType
	}{
		{"http.method -> web", []Tag{{"http.method", "GET"}}, "", TypeWeb},
		{"http.route -> web", []Tag{{"http.route", "/orders"}}, "", TypeWeb},
		{"nacos url with http tags -> nacos", []Tag{
			{"url.full", "http://172.22.0.27:8848/nacos/v1/cs/configs/listener"},
			{"http.request.method", "POST"},
		}, "", TypeNacos},
		{"clickhouse -> sql", []Tag{{"db.system", "clickhouse"}}, "", TypeSQL},
		{"mysql -> sql", []Tag{{"db.system", "mysql"}}, "", TypeSQL},
		{"db.type PostgreSQL -> sql", []Tag{{"db.type", "PostgreSQL"}}, "", TypeSQL},
		{"redis -> redis", []Tag{{"db.system", "redis"}}, "", TypeRedis},
		{"memcached -> cache", []Tag{{"db.type", "memcached"}}, "", TypeCache},
		{"layer Cache -> cache", []Tag{{"layer", "Cache"}}, "", TypeCache},
		{"layer HTTP -> web", []Tag{{"layer", "HTTP"}, {"component", "Tomcat"}}, "", TypeWeb},
		{"layer Database -> sql", []Tag{{"layer", "Database"}, {"component", "Mysql"}}, "", TypeSQL},
		{"messaging.system kafka -> mq", []Tag{{"messaging.system", "kafka"}}, "", TypeMQ},
		{"messaging.system rabbitmq -> mq", []Tag{{"messaging.system", "rabbitmq"}}, "", TypeMQ},
		{"rpc.system grpc -> rpc", []Tag{{"rpc.system", "grpc"}}, "", TypeRPC},
		{"layer MQ -> mq", []Tag{{"layer", "MQ"}}, "", TypeMQ},
		{"layer RPCFramework -> rpc", []Tag{{"layer", "RPCFramework"}}, "", TypeRPC},
		{"rabbitmq tags win over process operation", []Tag{{"messaging.system", "rabbitmq"}}, "process", TypeMQ},
		{"span.kind consumer + messaging.* -> mq", []Tag{
			{"span.kind", "consumer"},
			{"messaging.operation", "process"},
		}, "", TypeMQ},
		{"span.kind consumer alone -> empty", []Tag{{"span.kind", "consumer"}}, "", TypeUnknown},
		{"span.kind producer alone -> empty", []Tag{{"span.kind", "producer"}}, "", TypeUnknown},
		{"span.kind server -> empty", []Tag{{"span.kind", "server"}}, "", TypeUnknown},
		{"lone component tag -> empty", []Tag{{"component", "Mysql"}}, "", TypeUnknown},
		{"nil tags -> empty", nil, "", TypeUnknown},
		{"empty tags -> empty", []Tag{}, "", TypeUnknown},
		{"tags empty, operation GET /orders -> web", nil, "GET /orders", TypeWeb},
		{"tags empty, operation SELECT 1 -> sql", []Tag{}, "SELECT 1", TypeSQL},
		{"bare POST no tags -> web (not nacos)", nil, "POST", TypeWeb},
		{"bare POST empty tags -> web", []Tag{}, "POST", TypeWeb},
		{"clickhouse wins over POST", []Tag{{"db.system", "clickhouse"}}, "POST", TypeSQL},
		{"clickhouse wins over GET /query", []Tag{{"db.system", "clickhouse"}}, "GET /query", TypeSQL},
		{"clickhouse wins over GET", []Tag{{"db.system", "clickhouse"}}, "GET", TypeSQL},
		{"no tags, process -> empty", nil, "process", TypeUnknown},
		{"empty tags, process -> empty", []Tag{}, "process", TypeUnknown},
		{"no tags, consume -> empty", nil, "consume", TypeUnknown},
		{"no tags, GET -> web", nil, "GET", TypeWeb},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRootType(tc.tags, tc.operation); got != tc.want {
				t.Fatalf("resolveRootType = %q, want %q", got, tc.want)
			}
		})
	}
}

// 断言逐条来自 pharos-fe src/dh/trace/listType.test.ts 的 resolveFromOperation 用例。
func TestResolveFromOperation(t *testing.T) {
	cases := map[string]ListType{
		"GET /":                    TypeWeb,
		"GET /orders":              TypeWeb,
		"POST /api":                TypeWeb,
		"HTTP GET":                 TypeWeb,
		"GET:/users":               TypeWeb,
		"{GET}/users":              TypeWeb,
		"GET":                      TypeWeb,
		"POST":                     TypeWeb,
		"PUT":                      TypeWeb,
		"PATCH":                    TypeWeb,
		"DELETE":                   TypeWeb,
		"HEAD":                     TypeWeb,
		"OPTIONS":                  TypeWeb,
		"SELECT 1":                 TypeSQL,
		"INSERT INTO t VALUES (1)": TypeSQL,
		"UPDATE t SET x = 1":       TypeSQL,
		"DELETE FROM orders":       TypeSQL,
		"DELETE users":             TypeSQL,
		"DELETE /orders":           TypeWeb,
		"PING":                     TypeRedis,
		"INFO":                     TypeRedis,
		"SET":                      TypeRedis,
		"HGET":                     TypeRedis,
		"Mysql/query":              TypeUnknown,
		"unknown":                  TypeUnknown,
		"":                         TypeUnknown,
		"AcmeApplicationListener.onApplicationEvent": TypeUnknown,
		"ClientWatch.run": TypeUnknown,
		"process":         TypeUnknown,
		"consume":         TypeUnknown,
	}

	for operation, want := range cases {
		if got := ResolveFromOperation(operation); got != want {
			t.Fatalf("ResolveFromOperation(%q) = %q, want %q", operation, got, want)
		}
	}
}

// 断言逐条来自 pharos-fe src/dh/trace/listType.test.ts 的 isInternalFrame 用例。
func TestIsInternalFrame(t *testing.T) {
	generated := []string{
		"Worker$$Lambda.run",
		"Worker$$Lambda$123/0x0000000800c0a000",
		"Foo$$EnhancerBySpringCGLIB$$abc.intercept",
		"$Proxy12",
		"Foo$lambda$0$invoke",
	}
	for _, name := range generated {
		if !IsInternalFrame(name) {
			t.Fatalf("IsInternalFrame(%q) = false, want true", name)
		}
	}

	ordinary := []string{"GET", "AcmeApplicationListener.onApplicationEvent", "ClientWatch.run", ""}
	for _, name := range ordinary {
		if IsInternalFrame(name) {
			t.Fatalf("IsInternalFrame(%q) = true, want false", name)
		}
	}
}

// 断言逐条来自 pharos-fe src/dh/trace/listType.test.ts 的 resolveTraceKind 用例。
func TestResolveTraceKindWholeTrace(t *testing.T) {
	httpChild := ResolveTraceKind([]KindSpan{
		{SpanID: "root", StartTime: 1, OperationName: "Scheduler$$Lambda.run"},
		{SpanID: "child", StartTime: 2, OperationName: "POST", Tags: []Tag{
			{"http.request.method", "POST"},
			{"url.full", "http://svc.internal/instances"},
			{"http.response.status_code", "201"},
		}, Parents: []SpanRef{{SpanID: "root"}}},
	})
	if httpChild != TypeWeb {
		t.Fatalf("http child = %q, want web", httpChild)
	}

	nacosChild := ResolveTraceKind([]KindSpan{
		{SpanID: "root", StartTime: 1, OperationName: "Watch$$Lambda.run"},
		{SpanID: "child", StartTime: 2, OperationName: "GET", Tags: []Tag{
			{"url.full", "http://172.22.0.27:8848/nacos/v1/ns/instance/list"},
			{"http.request.method", "GET"},
			{"thread.name", "com.alibaba.nacos.client.naming.updater"},
		}, Parents: []SpanRef{{SpanID: "root"}}},
	})
	if nacosChild != TypeNacos {
		t.Fatalf("nacos child = %q, want nacos", nacosChild)
	}

	if got := ResolveTraceKind([]KindSpan{{OperationName: "Worker$$Lambda.run"}}); got != TypeInternal {
		t.Fatalf("generated root without children = %q, want internal", got)
	}
	if got := ResolveTraceKind([]KindSpan{{OperationName: "GET"}}); got != TypeWeb {
		t.Fatalf("bare GET = %q, want web", got)
	}
	if got := ResolveTraceKind([]KindSpan{{Tags: []Tag{{"db.system", "clickhouse"}}}}); got != TypeSQL {
		t.Fatalf("clickhouse = %q, want sql", got)
	}
	if got := ResolveTraceKind(nil); got != TypeUnknown {
		t.Fatalf("no spans = %q, want empty", got)
	}

	rootWins := ResolveTraceKind([]KindSpan{
		{SpanID: "root", StartTime: 1, OperationName: "GET /orders", Tags: []Tag{{"http.method", "GET"}}},
		{SpanID: "child", StartTime: 2, Tags: []Tag{{"db.system", "mysql"}}, Parents: []SpanRef{{SpanID: "root"}}},
	})
	if rootWins != TypeWeb {
		t.Fatalf("root tags win = %q, want web", rootWins)
	}

	rabbit := ResolveTraceKind([]KindSpan{{OperationName: "process", Tags: []Tag{
		{"messaging.system", "rabbitmq"},
		{"messaging.operation", "process"},
		{"span.kind", "consumer"},
	}}})
	if rabbit != TypeMQ {
		t.Fatalf("rabbitmq consumer = %q, want mq", rabbit)
	}
}
