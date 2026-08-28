package tracesummary

import (
	"reflect"
	"testing"
)

func span(spanID, service string, startTime, duration int64, operation string, tags []Tag, parents ...string) Span {
	refs := make([]SpanRef, 0, len(parents))
	for _, parent := range parents {
		refs = append(refs, SpanRef{SpanID: parent, TraceID: "abc"})
	}
	if operation == "" {
		operation = "op"
	}
	return Span{
		SpanID:    spanID,
		TraceID:   "abc",
		Service:   service,
		Operation: operation,
		StartTime: startTime,
		Duration:  duration,
		Tags:      tags,
		Parents:   refs,
	}
}

// 断言逐条来自 pharos-fe src/dh/trace/contract.test.ts 的 traceResponseToSummary 用例。
func TestSummarizeRootDurationAndServices(t *testing.T) {
	summary := Summarize(Trace{TraceID: "ABC", Spans: []Span{
		span("child2", "order", 1200, 500, "", nil, "root"),
		span("root", "gateway", 1000, 900, "GET /orders", nil),
		span("child1", "order", 1100, 200, "", nil, "root"),
	}})
	if summary == nil {
		t.Fatal("Summarize returned nil")
	}

	if summary.TraceID != "abc" {
		t.Errorf("TraceID = %q, want abc (lowercased)", summary.TraceID)
	}
	if summary.RootService != "gateway" {
		t.Errorf("RootService = %q", summary.RootService)
	}
	if summary.RootOperation != "GET /orders" {
		t.Errorf("RootOperation = %q", summary.RootOperation)
	}
	if summary.RootInterface != "GET /orders" {
		t.Errorf("RootInterface = %q", summary.RootInterface)
	}
	if summary.RootType != "web" {
		t.Errorf("RootType = %q", summary.RootType)
	}
	if summary.StartTimeUs != 1000 || summary.DurationUs != 900 {
		t.Errorf("start/duration = %d/%d, want 1000/900", summary.StartTimeUs, summary.DurationUs)
	}
	if summary.SpanCount != 3 || summary.ErrorSpanCount != 0 || summary.OrphanSpanCount != 0 {
		t.Errorf("counts = %d/%d/%d", summary.SpanCount, summary.ErrorSpanCount, summary.OrphanSpanCount)
	}

	// 按 span 数降序，最忙的服务在前。
	want := []ServiceSummary{
		{Name: "order", SpanCount: 2},
		{Name: "gateway", SpanCount: 1},
	}
	if !reflect.DeepEqual(summary.Services, want) {
		t.Errorf("Services = %+v, want %+v", summary.Services, want)
	}
}

func TestSummarizeErrorSpans(t *testing.T) {
	summary := Summarize(Trace{TraceID: "abc", Spans: []Span{
		span("root", "gateway", 1000, 100, "", nil),
		span("ok", "order", 1010, 10, "", []Tag{{"error", "false"}}),
		span("bad", "order", 1020, 10, "", []Tag{{"error", "true"}}),
	}})
	if summary == nil {
		t.Fatal("Summarize returned nil")
	}
	if summary.ErrorSpanCount != 1 {
		t.Errorf("ErrorSpanCount = %d, want 1", summary.ErrorSpanCount)
	}
	var order *ServiceSummary
	for i := range summary.Services {
		if summary.Services[i].Name == "order" {
			order = &summary.Services[i]
		}
	}
	if order == nil || order.SpanCount != 2 || order.ErrorSpanCount != 1 {
		t.Errorf("order service = %+v, want spanCount 2 / errorSpanCount 1", order)
	}
}

func TestSummarizeOrphansStillFindRoot(t *testing.T) {
	summary := Summarize(Trace{TraceID: "abc", Spans: []Span{
		span("a", "gateway", 2000, 100, "", nil, "gone"),
		span("b", "gateway", 1500, 100, "earliest", nil, "also-gone"),
	}})
	if summary == nil {
		t.Fatal("Summarize returned nil")
	}
	if summary.OrphanSpanCount != 2 {
		t.Errorf("OrphanSpanCount = %d, want 2", summary.OrphanSpanCount)
	}
	if summary.RootOperation != "earliest" {
		t.Errorf("RootOperation = %q, want earliest", summary.RootOperation)
	}
}

func TestSummarizeReturnsNilWithoutUsableSpans(t *testing.T) {
	if got := Summarize(Trace{TraceID: "abc"}); got != nil {
		t.Errorf("no spans -> %+v, want nil", got)
	}
	// startTime 为 0 的 span 与前端一样被过滤掉。
	if got := Summarize(Trace{TraceID: "abc", Spans: []Span{span("a", "gateway", 0, 10, "", nil)}}); got != nil {
		t.Errorf("zero startTime -> %+v, want nil", got)
	}
	if got := Summarize(Trace{Spans: []Span{span("a", "gateway", 10, 10, "", nil)}}); got != nil {
		t.Errorf("no traceID -> %+v, want nil", got)
	}
}

func TestSummarizeRootInterfaceAndTypeFromTags(t *testing.T) {
	cases := []struct {
		name          string
		spans         []Span
		rootInterface string
		rootType      string
		rootOperation string
	}{
		{
			name: "http",
			spans: []Span{span("root", "gateway", 1000, 100, "HTTP GET", []Tag{
				{"http.method", "GET"},
				{"http.route", "/orders"},
			})},
			rootInterface: "GET /orders", rootType: "web", rootOperation: "HTTP GET",
		},
		{
			name: "db",
			spans: []Span{span("root", "gateway", 1000, 100, "Mysql/query", []Tag{
				{"db.system", "mysql"},
				{"db.statement", "SELECT 1"},
			})},
			rootInterface: "SELECT 1", rootType: "sql", rootOperation: "Mysql/query",
		},
		{
			name:          "clickhouse",
			spans:         []Span{span("root", "gateway", 1000, 100, "query", []Tag{{"db.system", "clickhouse"}})},
			rootInterface: "query", rootType: "sql", rootOperation: "query",
		},
		{
			name: "nacos",
			spans: []Span{span("root", "gateway", 1000, 100, "POST", []Tag{
				{"url.full", "http://172.22.0.27:8848/nacos/v1/cs/configs/listener"},
				{"http.request.method", "POST"},
			})},
			rootInterface: "POST /nacos/v1/cs/configs/listener", rootType: "nacos", rootOperation: "POST",
		},
		{
			name:          "opaque",
			spans:         []Span{span("root", "gateway", 1000, 100, "internal-op", nil)},
			rootInterface: "internal-op", rootType: "", rootOperation: "internal-op",
		},
		{
			name: "rabbitmq",
			spans: []Span{span("root", "gateway", 1000, 100, "process", []Tag{
				{"messaging.system", "rabbitmq"},
				{"messaging.operation", "process"},
				{"span.kind", "consumer"},
			})},
			rootInterface: "process", rootType: "mq", rootOperation: "process",
		},
		{
			name:          "bare process is not mq",
			spans:         []Span{span("root", "gateway", 1000, 100, "process", nil)},
			rootInterface: "process", rootType: "", rootOperation: "process",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := Summarize(Trace{TraceID: "abc", Spans: tc.spans})
			if summary == nil {
				t.Fatal("Summarize returned nil")
			}
			if summary.RootInterface != tc.rootInterface {
				t.Errorf("RootInterface = %q, want %q", summary.RootInterface, tc.rootInterface)
			}
			if summary.RootType != tc.rootType {
				t.Errorf("RootType = %q, want %q", summary.RootType, tc.rootType)
			}
			if summary.RootOperation != tc.rootOperation {
				t.Errorf("RootOperation = %q, want %q", summary.RootOperation, tc.rootOperation)
			}
		})
	}
}

func TestSummarizeUsesChildTagsWhenRootIsGeneratedFrame(t *testing.T) {
	http := Summarize(Trace{TraceID: "abc", Spans: []Span{
		span("root", "gateway", 1000, 100, "Scheduler$$Lambda.run", nil),
		span("child", "order", 1010, 80, "POST", []Tag{
			{"http.request.method", "POST"},
			{"url.full", "http://svc.internal/instances"},
			{"http.response.status_code", "201"},
		}, "root"),
	}})
	if http == nil || http.RootType != "web" || http.RootOperation != "Scheduler$$Lambda.run" {
		t.Fatalf("generated root + http child = %+v", http)
	}

	nacos := Summarize(Trace{TraceID: "abc", Spans: []Span{
		span("root", "gateway", 1000, 100, "Watch$$Lambda.run", nil),
		span("child", "order", 1010, 80, "GET", []Tag{
			{"url.full", "http://172.22.0.27:8848/nacos/v1/ns/instance/list"},
			{"http.request.method", "GET"},
		}, "root"),
	}})
	if nacos == nil || nacos.RootType != "nacos" || nacos.RootOperation != "Watch$$Lambda.run" {
		t.Fatalf("generated root + nacos child = %+v", nacos)
	}
}
