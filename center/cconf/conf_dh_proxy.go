package cconf

import "strings"

// DhProxyGuard 是二开新增配置，只作用于反向代理路由 /proxy/:id/*url。
//
// 官方把这条路由的鉴权与 AnonymousAccess.PromQuerier 绑在一起：PromQuerier=true
// 时该路由不挂任何中间件，任何人都能借代理直连任意数据源，而 dsProxy 还会把数据源
// 里配置的 basic auth 凭据附加到转发请求上（等于把凭据借给调用方）。PromQuerier
// 同时控制着十几个查询接口，直接改它波及面过大，所以这里把 /proxy 的匿名放行单独
// 抽出来，默认关闭、需要时显式开启并收敛到最小面。
type DhProxyGuard struct {
	// AllowAnonymous 为 true 时才允许未登录访问 /proxy/:id/*url。默认 false，
	// 即无论 PromQuerier 取值如何，该路由都要求登录 + 数据源权限。
	AllowAnonymous bool

	// AnonymousDatasourceIds 限定匿名可访问的数据源 id。为空表示不限制（等同官方
	// 行为，风险最高）；只在 AllowAnonymous=true 时生效。
	AnonymousDatasourceIds []int64

	// AnonymousMethods 限定匿名可用的 HTTP 方法，为空表示不限制。只在
	// AllowAnonymous=true 时生效。建议只留 GET/HEAD。
	AnonymousMethods []string
}

// AllowAnonymousProxy 判断一次未登录的代理请求是否放行。任何一项不满足都拒绝。
func (g *DhProxyGuard) AllowAnonymousProxy(dsId int64, method string) bool {
	if !g.AllowAnonymous {
		return false
	}

	if len(g.AnonymousDatasourceIds) > 0 {
		matched := false
		for _, id := range g.AnonymousDatasourceIds {
			if id == dsId {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(g.AnonymousMethods) > 0 {
		method = strings.ToUpper(strings.TrimSpace(method))
		matched := false
		for _, m := range g.AnonymousMethods {
			if strings.ToUpper(strings.TrimSpace(m)) == method {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}
