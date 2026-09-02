package tracesummary

import (
	"testing"

	"github.com/ccfos/nightingale/v6/pkg/dh/tracefetch"
)

// 查询侧（tracefetch）用这个键收窄，摘要侧用它填环境列。两个包各写一份常量（本包是纯转换层，
// 不引 HTTP 客户端包），所以在测试里钉死两者相等，防止只改了一侧导致「过滤生效但列是空的」。
func TestEnvAttrMatchesQuerySide(t *testing.T) {
	if envAttr != tracefetch.EnvAttributeKey {
		t.Fatalf("envAttr = %q, tracefetch.EnvAttributeKey = %q，两侧必须一致", envAttr, tracefetch.EnvAttributeKey)
	}
}

// env 是 resource 级属性，所以同一条 trace 的所有 span 正常共享一个值。
const envTraceBody = `{
  "result": {
    "resourceSpans": [
      {
        "resource": {
          "attributes": [
            { "key": "service.name", "value": { "stringValue": "gateway" } },
            { "key": "deployment.environment.name", "value": { "stringValue": "test" } }
          ]
        },
        "scopeSpans": [
          {
            "spans": [
              {
                "traceId": "AABB",
                "spanId": "01",
                "name": "GET /orders",
                "kind": 2,
                "startTimeUnixNano": "1700000000000000000",
                "endTimeUnixNano": "1700000000900000000"
              }
            ]
          }
        ]
      },
      {
        "resource": {
          "attributes": [
            { "key": "service.name", "value": { "stringValue": "order" } },
            { "key": "deployment.environment.name", "value": { "stringValue": "test" } }
          ]
        },
        "scopeSpans": [
          {
            "spans": [
              {
                "traceId": "aabb",
                "spanId": "02",
                "parentSpanId": "01",
                "name": "list",
                "startTimeUnixNano": "1700000000100000000",
                "endTimeUnixNano": "1700000000300000000"
              }
            ]
          }
        ]
      }
    ]
  }
}`

func TestDecodeTracesCarriesResourceEnvOntoSpans(t *testing.T) {
	traces, err := DecodeTraces([]byte(envTraceBody))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("len(traces) = %d, want 1", len(traces))
	}
	for _, span := range traces[0].Spans {
		if span.Env != "test" {
			t.Errorf("span %s Env = %q, want test", span.SpanID, span.Env)
		}
	}
}

func TestSummarizeAggregatesSingleEnv(t *testing.T) {
	traces, err := DecodeTraces([]byte(envTraceBody))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	summary := Summarize(traces[0])
	if summary == nil {
		t.Fatal("Summarize() = nil")
	}
	if len(summary.Envs) != 1 || summary.Envs[0] != "test" {
		t.Fatalf("Envs = %v, want [test]", summary.Envs)
	}
}

// 跨环境 trace 是兜底分支（服务间调用不跨环境，DB 不上报 span），但真出现时列表要能如实标注，
// 所以按首次出现顺序保留全部取值，不折成单值也不静默丢弃。
func TestSummarizeDeduplicatesAndKeepsMultipleEnvs(t *testing.T) {
	trace := Trace{
		TraceID: "aabb",
		Spans: []Span{
			{SpanID: "01", TraceID: "aabb", Service: "gateway", Env: "test", StartTime: 100, Duration: 50},
			{SpanID: "02", TraceID: "aabb", Service: "gateway", Env: "test", StartTime: 110, Duration: 10, Parents: []SpanRef{{SpanID: "01", TraceID: "aabb"}}},
			{SpanID: "03", TraceID: "aabb", Service: "order", Env: "pre", StartTime: 120, Duration: 10, Parents: []SpanRef{{SpanID: "01", TraceID: "aabb"}}},
		},
	}

	summary := Summarize(trace)
	if summary == nil {
		t.Fatal("Summarize() = nil")
	}
	if len(summary.Envs) != 2 || summary.Envs[0] != "test" || summary.Envs[1] != "pre" {
		t.Fatalf("Envs = %v, want [test pre]（首次出现顺序、已去重）", summary.Envs)
	}
}

// 大量服务尚未注入该属性（上报侧才刚铺开），空串不能变成一个名为 "" 的环境。
func TestSummarizeSkipsMissingEnv(t *testing.T) {
	trace := Trace{
		TraceID: "aabb",
		Spans: []Span{
			{SpanID: "01", TraceID: "aabb", Service: "gateway", StartTime: 100, Duration: 50},
			{SpanID: "02", TraceID: "aabb", Service: "order", Env: "test", StartTime: 110, Duration: 10, Parents: []SpanRef{{SpanID: "01", TraceID: "aabb"}}},
		},
	}

	summary := Summarize(trace)
	if summary == nil {
		t.Fatal("Summarize() = nil")
	}
	if len(summary.Envs) != 1 || summary.Envs[0] != "test" {
		t.Fatalf("Envs = %v, want [test]（空串不计入）", summary.Envs)
	}
}

// 全都没带环境属性时是空数组而不是 nil：JSON 里要序列化成 []，前端拿到的就是「没有环境信息」，
// 而不是需要额外判空的 null。
func TestSummarizeEnvsIsEmptySliceWhenAbsent(t *testing.T) {
	trace := Trace{
		TraceID: "aabb",
		Spans:   []Span{{SpanID: "01", TraceID: "aabb", Service: "gateway", StartTime: 100, Duration: 50}},
	}

	summary := Summarize(trace)
	if summary == nil {
		t.Fatal("Summarize() = nil")
	}
	if summary.Envs == nil {
		t.Fatal("Envs = nil, want 空数组")
	}
	if len(summary.Envs) != 0 {
		t.Fatalf("Envs = %v, want []", summary.Envs)
	}
}
