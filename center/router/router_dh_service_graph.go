package router

// 二开（dh）：服务拓扑改由后端查询 + 按团队硬过滤后返回。
//
// 之前前端直接把三条 service_graph PromQL 打到 /proxy/:id/api/v1/query，而 dsProxyGuarded 不解析
// PromQL、只能做到数据源级判权（见 router_dh_proxy.go），拿不到服务维度，于是全局拓扑会把所有团队
// 的服务和调用关系都吐给前端。团队归属只存在 dh_service_team 表里，不在任何指标 label 上，指标侧
// 没法下推，所以在这里查全量再按可见性裁剪。

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/dh/servicegraph"
	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
	"github.com/ccfos/nightingale/v6/pkg/ginx"
	promsdk "github.com/ccfos/nightingale/v6/pkg/prom"

	"github.com/gin-gonic/gin"
)

const (
	dhServiceGraphTimeout = 30 * time.Second
	// 单次返回的边上限，避免大基数查询把 center 打挂；截断前已按错误率 / P95 / 调用量排序。
	dhServiceGraphMaxEdges = 3000
)

func (rt *Router) dhServiceGraph(c *gin.Context) {
	dsId := ginx.QueryInt64(c, "datasource_id", 0)
	if dsId <= 0 {
		ginx.Bomb(http.StatusBadRequest, "datasource_id is required")
	}

	end := ginx.QueryInt64(c, "end", 0)
	if end <= 0 {
		end = time.Now().Unix()
	}
	start := ginx.QueryInt64(c, "start", 0)
	if start <= 0 || start >= end {
		ginx.Bomb(http.StatusBadRequest, "invalid start/end")
	}

	scope := servicegraph.Scope{
		Service:   strings.TrimSpace(ginx.QueryStr(c, "service", "")),
		Env:       strings.TrimSpace(ginx.QueryStr(c, "env", "")),
		Cluster:   strings.TrimSpace(ginx.QueryStr(c, "cluster", "")),
		Namespace: strings.TrimSpace(ginx.QueryStr(c, "namespace", "")),
	}

	// 与 /proxy、/ds-query 保持同一套数据源级口径，别让这个新接口变成绕过数据源判权的口子。
	rt.checkDsProxyPerm(c, dsId)

	var cli promsdk.API
	if rt.PromClients != nil {
		cli = rt.PromClients.GetCli(dsId)
	}
	if cli == nil {
		ginx.Bomb(http.StatusBadRequest, "no such datasource")
	}

	qctx, cancel := context.WithTimeout(c.Request.Context(), dhServiceGraphTimeout)
	defer cancel()

	ts := time.Unix(end, 0)
	promRange := servicegraph.ToPromRange(end - start)

	edges, err := dhQueryServiceGraph(qctx, cli, promRange, ts, scope)
	if err != nil {
		ginx.Bomb(http.StatusBadGateway, "query service graph failed: %s", err.Error())
	}
	if len(edges) == 0 && scope.Service != "" && scope.HasWorkload() {
		edges, err = dhServiceGraphWorkloadFallback(qctx, cli, promRange, ts, scope)
		if err != nil {
			ginx.Bomb(http.StatusBadGateway, "query service graph failed: %s", err.Error())
		}
	}

	user := c.MustGet("user").(*models.User)
	viewAll, _, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)

	rows, err := models.DhServiceTeamGets(rt.Ctx)
	ginx.Dangerous(err)
	bindings := models.DhServiceTeamToBindings(rows)

	groupIds, err := models.MyGroupIds(rt.Ctx, user.Id)
	ginx.Dangerous(err)
	groupSet := serviceteam.GroupIDSet(groupIds)

	res := servicegraph.Filter(servicegraph.FilterInput{
		Edges:    edges,
		ViewAll:  viewAll,
		MaxEdges: dhServiceGraphMaxEdges,
		CanSee: func(service string) bool {
			return servicegraph.CanSeeAnyEnv(false, groupSet, bindings, service)
		},
	})

	ginx.NewRender(c).Data(gin.H{
		"edges":            res.Edges,
		"visible_services": res.VisibleServices,
		"truncated":        res.Truncated,
	}, nil)
}

func dhQueryServiceGraph(ctx context.Context, cli promsdk.API, promRange string, ts time.Time, scope servicegraph.Scope) ([]servicegraph.Edge, error) {
	queries := servicegraph.BuildQueries(promRange, scope)
	promqls := [3]string{queries.Total, queries.Failed, queries.P95}

	var (
		wg      sync.WaitGroup
		samples [3][]servicegraph.Sample
		errs    [3]error
	)
	for i := range promqls {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			samples[idx], errs[idx] = dhQueryPromVector(ctx, cli, promqls[idx], ts)
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return servicegraph.MergeVectors(samples[0], samples[1], samples[2]), nil
}

func dhQueryPromVector(ctx context.Context, cli promsdk.API, promql string, ts time.Time) ([]servicegraph.Sample, error) {
	value, _, err := cli.Query(ctx, promql, ts)
	if err != nil {
		return nil, err
	}
	return servicegraph.SamplesFromValue(value), nil
}

// dhServiceGraphWorkloadFallback 与前端原实现一致：详情页带了 env / cluster / namespace 但查不到边时，
// 判断 service_graph 是否已经有 env dimension；还没有就退回只按服务名查，再用本环境的 CLIENT span_name
// 剔除外环境 peer。探测与 span_name 查询失败都按「拿不到额外信息」处理，不让回退路径反过来打断主流程。
func dhServiceGraphWorkloadFallback(ctx context.Context, cli promsdk.API, promRange string, ts time.Time, scope servicegraph.Scope) ([]servicegraph.Edge, error) {
	if probe, err := dhQueryPromVector(ctx, cli, servicegraph.EnvDimensionProbeQuery(), ts); err == nil && servicegraph.HasPositiveSample(probe) {
		return nil, nil
	}

	mixed, err := dhQueryServiceGraph(ctx, cli, promRange, ts, servicegraph.Scope{Service: scope.Service})
	if err != nil {
		return nil, err
	}

	promql := servicegraph.SpanmetricsPeerQuery(promRange, scope)
	if promql == "" {
		return mixed, nil
	}
	samples, err := dhQueryPromVector(ctx, cli, promql, ts)
	if err != nil {
		return mixed, nil
	}
	tokens := servicegraph.PeerTokensFromSpanNames(servicegraph.SpanNamesFromSamples(samples))
	return servicegraph.FilterEdgesByPeerTokens(mixed, scope.Service, tokens), nil
}
