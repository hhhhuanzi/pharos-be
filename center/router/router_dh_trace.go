package router

// 二开（dh）：trace 详情改由后端取回、判权后再原样回传。
//
// 之前前端直接把 /api/v3/traces/{traceID} 打到 /proxy/:id/*，而这个请求里只有 traceID、没有任何
// 服务维度，dsProxyGuarded 又不解析响应体（见 router_dh_proxy.go），所以只能做到数据源级判权：
// 任何有数据源查询权的登录用户都能看任意一条 trace 的全部 span、resource 属性和 SQL。团队归属只
// 存在 dh_service_team 表里，判权必须发生在一个会读 trace 内容的接口上。
//
// 放行粒度是整条 trace：trace 涉及的服务集合与我的可见集合有交集就整条放行（含其它团队的 span），
// 全不可见才 403。跨团队链路两端都能看才有排障价值，span 级遮蔽会让瀑布图断层。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
	"github.com/ccfos/nightingale/v6/pkg/dh/tracefetch"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

const dhTraceFetchTimeout = 30 * time.Second

func (rt *Router) dhTraceGet(c *gin.Context) {
	traceID := strings.TrimSpace(ginx.UrlParamStr(c, "trace_id"))
	if !tracefetch.IsValidTraceID(traceID) {
		ginx.Bomb(http.StatusBadRequest, "invalid trace_id")
	}

	dsId := ginx.QueryInt64(c, "datasource_id", 0)
	if dsId <= 0 {
		ginx.Bomb(http.StatusBadRequest, "datasource_id is required")
	}

	// 与 /proxy、/ds-query 保持同一套数据源级口径，别让这个新接口变成绕过数据源可见性的口子。
	rt.checkDsProxyPerm(c, dsId)

	ds := rt.DatasourceCache.GetById(dsId)
	if ds == nil {
		ginx.Bomb(http.StatusBadRequest, "no such datasource")
	}

	// plugin_type 只是前端的提示；判定以数据源自身的类型为准，避免用查询参数改写取数方式。
	switch ds.PluginType {
	case dhTracingPluginJaeger:
	case dhTracingPluginSkyWalking:
		// SkyWalking 的 trace 详情走 GraphQL，形状与 api_v3 完全不同，本期不做：宁可这条链路
		// 不可用，也不要放一个没验证过的客户端进判权路径。
		ginx.Bomb(http.StatusNotImplemented, "trace detail authorization is not supported for skywalking yet")
	default:
		ginx.Bomb(http.StatusBadRequest, "datasource is not a tracing datasource")
	}

	cli, err := tracefetch.NewJaegerClient(ds, dhTraceFetchTimeout)
	if err != nil {
		ginx.Bomb(http.StatusBadRequest, "invalid datasource config")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), dhTraceFetchTimeout)
	defer cancel()

	body, err := cli.GetTrace(ctx, traceID)
	if errors.Is(err, tracefetch.ErrTraceNotFound) {
		ginx.Bomb(http.StatusNotFound, "no such trace")
	}
	if err != nil {
		ginx.Bomb(http.StatusBadGateway, "query trace failed: %s", err.Error())
	}

	found, err := tracefetch.ExtractServices(body)
	if err != nil {
		ginx.Bomb(http.StatusBadGateway, "invalid upstream response")
	}
	if found.ResourceSpans == 0 {
		ginx.Bomb(http.StatusNotFound, "no such trace")
	}

	visible, err := rt.dhTraceVisible(c, found.Names)
	ginx.Dangerous(err)
	if !visible {
		ginx.Bomb(http.StatusForbidden, "trace does not belong to your team")
	}

	// 原样回传上游响应体（OTLP -> Jaeger 的转换仍在前端做），只补一层 n9e 的标准信封。
	ginx.NewRender(c).Data(json.RawMessage(body), nil)
}

const (
	dhTracingPluginJaeger     = "jaeger"
	dhTracingPluginSkyWalking = "skywalking"
)

// dhTraceVisible 判定 trace 里出现过的服务是否与当前用户可见集合有交集。
//
// trace 响应里没有环境维度，与拓扑指标一样，因此按服务名对所有 env 绑定做 OR
// （serviceteam.CanSeeAnyEnv）；未绑定团队的服务不可见。
func (rt *Router) dhTraceVisible(c *gin.Context, services []string) (bool, error) {
	user := c.MustGet("user").(*models.User)
	viewAll, _, err := rt.dhServiceViewer(user)
	if err != nil {
		return false, err
	}
	if viewAll {
		return true, nil
	}
	if len(services) == 0 {
		return false, nil
	}

	rows, err := models.DhServiceTeamGets(rt.Ctx)
	if err != nil {
		return false, err
	}
	bindings := models.DhServiceTeamToBindings(rows)

	groupIds, err := models.MyGroupIds(rt.Ctx, user.Id)
	if err != nil {
		return false, err
	}
	groupSet := serviceteam.GroupIDSet(groupIds)

	for _, name := range services {
		if serviceteam.CanSeeAnyEnv(false, groupSet, bindings, name) {
			return true, nil
		}
	}
	return false, nil
}
