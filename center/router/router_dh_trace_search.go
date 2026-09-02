package router

// 二开（dh）：trace 列表与富化查询改由后端取数、判权后再回传。
//
// 之前这两条路径都走 /proxy/:id/api/v3/traces（FindTraces）。dsProxyGuarded 不解析请求也不解析响应
// （见 router_dh_proxy.go），只能做到数据源级判权，于是：
//
//   - 列表：响应里是全部匹配 trace 的完整 span（含 resource 属性与 SQL），摘要是在浏览器里裁出来
//     的。服务下拉又不过滤，任何登录用户选中别的团队的服务查一次列表，就能从网络响应里拿到那批
//     trace 的全部 span —— 不需要点详情。
//   - 富化 / 抽屉：阶段二在前端按白名单收敛了展示面，但请求本身还是同一个 FindTraces。
//
// 与 /dh/trace/:trace_id（内容检查）不同，这两类请求的判权是**参数检查**：请求本身带 service
// 参数（「给我服务 X 的 trace」），所以只需要问「X 对当前用户可见吗」，不需要读 span 内容。
//
// 放行粒度沿用 /dh/trace/:trace_id 的规则：service 参数可见 ⇒ 整条 trace 放行，包括这些 trace 里
// 其它团队的 span 和 SQL —— 这条链路本来就是我的服务发起的。所以后端不做 span 级裁剪；前端
// dependencies/** 那层按 span 发出方的过滤保留，定位是展示层收敛而不是判权。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccfos/nightingale/v6/pkg/dh/tracefetch"
	"github.com/ccfos/nightingale/v6/pkg/dh/tracesummary"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

const (
	// dhTraceSearchTimeout 是整体取数超时。列表要在服务端拉全量 span 再折摘要，比取单条 trace 重，
	// 但仍受前端查询的时间窗与 num_traces 约束，不额外放宽。
	dhTraceSearchTimeout = 45 * time.Second
	// dhTraceSearchMaxNumTraces 是单次查询的 trace 数上限，与前端表单的 TRACE_SEARCH_MAX_LIMIT 一致
	// （src/dh/trace/explorer/searchDefaults.ts）；超过直接 400，不静默截断成别的语义。
	dhTraceSearchMaxNumTraces = 2000
	// dhTraceSearchDefaultNumTraces 与前端 TRACE_LIST_SUMMARY_LIMIT 一致。
	dhTraceSearchDefaultNumTraces = 100
	// dhTraceSearchMaxWindow 限制查询时间窗，避免一次扫过大的区间。
	dhTraceSearchMaxWindow = 7 * 24 * time.Hour
	// dhTraceSearchMaxAttributes 限制属性过滤的条数（富化的 hint 只会带 1~2 条）。
	dhTraceSearchMaxAttributes = 32
)

// dhTraceSearchRequest 是两个接口共用的入参，字段名与前端 TraceSearchParams 一致。
type dhTraceSearchRequest struct {
	datasourceID int64
	query        tracefetch.FindQuery
	numTraces    int
}

// dhParseTraceSearchRequest 解析并校验入参。
//
// service 必填是这两个接口的判权前提：判权就是「service 参数对我可见吗」，没有 service 的请求无法
// 用参数判权，只能 fail closed。前端链路追踪页的 Service 下拉是可清空的（placeholder「全部服务」），
// 所以「不选服务直接查」确实存在 —— 这里返回 400，前端按 list.service_required 提示用户选服务，
// 而不是放行一次全量查询。
func dhParseTraceSearchRequest(c *gin.Context) dhTraceSearchRequest {
	dsID := ginx.QueryInt64(c, "datasource_id", 0)
	if dsID <= 0 {
		ginx.Bomb(http.StatusBadRequest, "datasource_id is required")
	}

	service := strings.TrimSpace(ginx.QueryStr(c, "service", ""))
	if service == "" {
		ginx.Bomb(http.StatusBadRequest, "service is required: trace search is authorized by the service parameter")
	}

	startMs := ginx.QueryInt64(c, "start_time_min", 0)
	endMs := ginx.QueryInt64(c, "start_time_max", 0)
	if startMs <= 0 || endMs <= 0 || startMs >= endMs {
		ginx.Bomb(http.StatusBadRequest, "invalid start_time_min/start_time_max")
	}
	if time.Duration(endMs-startMs)*time.Millisecond > dhTraceSearchMaxWindow {
		ginx.Bomb(http.StatusBadRequest, "time range is too wide")
	}

	numTraces := ginx.QueryInt(c, "num_traces", 0)
	if numTraces < 0 || numTraces > dhTraceSearchMaxNumTraces {
		ginx.Bomb(http.StatusBadRequest, "num_traces must be between 1 and %d", dhTraceSearchMaxNumTraces)
	}
	if numTraces == 0 {
		numTraces = dhTraceSearchDefaultNumTraces
	}

	// env 是查询收窄，不是判权维度：空值走软降级（不加过滤条件、跨环境结果照常返回），因为链路是
	// 排障主路径，把 tab 变成空白比混着几个环境更难用。判权仍然只看 service，见下方 dhTraceVisible。
	env := ginx.QueryStr(c, "env", "")

	return dhTraceSearchRequest{
		datasourceID: dsID,
		numTraces:    numTraces,
		query: tracefetch.FindQuery{
			Service:      service,
			StartTimeMin: time.UnixMilli(startMs),
			StartTimeMax: time.UnixMilli(endMs),
			Operation:    strings.TrimSpace(ginx.QueryStr(c, "operation", "")),
			DurationMin:  strings.TrimSpace(ginx.QueryStr(c, "duration_min", "")),
			DurationMax:  strings.TrimSpace(ginx.QueryStr(c, "duration_max", "")),
			NumTraces:    numTraces,
			Attributes:   tracefetch.WithEnv(dhParseTraceAttributes(c), env),
		},
	}
}

// dhParseTraceAttributes 解析 attributes（URL 编码的 JSON string map）。富化会先带 hint 查一次、
// 没命中再无 hint 重查，所以这个参数必须能透传，否则富化覆盖率会掉。
func dhParseTraceAttributes(c *gin.Context) map[string]string {
	raw := strings.TrimSpace(ginx.QueryStr(c, "attributes", ""))
	if raw == "" {
		return nil
	}
	var attributes map[string]string
	if err := json.Unmarshal([]byte(raw), &attributes); err != nil {
		ginx.Bomb(http.StatusBadRequest, "invalid attributes: expected a JSON string map")
	}
	if len(attributes) > dhTraceSearchMaxAttributes {
		ginx.Bomb(http.StatusBadRequest, "too many attributes")
	}
	return attributes
}

// dhTraceFindTraces 做完数据源级判权与 service 可见性判权后向上游取数，返回原始响应体。
func (rt *Router) dhTraceFindTraces(c *gin.Context, req dhTraceSearchRequest) []byte {
	// 与 /proxy、/ds-query 保持同一套数据源级口径，别让这个新接口变成绕过数据源可见性的口子。
	rt.checkDsProxyPerm(c, req.datasourceID)

	ds := rt.DatasourceCache.GetById(req.datasourceID)
	if ds == nil {
		ginx.Bomb(http.StatusBadRequest, "no such datasource")
	}

	// plugin_type 只是前端的提示；判定以数据源自身的类型为准，避免用查询参数改写取数方式。
	switch ds.PluginType {
	case dhTracingPluginJaeger:
	case dhTracingPluginSkyWalking:
		// SkyWalking 的列表走 GraphQL 单一 endpoint，形状与 api_v3 完全不同，本期不做。
		ginx.Bomb(http.StatusNotImplemented, "trace search authorization is not supported for skywalking yet")
	default:
		ginx.Bomb(http.StatusBadRequest, "datasource is not a tracing datasource")
	}

	// service 可见性：与拓扑、trace 详情一样按服务名对所有 env 做 OR。
	//
	// 请求里现在带 env 了（收窄查询用），但判权刻意不看它：dh_service_team 虽有 env 列，当前一律
	// 写空串，按 env 判权只会误拒。收窄查询不可能放宽权限，所以这里保持只问「service 对我可见吗」。
	visible, err := rt.dhTraceVisible(c, []string{req.query.Service})
	ginx.Dangerous(err)
	if !visible {
		ginx.Bomb(http.StatusForbidden, "service does not belong to your team")
	}

	cli, err := tracefetch.NewJaegerClient(ds, dhTraceSearchTimeout)
	if err != nil {
		ginx.Bomb(http.StatusBadRequest, "invalid datasource config")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dhTraceSearchTimeout)
	defer cancel()

	body, err := cli.FindTraces(ctx, req.query)
	if errors.Is(err, tracefetch.ErrTraceNotFound) {
		return nil
	}
	if errors.Is(err, tracefetch.ErrResponseTooLarge) {
		ginx.Bomb(http.StatusRequestEntityTooLarge, "too many spans matched: narrow the time range or lower num_traces")
	}
	if err != nil {
		ginx.Bomb(http.StatusBadGateway, "query traces failed: %s", err.Error())
	}
	return body
}

// dhTraceSummaries 是 trace 列表：服务端取全量 span、算完摘要只回摘要。
//
// 不用 Jaeger 的轻量 /api/v3/trace-summaries：那个接口不返回 tags，列表「类型」列（rootType 依赖
// messaging.system / http.* / db.*）就填不出来。改成服务端算，类型列仍然准确，而浏览器侧的全量
// span 下载消失 —— 重的传输变成 Jaeger -> center 的集群内流量。
func (rt *Router) dhTraceSummaries(c *gin.Context) {
	req := dhParseTraceSearchRequest(c)
	body := rt.dhTraceFindTraces(c, req)

	if len(body) == 0 {
		ginx.NewRender(c).Data(gin.H{"summaries": []tracesummary.TraceSummary{}, "truncated": false}, nil)
		return
	}

	traces, err := tracesummary.DecodeTraces(body)
	if err != nil {
		ginx.Bomb(http.StatusBadGateway, "invalid upstream response")
	}

	ginx.NewRender(c).Data(gin.H{
		"summaries": tracesummary.SummarizeAll(traces),
		// 与前端原口径一致（api.ts 的 traces.length >= limit）：按归组后的 trace 条数判，不按摘要条数。
		"truncated": len(traces) >= req.numTraces,
	}, nil)
}

// dhTraceSearch 是富化与抽屉用的完整 span 查询：判权后把上游 JSON 原样放进 n9e 信封的 dat 透传，
// OTLP -> Jaeger 的转换仍在前端做（与 /dh/trace/:trace_id 同一做法）。
func (rt *Router) dhTraceSearch(c *gin.Context) {
	req := dhParseTraceSearchRequest(c)
	body := rt.dhTraceFindTraces(c, req)

	if len(body) == 0 {
		// 上游用 404 表达空结果，这里统一成空信封，让前端的空态与「查询失败」区分开。
		ginx.NewRender(c).Data(struct{}{}, nil)
		return
	}
	ginx.NewRender(c).Data(json.RawMessage(body), nil)
}
