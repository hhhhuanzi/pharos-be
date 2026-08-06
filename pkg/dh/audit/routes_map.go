// Package audit 是 dh 二开的操作审计能力：gin 中间件 + 路由 ->
// {对象类型, 风险等级, 业务模块, 动作文案} 的静态映射。官方 router.go 只挂一行中间件
// （见 center/router/router.go 的 pages.Use(rt.dhOperationLog())），业务全部在本包。
package audit

import (
	"net/http"
	"strings"
)

// 风险等级英文枚举（落库值），FE 做 i18n：高危 / 中危 / 低危。
const (
	RiskHigh   = "high"
	RiskMedium = "medium"
	RiskLow    = "low"
)

// routeRule 按 c.FullPath()（形如 /api/n9e/user/:id/profile，携带路由模板而非真实
// 参数值）做子串匹配，Contains 内的子串必须全部命中才算匹配。列表按从具体到笼统排序，
// matchRoute 命中第一条即返回，所以"更具体"的规则必须排在"更笼统"的规则之前
// （例如 /busi-group/:id/alert-rules 要在通用 busi_group 规则之前先被 alert_rule 规则截获）。
//
// 风险等级划分：
//   - high：角色/权限变更、用户角色绑定、密码、业务组成员变更、数据源凭据相关写操作
//   - medium：告警规则/通知策略/大盘/值班/业务组/团队等业务配置的增删改
//   - low：个人账号、组件/嵌入产品等其它被审计的写操作
type routeRule struct {
	Contains     []string
	ObjectType   string
	RiskLevel    string
	Module       string
	ActionCreate string
	ActionUpdate string
	ActionDelete string
}

func (r routeRule) actionLabel(method string) string {
	switch method {
	case http.MethodPost:
		if r.ActionCreate != "" {
			return r.ActionCreate
		}
		return "新增"
	case http.MethodPut, http.MethodPatch:
		if r.ActionUpdate != "" {
			return r.ActionUpdate
		}
		return "修改"
	case http.MethodDelete:
		if r.ActionDelete != "" {
			return r.ActionDelete
		}
		return "删除"
	default:
		return method
	}
}

var rules = []routeRule{
	// 业务组成员变更（P-01 排查里点名的"裸奔"接口之一）—— 高危
	{
		Contains: []string{"/busi-group", "/members"}, ObjectType: "busi_group_member", RiskLevel: RiskHigh,
		Module: "业务组", ActionCreate: "添加业务组成员", ActionUpdate: "更新业务组成员", ActionDelete: "移除业务组成员",
	},

	// 告警资产 —— 中危
	{
		Contains: []string{"/alert-rule"}, ObjectType: "alert_rule", RiskLevel: RiskMedium,
		Module: "告警规则", ActionCreate: "新增告警规则", ActionUpdate: "更新告警规则", ActionDelete: "删除告警规则",
	},
	{
		Contains: []string{"/alert-mute"}, ObjectType: "alert_mute", RiskLevel: RiskMedium,
		Module: "屏蔽规则", ActionCreate: "新增屏蔽规则", ActionUpdate: "更新屏蔽规则", ActionDelete: "删除屏蔽规则",
	},
	{
		Contains: []string{"/alert-subscribe"}, ObjectType: "alert_subscribe", RiskLevel: RiskMedium,
		Module: "订阅规则", ActionCreate: "新增订阅规则", ActionUpdate: "更新订阅规则", ActionDelete: "删除订阅规则",
	},
	{
		Contains: []string{"/recording-rule"}, ObjectType: "recording_rule", RiskLevel: RiskMedium,
		Module: "记录规则", ActionCreate: "新增记录规则", ActionUpdate: "更新记录规则", ActionDelete: "删除记录规则",
	},

	// 自愈脚本/任务 —— 中危
	{
		Contains: []string{"/task-tpl"}, ObjectType: "job_tpl", RiskLevel: RiskMedium,
		Module: "自愈脚本", ActionCreate: "新增自愈脚本", ActionUpdate: "更新自愈脚本", ActionDelete: "删除自愈脚本",
	},
	{
		Contains: []string{"/tasks"}, ObjectType: "job_task", RiskLevel: RiskMedium,
		Module: "自愈任务", ActionCreate: "创建自愈任务", ActionUpdate: "更新自愈任务", ActionDelete: "删除自愈任务",
	},

	// 大盘 —— 中危
	{
		Contains: []string{"/board"}, ObjectType: "board", RiskLevel: RiskMedium,
		Module: "大盘", ActionCreate: "新增大盘", ActionUpdate: "更新大盘", ActionDelete: "删除大盘",
	},
	{
		Contains: []string{"/dashboard-annotation"}, ObjectType: "board", RiskLevel: RiskMedium,
		Module: "大盘", ActionCreate: "新增大盘标注", ActionUpdate: "更新大盘标注", ActionDelete: "删除大盘标注",
	},

	// 权限提升类：角色、用户资料（含改角色）、用户密码 —— 高危
	{
		Contains: []string{"/roles"}, ObjectType: "role", RiskLevel: RiskHigh,
		Module: "角色权限", ActionCreate: "新增角色", ActionUpdate: "更新角色", ActionDelete: "删除角色",
	},
	{
		Contains: []string{"/role/"}, ObjectType: "role", RiskLevel: RiskHigh,
		Module: "角色权限", ActionCreate: "新增角色", ActionUpdate: "更新角色权限", ActionDelete: "删除角色",
	},
	{
		Contains: []string{"/user/", "/profile"}, ObjectType: "user", RiskLevel: RiskHigh,
		Module: "用户管理", ActionUpdate: "修改用户资料",
	},
	{
		Contains: []string{"/user/", "/password"}, ObjectType: "user", RiskLevel: RiskHigh,
		Module: "用户管理", ActionUpdate: "修改用户密码",
	},
	{
		Contains: []string{"/users"}, ObjectType: "user", RiskLevel: RiskMedium,
		Module: "用户管理", ActionCreate: "新增用户", ActionUpdate: "更新用户", ActionDelete: "删除用户",
	},
	{
		Contains: []string{"/user-group"}, ObjectType: "user_group", RiskLevel: RiskMedium,
		Module: "团队", ActionCreate: "新增团队", ActionUpdate: "更新团队", ActionDelete: "删除团队",
	},

	// 业务组本身增删改（成员变更已在上面单独截获）—— 中危
	{
		Contains: []string{"/busi-group"}, ObjectType: "busi_group", RiskLevel: RiskMedium,
		Module: "业务组", ActionCreate: "新增业务组", ActionUpdate: "更新业务组", ActionDelete: "删除业务组",
	},

	// 数据源：能拿到凭据、能连去内网 —— 高危
	{
		Contains: []string{"/datasource"}, ObjectType: "datasource", RiskLevel: RiskHigh,
		Module: "数据源", ActionCreate: "新增数据源", ActionUpdate: "更新数据源", ActionDelete: "删除数据源",
	},

	// 通知 —— 中危
	{
		Contains: []string{"/notify-rule"}, ObjectType: "notify_rule", RiskLevel: RiskMedium,
		Module: "通知规则", ActionCreate: "新增通知规则", ActionUpdate: "更新通知规则", ActionDelete: "删除通知规则",
	},
	{
		Contains: []string{"/notification-rule"}, ObjectType: "notify_rule", RiskLevel: RiskMedium,
		Module: "通知规则", ActionCreate: "新增通知规则", ActionUpdate: "更新通知规则", ActionDelete: "删除通知规则",
	},
	{
		Contains: []string{"/notify-channel"}, ObjectType: "notify_channel", RiskLevel: RiskMedium,
		Module: "通知媒介", ActionCreate: "新增通知媒介", ActionUpdate: "更新通知媒介", ActionDelete: "删除通知媒介",
	},
	{
		Contains: []string{"/notification-channel"}, ObjectType: "notify_channel", RiskLevel: RiskMedium,
		Module: "通知媒介", ActionCreate: "新增通知媒介", ActionUpdate: "更新通知媒介", ActionDelete: "删除通知媒介",
	},
	{
		Contains: []string{"/message-template"}, ObjectType: "notify_template", RiskLevel: RiskMedium,
		Module: "消息模板", ActionCreate: "新增消息模板", ActionUpdate: "更新消息模板", ActionDelete: "删除消息模板",
	},
	{
		Contains: []string{"/notification-template"}, ObjectType: "notify_template", RiskLevel: RiskMedium,
		Module: "消息模板", ActionCreate: "新增消息模板", ActionUpdate: "更新消息模板", ActionDelete: "删除消息模板",
	},
	{
		Contains: []string{"/event-pipeline"}, ObjectType: "event_pipeline", RiskLevel: RiskMedium,
		Module: "事件管道", ActionCreate: "新增事件管道", ActionUpdate: "更新事件管道", ActionDelete: "删除事件管道",
	},

	// 值班：官方开源版当前没有独立的 /duty 路由，值班信息挂在 user-group 之下，
	// 这里预留前缀方便未来接入（当前不会命中，属于占位）—— 中危
	{
		Contains: []string{"/duty"}, ObjectType: "duty", RiskLevel: RiskMedium,
		Module: "值班", ActionCreate: "新增值班", ActionUpdate: "更新值班", ActionDelete: "删除值班",
	},

	// 集成中心 —— 低危
	{
		Contains: []string{"/components"}, ObjectType: "component", RiskLevel: RiskLow,
		Module: "组件", ActionCreate: "新增组件", ActionUpdate: "更新组件", ActionDelete: "删除组件",
	},
	{
		Contains: []string{"/embedded-product"}, ObjectType: "embedded_product", RiskLevel: RiskLow,
		Module: "嵌入产品", ActionCreate: "新增嵌入产品", ActionUpdate: "更新嵌入产品", ActionDelete: "删除嵌入产品",
	},

	// 自身账号操作 —— 低危
	{
		Contains: []string{"/self/"}, ObjectType: "self", RiskLevel: RiskLow,
		Module: "个人账号", ActionCreate: "个人账号操作", ActionUpdate: "修改个人资料", ActionDelete: "个人账号操作",
	},
}

// noisyContains：命中即跳过整条记录，不落库。覆盖各类"查询类"接口——它们虽然
// 是 POST，但语义是只读查询，不是管理操作，混进审计日志只会增加噪音。
var noisyContains = []string{
	"/ds-query",
	"/logs-query",
	"/log-query",
	"/query-range-batch",
	"/query-instant-batch",
	"/proxy/",
	"/es-",
	"/indices",
	"/fields",
	"/tdengine-",
	"/iotdb-",
	"/victorialogs-",
	"/loki-",
	"/db-databases",
	"/db-tables",
	"/db-desc-table",
	"/os-",
	"/sql-template",
	"/builtin-metric-promql",
	"/datasource/query",
	"/datasource/brief",
	"/datasource/list",
	"/datasource/desc",
	"/datasource/plugin/list",
	"/auth/", // 登录/登出/验证码/刷新 token，属认证事件而非管理操作，不在本期审计范围
}

// matchRoute 返回命中的规则与 matched。matched=false 时调用方应落一条
// object_type="other" 的记录而不是整条丢弃——静默漏记比"分类不准"更糟。
func matchRoute(path string) (routeRule, bool) {
	for _, r := range rules {
		hit := true
		for _, sub := range r.Contains {
			if !strings.Contains(path, sub) {
				hit = false
				break
			}
		}
		if hit {
			return r, true
		}
	}
	return routeRule{}, false
}

func isNoisy(path string) bool {
	for _, sub := range noisyContains {
		if strings.Contains(path, sub) {
			return true
		}
	}
	return false
}

func fallbackActionLabel(method string) string {
	switch method {
	case http.MethodPost:
		return "新增"
	case http.MethodPut, http.MethodPatch:
		return "修改"
	case http.MethodDelete:
		return "删除"
	default:
		return method
	}
}
