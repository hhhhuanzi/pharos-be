package router

// 二开（dh）：把 tracing 类数据源的 trace 读取路径从通用代理 /proxy/:id/* 上堵掉。
//
// /proxy 只做数据源级判权、不解析请求（见 router_dh_proxy.go），所以只要还有 trace 读取路径留在
// 它上面，/dh/trace* 那套按团队判权的保证就不可检查 —— curl 能直接绕过 UI。堵掉之后这些路径只剩
// /dh/trace*，判权点收敛到一处。
//
// 只堵 trace 读取，不堵服务/操作/依赖列表：那几个接口返回的是名字，不含 span 内容，产品明确决定不
// 收口（堵了会坏服务下拉与拓扑）。非 tracing 类数据源（Thanos / Prometheus / ES 等）的任何路径都不
// 动，尤其 /api/v1/query —— 拓扑的 span_metrics 回退查询、大盘和告警都依赖它。
//
// 已知缺口：SkyWalking 的 GraphQL 是单一 endpoint（/graphql），没法按路径区分 trace 查询和服务列表
// 查询，堵不了。SkyWalking 的 trace 详情前端已 fail closed，列表按产品决定不收口；不为此堵掉整个
// graphql（会坏 SkyWalking 的服务下拉和列表）。

import (
	"net/http"
	"strings"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ginx"
)

// dhTracingPluginTypes 是「tracing 类数据源」的判定依据：按 ds.PluginType 判，不按请求路径猜，
// 否则换个前缀就能绕开。与 center/cconf/plugin.go 里 Category 为 tracing 的项保持一致。
var dhTracingPluginTypes = map[string]struct{}{
	dhTracingPluginJaeger:     {},
	dhTracingPluginSkyWalking: {},
}

// dhBlockedTracingPaths 是被堵的 trace 读取路径（精确匹配，或作为前缀后接 /）。
//
//	/api/v3/traces           FindTraces：一批 trace 的全量 span  -> 改用 /dh/trace-summaries 或 /dh/trace-search
//	/api/v3/traces/{id}      GetTrace：单条 trace 的全量 span    -> 改用 /dh/trace/:trace_id
//	/api/v3/trace-summaries  FindTraceSummaries：轻量摘要        -> 改用 /dh/trace-summaries
var dhBlockedTracingPaths = []string{
	"/api/v3/traces",
	"/api/v3/trace-summaries",
}

// dhIsBlockedTracingProxyPath 判定一个 /proxy/:id/*url 的目标路径是否属于被堵的 trace 读取路径。
//
// 归一化后再比：*url 可能带或不带前导斜杠、可能有重复斜杠或尾斜杠，路径匹配必须对这些不敏感。
func dhIsBlockedTracingProxyPath(rawPath string) bool {
	path := rawPath
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return false
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.ToLower(path)

	for _, blocked := range dhBlockedTracingPaths {
		if path == blocked || strings.HasPrefix(path, blocked+"/") {
			return true
		}
	}
	return false
}

// dhGuardTracingProxyPath 对 tracing 类数据源上的 trace 读取路径返回 403 并中断。
func dhGuardTracingProxyPath(ds *models.Datasource, rawPath string) {
	if ds == nil {
		return
	}
	if _, ok := dhTracingPluginTypes[ds.PluginType]; !ok {
		return
	}
	if !dhIsBlockedTracingProxyPath(rawPath) {
		return
	}
	ginx.Bomb(http.StatusForbidden,
		"reading traces through the datasource proxy is not allowed: use /dh/trace-summaries (list), /dh/trace-search (spans) or /dh/trace/{trace_id} (detail), which authorize by service ownership")
}
