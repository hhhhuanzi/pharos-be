package tracefetch

import (
	"testing"
	"time"
)

// 属性名写飘就是「查询静默返回 0 条」，而上游对未知参数/对不上的值不会报错，所以键名钉死在测试里。
// 前端对应 src/dh/trace/env.ts 的 TRACE_ENV_ATTRIBUTE_KEY。
func TestEnvAttributeKey(t *testing.T) {
	if EnvAttributeKey != "deployment.environment.name" {
		t.Fatalf("EnvAttributeKey = %q, want deployment.environment.name (semconv 1.27+, 不是已废弃的 deployment.environment)", EnvAttributeKey)
	}
}

func TestNormalizeEnv(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "已经是小写则原样返回", in: "test", want: "test"},
		{name: "去掉首尾空白", in: "  pre  ", want: "pre"},
		{name: "统一成小写", in: "PROD", want: "prod"},
		{name: "混合大小写与空白", in: " Pre\t", want: "pre"},
		{name: "空串", in: "", want: ""},
		{name: "纯空白视为未指定", in: "   ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeEnv(tc.in); got != tc.want {
				t.Errorf("NormalizeEnv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// 归一化必须幂等：同一个值经过 URL 参数与内部传参可能被处理多次。
func TestNormalizeEnvIsIdempotent(t *testing.T) {
	for _, in := range []string{" Test ", "prod", "", "  "} {
		once := NormalizeEnv(in)
		if twice := NormalizeEnv(once); twice != once {
			t.Errorf("NormalizeEnv(NormalizeEnv(%q)) = %q, want %q", in, twice, once)
		}
	}
}

func TestEnvAttributes(t *testing.T) {
	got := EnvAttributes(" TEST ")
	if len(got) != 1 || got[EnvAttributeKey] != "test" {
		t.Fatalf("EnvAttributes(\" TEST \") = %v, want {%s: test}", got, EnvAttributeKey)
	}
}

// 空 env 返回 nil 而不是带空值的过滤项：空值过滤匹配不到任何 span，会把「没指定环境」变成
// 「查不到任何数据」，而软降级要的是不加过滤条件。
func TestEnvAttributesEmptyEnvAddsNoFilter(t *testing.T) {
	for _, in := range []string{"", "   "} {
		if got := EnvAttributes(in); got != nil {
			t.Errorf("EnvAttributes(%q) = %v, want nil", in, got)
		}
	}
}

func TestWithEnvMergesAlongsideExistingAttributes(t *testing.T) {
	attributes := map[string]string{"http.status_code": "500"} // 富化 hint
	got := WithEnv(attributes, "pre")

	want := map[string]string{"http.status_code": "500", EnvAttributeKey: "pre"}
	if len(got) != len(want) {
		t.Fatalf("WithEnv() = %v, want %v", got, want)
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("WithEnv()[%q] = %q, want %q", key, got[key], expected)
		}
	}
}

// WithEnv 不能改入参：attributes 来自请求解析结果，富化会先带 hint 查一次、没命中再无 hint 重查，
// 原地写入会把上一次的 env 过滤留在下一次查询里。
func TestWithEnvDoesNotMutateInput(t *testing.T) {
	attributes := map[string]string{"http.status_code": "500"}
	WithEnv(attributes, "prod")

	if len(attributes) != 1 {
		t.Fatalf("入参被改写了：%v", attributes)
	}
	if _, ok := attributes[EnvAttributeKey]; ok {
		t.Errorf("入参被写入了 %s", EnvAttributeKey)
	}
}

func TestWithEnvEmptyEnvKeepsAttributes(t *testing.T) {
	got := WithEnv(map[string]string{"http.status_code": "500"}, "  ")
	if len(got) != 1 || got["http.status_code"] != "500" {
		t.Fatalf("WithEnv(attrs, \"  \") = %v, want 原有 attributes 不变", got)
	}
	if _, ok := got[EnvAttributeKey]; ok {
		t.Errorf("env 为空时不应加过滤项，got %v", got)
	}
}

func TestWithEnvNoAttributesNoEnvIsNil(t *testing.T) {
	if got := WithEnv(nil, ""); got != nil {
		t.Fatalf("WithEnv(nil, \"\") = %v, want nil（findTracesParams 靠 len 判断是否发参数）", got)
	}
}

// 端到端确认这条通道是通的：env 经 WithEnv 进 Attributes 后，一定会被序列化进 query.attributes。
func TestFindTracesParamsCarriesEnvAttribute(t *testing.T) {
	query := FindQuery{
		Service:      "turms-business-service",
		StartTimeMin: time.UnixMilli(1731000000000),
		StartTimeMax: time.UnixMilli(1731000060000),
		Attributes:   WithEnv(nil, "test"),
	}

	values, err := query.findTracesParams()
	if err != nil {
		t.Fatalf("findTracesParams() error = %v", err)
	}
	if got := values.Get("query.attributes"); got != `{"deployment.environment.name":"test"}` {
		t.Errorf("query.attributes = %q, want {\"deployment.environment.name\":\"test\"}", got)
	}
}

// env 为空时不能发 query.attributes：发一个空 map 就是多一个上游要解析的参数，语义还不等于「不过滤」。
func TestFindTracesParamsOmitsAttributesWithoutEnv(t *testing.T) {
	query := FindQuery{
		Service:      "turms-business-service",
		StartTimeMin: time.UnixMilli(1731000000000),
		StartTimeMax: time.UnixMilli(1731000060000),
		Attributes:   WithEnv(nil, ""),
	}

	values, err := query.findTracesParams()
	if err != nil {
		t.Fatalf("findTracesParams() error = %v", err)
	}
	if _, ok := values["query.attributes"]; ok {
		t.Errorf("query.attributes should be omitted, got %q", values.Get("query.attributes"))
	}
}
