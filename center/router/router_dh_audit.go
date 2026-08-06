package router

import (
	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/dh/audit"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

// dh 二开：操作审计日志能力（ROADMAP.md G-11）。
//
// dhOperationLog 是官方 router.go 唯一要挂载的一行入口
// （pages.Use(rt.dhOperationLog())），业务实现全部在 pkg/dh/audit。
func (rt *Router) dhOperationLog() gin.HandlerFunc {
	return audit.Middleware(rt.Ctx)
}

// auditLogList 分页查询操作审计日志。
//
// 权限口径：路由本身只要求登录（rt.auth()+rt.user()，不挂 rt.perm()），因为要支持
// "没有 /audit-log 权限点的普通用户也能查看自己的操作记录"。有 /audit-log 权限点
// 的用户（默认只有 Admin，见 center/cconf/ops.go）可以按任意操作人筛选；没有该权限点
// 或显式传 mine=true 的请求，一律强制按当前登录用户收窄，忽略 username 筛选参数。
func (rt *Router) auditLogList(c *gin.Context) {
	user := c.MustGet("user").(*models.User)

	hasPerm, err := user.CheckPerm(rt.Ctx, "/audit-log")
	ginx.Dangerous(err)

	mine := ginx.QueryBool(c, "mine", false)

	limit := ginx.QueryInt(c, "limit", 30)
	offset := ginx.Offset(c, limit)

	var scopedUserId int64
	username := ginx.QueryStr(c, "username", "")
	if !hasPerm || mine {
		scopedUserId = user.Id
		username = "" // 按 user_id 收窄时忽略 username 筛选，避免"看似能查别人、其实被静默改写"的困惑
	}

	objectType := ginx.QueryStr(c, "object_type", "")
	riskLevel := ginx.QueryStr(c, "risk_level", "")

	stime := ginx.QueryInt64(c, "stime", 0)
	etime := ginx.QueryInt64(c, "etime", 0)

	total, err := models.OperationLogTotal(rt.Ctx, scopedUserId, username, objectType, riskLevel, stime, etime)
	ginx.Dangerous(err)

	list, err := models.OperationLogGets(rt.Ctx, scopedUserId, username, objectType, riskLevel, stime, etime, limit, offset)
	ginx.Dangerous(err)

	// 旧数据兼容：早期 is_high_risk 布尔字段升级后，risk_level / module / action_label
	// 可能为空；列表返回前补默认值，避免 FE 看到空白。
	for i := range list {
		if list[i].RiskLevel == "" {
			list[i].RiskLevel = audit.RiskLow
		}
		if list[i].Module == "" {
			list[i].Module = list[i].ObjectType
		}
		if list[i].ActionLabel == "" {
			list[i].ActionLabel = list[i].Action
		}
	}

	ginx.NewRender(c).Data(gin.H{
		"list":  list,
		"total": total,
	}, nil)
}
