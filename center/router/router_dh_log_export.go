package router

import (
	"github.com/ccfos/nightingale/v6/center/cconf"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

// dh 二开：日志导出（ROADMAP.md R-22 / P-03）的埋点入口。
//
// 本文件【不查询任何日志数据】——导出的实际查询由前端直接走既有的
// /api/n9e/proxy/:id/* 与 /api/n9e/logs-query，本接口只承担三件事：
//  1. 用 /log/export 权限点把关（rt.perm，注册见 router.go）；
//  2. 让 pages.Use(rt.dhOperationLog()) 审计中间件能捕获到这次导出动作——
//     /proxy/ 与 /logs-query 都在 pkg/dh/audit/routes_map.go 的
//     noisyContains 里（这个豁免本身是对的，每翻一页落一条审计毫无意义），
//     所以不单独埋点就完全审计不到；
//  3. 下发服务端配置的导出条数上限，避免上限硬编码在前端 bundle 里。
//
// 设计说明见 pharos-ops/HANDOFF-log-export.md §9。
type logExportRecordForm struct {
	Phase          string   `json:"phase" binding:"required,oneof=start finish"`
	Cate           string   `json:"cate"`
	DatasourceId   int64    `json:"datasource_id"`
	DatasourceName string   `json:"datasource_name"`
	Index          string   `json:"index"`
	Query          string   `json:"query"`
	Start          int64    `json:"start"`
	End            int64    `json:"end"`
	Format         string   `json:"format"`
	Strategy       string   `json:"strategy"`
	ExpectRows     int      `json:"expect_rows"`
	Fields         []string `json:"fields"`

	ActualRows  int    `json:"actual_rows"`
	Status      string `json:"status"`
	StopReason  string `json:"stop_reason"`
	OutputBytes int64  `json:"output_bytes"`
	Error       string `json:"error"`
	DurationMs  int64  `json:"duration_ms"`
}

func (rt *Router) dhLogExportRecord(c *gin.Context) {
	var f logExportRecordForm
	ginx.BindJSON(c, &f)

	// 数据源级判权：与 router_dh_proxy.go 的 checkDsProxyPerm 保持同一套口径，
	// 避免出现「有 /log/export 权限点但看不到该数据源的人也能导」的越权。
	if f.DatasourceId > 0 {
		rt.checkDsProxyPerm(c, f.DatasourceId)
	}

	// 审计由 pages.Use(rt.dhOperationLog()) 中间件在 c.Next() 之后异步落库，
	// 这里【不需要】手动写 operation_log。
	ginx.NewRender(c).Data(gin.H{
		"max_rows": cconf.DhLogExport.MaxRows,
	}, nil)
}
