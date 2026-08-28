package tracefetch

import "testing"

func assertNames(t *testing.T, got TraceServices, want ...string) {
	t.Helper()
	if len(got.Names) != len(want) {
		t.Fatalf("names = %v, want %v", got.Names, want)
	}
	for i := range want {
		if got.Names[i] != want[i] {
			t.Fatalf("names = %v, want %v", got.Names, want)
		}
	}
}

func TestExtractServicesFromGatewayEnvelope(t *testing.T) {
	body := []byte(`{"result":{"resourceSpans":[
		{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"order"}},{"key":"host.name","value":{"stringValue":"node-1"}}]},
		 "scopeSpans":[{"spans":[{"traceId":"aa","spanId":"bb"}]}]},
		{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"pay"}}]}},
		{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"order"}}]}}
	]}}`)

	got, err := ExtractServices(body)
	if err != nil {
		t.Fatal(err)
	}
	assertNames(t, got, "order", "pay")
	if got.ResourceSpans != 3 {
		t.Fatalf("resourceSpans = %d, want 3", got.ResourceSpans)
	}
}

func TestExtractServicesFromBareTracesData(t *testing.T) {
	body := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"order"}}]}}]}`)

	got, err := ExtractServices(body)
	if err != nil {
		t.Fatal(err)
	}
	assertNames(t, got, "order")
}

// 服务端流式 RPC 的网关会把响应写成多个换行分隔的 JSON 值。
func TestExtractServicesAcrossStreamedChunks(t *testing.T) {
	body := []byte(`{"result":{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"order"}}]}}]}}
{"result":{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"pay"}}]}}]}}`)

	got, err := ExtractServices(body)
	if err != nil {
		t.Fatal(err)
	}
	assertNames(t, got, "order", "pay")
	if got.ResourceSpans != 2 {
		t.Fatalf("resourceSpans = %d, want 2", got.ResourceSpans)
	}
}

func TestExtractServicesEmptyResponses(t *testing.T) {
	cases := []string{``, `   `, `{}`, `{"result":{}}`, `{"result":{"resourceSpans":[]}}`}
	for _, body := range cases {
		got, err := ExtractServices([]byte(body))
		if err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(got.Names) != 0 || got.ResourceSpans != 0 {
			t.Fatalf("body %q: got %+v", body, got)
		}
	}
}

// 有数据但都不带 service.name：服务集合为空，但 ResourceSpans > 0，调用方要按「不可见」而不是
// 「不存在」处理。
func TestExtractServicesWithoutServiceNameAttribute(t *testing.T) {
	body := []byte(`{"result":{"resourceSpans":[
		{"resource":{"attributes":[{"key":"host.name","value":{"stringValue":"node-1"}}]}},
		{"resource":{}},
		{"resource":{"attributes":[{"key":"service.name","value":{"intValue":"7"}}]}},
		{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"  "}}]}}
	]}}`)

	got, err := ExtractServices(body)
	if err != nil {
		t.Fatal(err)
	}
	assertNames(t, got)
	if got.ResourceSpans != 4 {
		t.Fatalf("resourceSpans = %d, want 4", got.ResourceSpans)
	}
}

func TestExtractServicesRejectsMalformedJSON(t *testing.T) {
	if _, err := ExtractServices([]byte(`{"result":`)); err == nil {
		t.Fatal("want error")
	}
}

func TestIsValidTraceID(t *testing.T) {
	cases := map[string]bool{
		"4bf92f3577b34da6a3ce929d0e0e4736":  true,
		"AB12":                              true,
		"":                                  false,
		"../../etc/passwd":                  false,
		"zz":                                false,
		"4bf92f3577b34da6a3ce929d0e0e47361": false,
	}
	for id, want := range cases {
		if got := IsValidTraceID(id); got != want {
			t.Fatalf("IsValidTraceID(%q)=%v want %v", id, got, want)
		}
	}
}
