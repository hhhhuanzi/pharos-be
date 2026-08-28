package tracefetch

import (
	"testing"
	"time"
)

// 断言与前端 src/dh/trace/adapters/jaeger.ts 的 buildFindTracesParams 对齐（同一批参数名、同样的
// 可选项取舍、attributes 同样是 JSON string map），两侧口径必须一致。
func TestFindTracesParams(t *testing.T) {
	query := FindQuery{
		Service:      "svc-a",
		StartTimeMin: time.UnixMilli(1731000000000),
		StartTimeMax: time.UnixMilli(1731000060000),
		Operation:    "GET /foo",
		DurationMin:  "10ms",
		DurationMax:  "2s",
		NumTraces:    20,
		Attributes:   map[string]string{"http.status_code": "200"},
	}

	values, err := query.findTracesParams()
	if err != nil {
		t.Fatalf("findTracesParams() error = %v", err)
	}

	want := map[string]string{
		"query.service_name": "svc-a",
		// 前端发的是 Date.toISOString()（"2024-11-07T17:20:00.000Z"）；Go 的 RFC3339Nano 会省掉
		// 全零的小数秒，两者是同一时刻的合法 RFC3339Nano 写法，上游 time.Parse 都认。
		"query.start_time_min": "2024-11-07T17:20:00Z",
		"query.start_time_max": "2024-11-07T17:21:00Z",
		"query.operation_name": "GET /foo",
		"query.duration_min":   "10ms",
		"query.duration_max":   "2s",
		"query.num_traces":     "20",
		"query.attributes":     `{"http.status_code":"200"}`,
	}
	if len(values) != len(want) {
		t.Fatalf("param count = %d, want %d (%v)", len(values), len(want), values)
	}
	for key, expected := range want {
		if got := values.Get(key); got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
}

// 未设置的过滤项一律不发：grpc-gateway 对空值的处理不保证与「不传」等价，前端也是同样的取舍。
func TestFindTracesParamsOmitsUnsetFilters(t *testing.T) {
	query := FindQuery{
		Service:      "svc-a",
		StartTimeMin: time.UnixMilli(1731000000000),
		StartTimeMax: time.UnixMilli(1731000060000),
	}

	values, err := query.findTracesParams()
	if err != nil {
		t.Fatalf("findTracesParams() error = %v", err)
	}

	for _, key := range []string{"query.operation_name", "query.duration_min", "query.duration_max", "query.num_traces", "query.attributes"} {
		if _, ok := values[key]; ok {
			t.Errorf("%s should be omitted, got %q", key, values.Get(key))
		}
	}
}

func TestFindTracesRequiresService(t *testing.T) {
	cli := &JaegerClient{}
	if _, err := cli.FindTraces(nil, FindQuery{}); err == nil {
		t.Fatal("FindTraces() without a service should fail: authorization is based on that parameter")
	}
}
