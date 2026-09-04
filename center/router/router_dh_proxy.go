package router

import (
	"net/http"
	"sync"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
	"github.com/toolkits/pkg/logger"
)

// 二开（dh）：给官方反向代理 /proxy/:id/*url 补上登录校验与数据源级权限校验。
//
// 官方在 router.go 里按 Center.AnonymousAccess.PromQuerier 分叉注册这条路由：为 true
// 时不挂任何中间件（仓库自带 etc/config.toml 默认就是 true），为 false 时也只挂
// rt.auth()。而 dsProxy 会把数据源里配置的 basic auth 凭据 SetBasicAuth 到转发请求上，
// 于是这条路径既能越权读任意数据源，又相当于把数据源凭据借给了调用方。
//
// 这里把该路由的唯一 handler 换成 dsProxyGuarded，官方文件只保留一行注册；匿名放行面
// 由二开配置 Center.DhProxyGuard 单独控制（默认全关），不再跟着 PromQuerier 走。
//
// 仪表盘限时分享：路由上挂 boardTokenDetect()（只写 context、无/无效 token 不 401）。
// 有效 token 跳过登录，交给官方 dsProxy 做「数据源属于这块板」+ 只读路径白名单；
// 不要在这里复制那段校验。无 token 仍走下面的登录 + 数据源权限（1.2.1 收紧）。
var anonymousProxyWarnOnce sync.Once

func (rt *Router) dsProxyGuarded(c *gin.Context) {
	dsId := ginx.UrlParamInt64(c, "id")

	// tracing 类数据源的 trace 读取路径一律不走这条代理（见 router_dh_proxy_tracing.go）。放在匿名
	// 放行判定之前：匿名 / 分享 token 分支同样不能成为读 trace 的口子。
	dhGuardTracingProxyPath(rt.DatasourceCache.GetById(dsId), c.Param("url"))

	// dh: 有效 board 分享 token 已由 boardTokenDetect 写入 context。跳过 auth/user，
	// 官方 dsProxy 看到 board_share_bid 后会做板内数据源 + 只读路径校验。
	if _, ok := boardTokenBid(c); ok {
		rt.dsProxy(c)
		return
	}

	if rt.Center.DhProxyGuard.AllowAnonymousProxy(dsId, c.Request.Method) {
		anonymousProxyWarnOnce.Do(func() {
			logger.Warningf("dh: anonymous datasource proxy is enabled by Center.DhProxyGuard, "+
				"requests may reach datasource id:%d without login", dsId)
		})
		rt.dsProxy(c)
		return
	}

	// auth()/user() 内部会 c.Next()。本 handler 是这条路由链上的最后一个，c.Next() 只是
	// 把游标推过末尾、不会提前执行 dsProxy，因此可以像官方 router_alert_his_event.go
	// 那样在 handler 内部串行调用；校验失败它们会 ginx.Bomb 中断本次请求。
	rt.auth()(c)
	rt.user()(c)

	rt.checkDsProxyPerm(c, dsId)

	rt.dsProxy(c)
}

// checkDsProxyPerm 做数据源级权限校验，不通过直接中断（此时 dsProxy 不会被调用，
// 数据源凭据也就不会被附加到任何转发请求上）。
//
// DatasourceFilter 与 CheckDsPerm 在开源版分别是恒等、恒真实现，plus 版会注入真实的
// 数据源可见性与细粒度判权；两个都调是为了让这条代理与 /ds-query、/logs-query 等查询
// 接口保持同一套权限口径。CheckDsPerm 的 query 参数传 nil：代理转发的是不透明的原始
// 请求体，拿不到结构化查询，只能做到数据源级，index/topic 级收窄不在这里生效。
func (rt *Router) checkDsProxyPerm(c *gin.Context, dsId int64) {
	ds := rt.DatasourceCache.GetById(dsId)
	if ds == nil {
		ginx.Bomb(http.StatusBadRequest, "no such datasource")
	}

	user := c.MustGet("user").(*models.User)
	if len(rt.DatasourceCache.DatasourceFilter([]*models.Datasource{ds}, user)) == 0 {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}

	if !CheckDsPerm(c, dsId, ds.PluginType, nil) {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}
}
