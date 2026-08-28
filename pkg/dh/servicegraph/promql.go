// Package servicegraph 是 dh 二开：OTel service_graph 拓扑的查询构造、向量合并与团队可见性裁剪。
//
// 查询构造与向量合并是 pharos-fe src/dh/trace/dependencies/promql.ts 的移植，两侧口径必须一致；
// 前端那个文件已经不在生产路径上，但保留为口径基准，改这里时先与它比对。
package servicegraph

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/prometheus/common/model"
)

// OTel Collector service_graph connector / spanmetrics connector 的默认指标名。
const (
	MetricRequestTotal  = "traces_service_graph_request_total"
	MetricRequestFailed = "traces_service_graph_request_failed_total"
	MetricServerBucket  = "traces_service_graph_request_server_seconds_bucket"
	MetricSpanCalls     = "traces_span_metrics_calls_total"
)

// connector dimensions 带 client_ / server_ 前缀，与中心 Jaeger collector 的采集标签
// （cluster / env / namespace）不是同一批，不能混用。
const (
	ClientEnvLabel       = "client_deployment_environment_name"
	ServerEnvLabel       = "server_deployment_environment_name"
	ClientClusterLabel   = "client_k8s_cluster_name"
	ServerClusterLabel   = "server_k8s_cluster_name"
	ClientNamespaceLabel = "client_k8s_namespace_name"
	ServerNamespaceLabel = "server_k8s_namespace_name"
)

// Scope 是详情页的 1-hop 切片；全局拓扑传零值，查全集群。
type Scope struct {
	Service   string
	Env       string
	Cluster   string
	Namespace string
}

func (s Scope) HasWorkload() bool {
	return s.Env != "" || s.Cluster != "" || s.Namespace != ""
}

type Queries struct {
	Total  string
	Failed string
	P95    string
}

type Sample struct {
	Labels map[string]string
	Value  float64
}

type Edge struct {
	Client string `json:"client"`
	Server string `json:"server"`
	// OTel connection_type（空 / messaging_system / database / virtual_node）。
	ConnectionType string   `json:"connection_type"`
	RequestCount   float64  `json:"request_count"`
	FailedCount    float64  `json:"failed_count"`
	ErrorRate      float64  `json:"error_rate"`
	P95Seconds     *float64 `json:"p95_seconds,omitempty"`
}

func ToPromRange(seconds int64) string {
	s := seconds
	if s < 1 {
		s = 1
	}
	if s%86400 == 0 {
		return fmt.Sprintf("%dd", s/86400)
	}
	if s%3600 == 0 {
		return fmt.Sprintf("%dh", s/3600)
	}
	if s%60 == 0 {
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%ds", s)
}

// EscapePromLabel 转义 PromQL 字符串字面量里的 \ 与 "。单次遍历替换，等价于前端的两步 replace。
func EscapePromLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

func labelEq(label, value string) string {
	return label + `="` + EscapePromLabel(value) + `"`
}

func sideSelector(side string, scope Scope) string {
	if scope.Service == "" {
		return ""
	}
	envLabel, clusterLabel, namespaceLabel := ServerEnvLabel, ServerClusterLabel, ServerNamespaceLabel
	if side == "client" {
		envLabel, clusterLabel, namespaceLabel = ClientEnvLabel, ClientClusterLabel, ClientNamespaceLabel
	}
	parts := []string{labelEq(side, scope.Service)}
	if scope.Env != "" {
		parts = append(parts, labelEq(envLabel, scope.Env))
	}
	if scope.Cluster != "" {
		parts = append(parts, labelEq(clusterLabel, scope.Cluster))
	}
	if scope.Namespace != "" {
		parts = append(parts, labelEq(namespaceLabel, scope.Namespace))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func scopedSum(fn, metric, promRange, by string, scope Scope) string {
	if scope.Service == "" {
		return fmt.Sprintf("sum by (%s) (%s(%s[%s]))", by, fn, metric, promRange)
	}
	return fmt.Sprintf("sum by (%s) (%s(%s%s[%s])) or sum by (%s) (%s(%s%s[%s]))",
		by, fn, metric, sideSelector("client", scope), promRange,
		by, fn, metric, sideSelector("server", scope), promRange)
}

func BuildQueries(promRange string, scope Scope) Queries {
	const edgeBy = "client, server, connection_type"
	return Queries{
		Total:  scopedSum("increase", MetricRequestTotal, promRange, edgeBy, scope),
		Failed: scopedSum("increase", MetricRequestFailed, promRange, edgeBy, scope),
		P95:    fmt.Sprintf("histogram_quantile(0.95, %s)", scopedSum("rate", MetricServerBucket, promRange, edgeBy+", le", scope)),
	}
}

// EnvDimensionProbeQuery 非零结果表示 service_graph 序列上已经刮到了 collector 的 env dimension。
func EnvDimensionProbeQuery() string {
	return fmt.Sprintf(`count(%s{%s=~".+"})`, MetricRequestTotal, ClientEnvLabel)
}

// SpanmetricsPeerQuery 取该环境 CLIENT span 的 span_name，用于 1-hop 回退时剔除外环境 peer。
// service 或 env 缺失时返回空串，调用方跳过这一步。
func SpanmetricsPeerQuery(promRange string, scope Scope) string {
	if scope.Service == "" || scope.Env == "" {
		return ""
	}
	parts := []string{
		labelEq("service_name", scope.Service),
		labelEq("deployment_environment_name", scope.Env),
		`span_kind="SPAN_KIND_CLIENT"`,
	}
	if scope.Cluster != "" {
		parts = append(parts, labelEq("k8s_cluster_name", scope.Cluster))
	}
	if scope.Namespace != "" {
		parts = append(parts, labelEq("k8s_namespace_name", scope.Namespace))
	}
	return fmt.Sprintf("sum by (span_name) (increase(%s{%s}[%s]))", MetricSpanCalls, strings.Join(parts, ", "), promRange)
}

func SamplesFromValue(value model.Value) []Sample {
	vector, ok := value.(model.Vector)
	if !ok {
		return nil
	}
	out := make([]Sample, 0, len(vector))
	for _, item := range vector {
		if item == nil {
			continue
		}
		labels := make(map[string]string, len(item.Metric))
		for name, val := range item.Metric {
			labels[string(name)] = string(val)
		}
		out = append(out, Sample{Labels: labels, Value: float64(item.Value)})
	}
	return out
}

func SpanNamesFromSamples(samples []Sample) []string {
	out := make([]string, 0, len(samples))
	for _, sample := range samples {
		if name := sample.Labels["span_name"]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

func HasPositiveSample(samples []Sample) bool {
	for _, sample := range samples {
		if isUsableValue(sample.Value) && sample.Value > 0 {
			return true
		}
	}
	return false
}

func isUsableValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func edgeMapKey(client, server, connectionType string) string {
	return client + "\x00" + server + "\x00" + connectionType
}

// MergeVectors 按 (client, server, connection_type) 把三个即时向量对齐成边。
// 无流量（且无失败）的边丢掉，否则一条陈旧的 0 值序列会画出一条幽灵连线。
func MergeVectors(total, failed, p95 []Sample) []Edge {
	byKey := make(map[string]*Edge)
	order := make([]string, 0, len(total))

	ensure := func(labels map[string]string) *Edge {
		client, server, connectionType := labels["client"], labels["server"], labels["connection_type"]
		if client == "" || server == "" {
			return nil
		}
		key := edgeMapKey(client, server, connectionType)
		if edge, ok := byKey[key]; ok {
			return edge
		}
		edge := &Edge{Client: client, Server: server, ConnectionType: connectionType}
		byKey[key] = edge
		order = append(order, key)
		return edge
	}

	for _, sample := range total {
		if edge := ensure(sample.Labels); edge != nil && isUsableValue(sample.Value) {
			edge.RequestCount = sample.Value
		}
	}
	for _, sample := range failed {
		if edge := ensure(sample.Labels); edge != nil && isUsableValue(sample.Value) {
			edge.FailedCount = sample.Value
		}
	}
	for _, sample := range p95 {
		if edge := ensure(sample.Labels); edge != nil && isUsableValue(sample.Value) {
			value := sample.Value
			edge.P95Seconds = &value
		}
	}

	out := make([]Edge, 0, len(order))
	for _, key := range order {
		edge := byKey[key]
		if edge.RequestCount <= 0 && edge.FailedCount <= 0 {
			continue
		}
		if edge.RequestCount > 0 {
			edge.ErrorRate = math.Min(1, edge.FailedCount/edge.RequestCount)
		}
		out = append(out, *edge)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ErrorRate != b.ErrorRate {
			return a.ErrorRate > b.ErrorRate
		}
		if p95Of(a) != p95Of(b) {
			return p95Of(a) > p95Of(b)
		}
		return a.RequestCount > b.RequestCount
	})
	return out
}

func p95Of(edge Edge) float64 {
	if edge.P95Seconds == nil {
		return 0
	}
	return *edge.P95Seconds
}
