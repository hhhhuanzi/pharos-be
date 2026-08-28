package tracesummary

import "testing"

// FindTraces 把所有命中 trace 的 resourceSpans 拍平进同一个响应，归组必须按 span 上的 traceId 做。
// 这与前端 otlpTracesDataToJaegerResponses 的注释是同一条约束。
const findTracesBody = `{
  "result": {
    "resourceSpans": [
      {
        "resource": { "attributes": [{ "key": "service.name", "value": { "stringValue": "gateway" } }] },
        "scopeSpans": [
          {
            "scope": { "name": "io.opentelemetry.tomcat", "version": "1.0" },
            "spans": [
              {
                "traceId": "AABB",
                "spanId": "01",
                "name": "GET /orders",
                "kind": 2,
                "startTimeUnixNano": "1700000000000000000",
                "endTimeUnixNano": "1700000000900000000",
                "attributes": [
                  { "key": "http.method", "value": { "stringValue": "GET" } },
                  { "key": "http.route", "value": { "stringValue": "/orders" } },
                  { "key": "http.status_code", "value": { "intValue": "200" } }
                ]
              },
              {
                "traceId": "CCDD",
                "spanId": "11",
                "name": "SELECT 1",
                "startTimeUnixNano": "1700000001000000000",
                "endTimeUnixNano": "1700000001100000000",
                "status": { "code": 2, "message": "boom" }
              }
            ]
          }
        ]
      },
      {
        "resource": { "attributes": [{ "key": "service.name", "value": { "stringValue": "order" } }] },
        "scopeSpans": [
          {
            "spans": [
              {
                "traceId": "aabb",
                "spanId": "02",
                "parentSpanId": "01",
                "name": "list",
                "startTimeUnixNano": "1700000000100000000",
                "endTimeUnixNano": "1700000000300000000",
                "attributes": [{ "key": "db.system", "value": { "stringValue": "mysql" } }]
              }
            ]
          }
        ]
      }
    ]
  }
}`

func TestDecodeTracesGroupsBySpanTraceID(t *testing.T) {
	traces, err := DecodeTraces([]byte(findTracesBody))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("len(traces) = %d, want 2", len(traces))
	}
	if traces[0].TraceID != "aabb" || traces[1].TraceID != "ccdd" {
		t.Fatalf("trace ids = %q/%q, want aabb/ccdd (first-appearance order, lowercased)", traces[0].TraceID, traces[1].TraceID)
	}
	if len(traces[0].Spans) != 2 {
		t.Fatalf("aabb spans = %d, want 2", len(traces[0].Spans))
	}

	summaries := SummarizeAll(traces)
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}

	web := summaries[0]
	if web.TraceID != "aabb" || web.RootType != "web" || web.RootInterface != "GET /orders" {
		t.Fatalf("web summary = %+v", web)
	}
	if web.RootService != "gateway" || web.SpanCount != 2 || web.ErrorSpanCount != 0 || web.OrphanSpanCount != 0 {
		t.Fatalf("web summary counts = %+v", web)
	}
	if web.StartTimeUs != 1700000000000000 || web.DurationUs != 900000 {
		t.Fatalf("web times = %d/%d", web.StartTimeUs, web.DurationUs)
	}
	wantServices := []ServiceSummary{{Name: "gateway", SpanCount: 1}, {Name: "order", SpanCount: 1}}
	if len(web.Services) != 2 || web.Services[0] != wantServices[0] || web.Services[1] != wantServices[1] {
		t.Fatalf("web services = %+v, want %+v", web.Services, wantServices)
	}

	// status.code == 2 会被合成 error 属性，与前端 otlpTracesDataToJaegerResponses 一致。
	sql := summaries[1]
	if sql.ErrorSpanCount != 1 {
		t.Fatalf("sql ErrorSpanCount = %d, want 1", sql.ErrorSpanCount)
	}
	if sql.RootType != "sql" {
		t.Fatalf("sql RootType = %q, want sql", sql.RootType)
	}
}

func TestDecodeTracesHandlesStreamedChunksAndBareTracesData(t *testing.T) {
	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"a"}}]},` +
		`"scopeSpans":[{"spans":[{"traceId":"01","spanId":"aa","name":"GET","startTimeUnixNano":"1000000","endTimeUnixNano":"2000000"}]}]}]}
{"result":{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"b"}}]},` +
		`"scopeSpans":[{"spans":[{"traceId":"02","spanId":"bb","name":"POST","startTimeUnixNano":"3000000","endTimeUnixNano":"4000000"}]}]}]}}`

	traces, err := DecodeTraces([]byte(body))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("len(traces) = %d, want 2", len(traces))
	}
}

func TestDecodeTracesEmptyBody(t *testing.T) {
	traces, err := DecodeTraces([]byte("   "))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	if len(traces) != 0 {
		t.Fatalf("len(traces) = %d, want 0", len(traces))
	}
}

func TestDecodeTracesMissingServiceNameFallsBackToUnknown(t *testing.T) {
	body := `{"result":{"resourceSpans":[{"scopeSpans":[{"spans":[` +
		`{"traceId":"01","spanId":"aa","name":"GET","startTimeUnixNano":"1000000","endTimeUnixNano":"2000000"}]}]}]}}`
	traces, err := DecodeTraces([]byte(body))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	summaries := SummarizeAll(traces)
	if len(summaries) != 1 || summaries[0].RootService != "unknown" {
		t.Fatalf("summaries = %+v, want rootService unknown", summaries)
	}
}

func TestDecodeTracesSkipsZeroParentSpanID(t *testing.T) {
	body := `{"result":{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"a"}}]},` +
		`"scopeSpans":[{"spans":[{"traceId":"01","spanId":"aa","parentSpanId":"0000000000000000","name":"GET",` +
		`"startTimeUnixNano":"1000000","endTimeUnixNano":"2000000"}]}]}]}}`
	traces, err := DecodeTraces([]byte(body))
	if err != nil {
		t.Fatalf("DecodeTraces: %v", err)
	}
	summaries := SummarizeAll(traces)
	if len(summaries) != 1 || summaries[0].OrphanSpanCount != 0 {
		t.Fatalf("summaries = %+v, want orphanSpanCount 0", summaries)
	}
}

func TestAnyValueToString(t *testing.T) {
	str := "s"
	yes := true
	num := 1.5
	whole := float64(1)
	cases := []struct {
		name  string
		value otlpAnyValue
		want  string
	}{
		{"string", otlpAnyValue{StringValue: &str}, "s"},
		{"bool", otlpAnyValue{BoolValue: &yes}, "true"},
		{"int as string", otlpAnyValue{IntValue: []byte(`"200"`)}, "200"},
		{"int as number", otlpAnyValue{IntValue: []byte(`200`)}, "200"},
		{"double", otlpAnyValue{DoubleValue: &num}, "1.5"},
		{"whole double drops the fraction like String()", otlpAnyValue{DoubleValue: &whole}, "1"},
		{"empty", otlpAnyValue{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value
			if got := anyValueToString(&value); got != tc.want {
				t.Fatalf("anyValueToString = %q, want %q", got, tc.want)
			}
		})
	}
}
