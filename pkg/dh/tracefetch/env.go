package tracefetch

import "strings"

// EnvAttributeKey 是环境维度的 resource 属性名。
//
// 用 OTel semconv 1.27+ 的 deployment.environment.name，不是已废弃的 deployment.environment：注入
// 配置写的就是这个键（pharos-ops k8s/otel-app/20-instrumentation.yaml）。它是 resource 级属性，
// 所以每个 span 都带，查询时不需要挑 span 层级。
//
// 键名里的点号是字面量：Jaeger 的 ES 存储没有开 --es.tags-as-fields，tag key 原样落库，用
// deployment@environment@name 这种转义写法查不到。
//
// 前端对应 src/dh/trace/env.ts 的 TRACE_ENV_ATTRIBUTE_KEY，两侧必须一致。
const EnvAttributeKey = "deployment.environment.name"

// NormalizeEnv 归一化环境取值：上报侧写的是小写（test / pre / prod），而 URL 参数里可能带上
// 空白或大小写差异，收窄查询前先对齐，避免「值对了但查不到」。
//
// 前端对应 src/dh/trace/env.ts 的 normalizeTraceEnv。
func NormalizeEnv(env string) string {
	return strings.ToLower(strings.TrimSpace(env))
}

// EnvAttributes 把环境值折成 FindQuery.Attributes 的一项。env 归一化后为空时返回 nil，让调用方
// 走「不加过滤条件」的软降级，而不是发一个匹配不到任何 span 的空值过滤。
func EnvAttributes(env string) map[string]string {
	normalized := NormalizeEnv(env)
	if normalized == "" {
		return nil
	}
	return map[string]string{EnvAttributeKey: normalized}
}

// WithEnv 把环境过滤并进已有的 attributes。
//
// 不改入参：attributes 来自请求解析结果，调用方可能还要复用（富化会带 hint 查一次、没命中再无
// hint 重查），原地写入会把上一次的过滤条件留在下一次查询里。
//
// query.attributes 的多个 key 之间是 AND，所以 env 与富化 hint 可以同时存在。
func WithEnv(attributes map[string]string, env string) map[string]string {
	normalized := NormalizeEnv(env)
	if normalized == "" {
		if len(attributes) == 0 {
			return nil
		}
		merged := make(map[string]string, len(attributes))
		for key, value := range attributes {
			merged[key] = value
		}
		return merged
	}

	merged := make(map[string]string, len(attributes)+1)
	for key, value := range attributes {
		merged[key] = value
	}
	merged[EnvAttributeKey] = normalized
	return merged
}
