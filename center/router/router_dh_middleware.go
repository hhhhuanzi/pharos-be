package router

// 二开（dh）：把官方 rt.perm 的「必须有这一个权限点」放宽成「列出的权限点有任意一个即可」。
//
// 同一个接口被多个入口页复用时需要它：链路详情既在 /trace/explorer 打开，也在服务详情页的链路
// tab 里打开（前端把同一个 TraceExplorer 内嵌过去），两个页面的权限点不同。用官方 rt.perm 只能
// 挑一个，会把本来就能进另一个页面的角色挡在门外。

import (
	"net/http"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

// dhPermAny 除了 OR，其余口径与 rt.perm 完全一致：依赖 rt.user() 已把 *models.User 放进
// context，Admin 由 CheckPerm 内部直通，查询出错走 ginx.Dangerous，判不过 403 forbidden。
func (rt *Router) dhPermAny(operations ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		me := c.MustGet("user").(*models.User)

		granted, err := dhAnyPermGranted(operations, func(operation string) (bool, error) {
			return me.CheckPerm(rt.Ctx, operation)
		})
		ginx.Dangerous(err)

		if !granted {
			ginx.Bomb(http.StatusForbidden, "forbidden")
		}

		c.Next()
	}
}

// dhAnyPermGranted 是 dhPermAny 的判定本体，抽出来是为了能脱离 DB 覆盖分支。
//
// 空权限点列表返回 false：调用方写漏了参数时接口应当关死，而不是对所有登录用户敞开。
func dhAnyPermGranted(operations []string, checkPerm func(operation string) (bool, error)) (bool, error) {
	for _, operation := range operations {
		if operation == "" {
			continue
		}
		can, err := checkPerm(operation)
		if err != nil {
			return false, err
		}
		if can {
			return true, nil
		}
	}
	return false, nil
}
