package router

import "testing"

func TestDhIsBlockedTracingProxyPath(t *testing.T) {
	blocked := []string{
		"/api/v3/traces",
		"api/v3/traces",
		"/api/v3/traces/",
		"/api/v3/traces?query.service_name=svc",
		"/api/v3/traces/4bf92f3577b34da6a3ce929d0e0e4736",
		"//api/v3//traces",
		"/API/v3/Traces",
		"/api/v3/trace-summaries",
		"/api/v3/trace-summaries?query.search_depth=20",
	}
	for _, path := range blocked {
		if !dhIsBlockedTracingProxyPath(path) {
			t.Errorf("dhIsBlockedTracingProxyPath(%q) = false, want true", path)
		}
	}

	// 产品明确决定不收口的 tracing 路径，以及所有非 trace 路径。堵掉它们会坏服务下拉、拓扑、
	// span_metrics 回退查询、大盘和告警。
	allowed := []string{
		"/api/v3/services",
		"/api/v3/operations",
		"/api/dependencies",
		"/api/v3/dependencies",
		"/graphql",
		"/api/v1/query",
		"/api/v1/query_range",
		"/api/v1/labels",
		"/_msearch",
		"/",
		"",
		// 前缀相同但不是同一个资源，不能误伤。
		"/api/v3/traces-experimental",
		"/api/v3/trace-summaries-x",
	}
	for _, path := range allowed {
		if dhIsBlockedTracingProxyPath(path) {
			t.Errorf("dhIsBlockedTracingProxyPath(%q) = true, want false", path)
		}
	}
}
