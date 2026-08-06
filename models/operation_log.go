package models

import (
	"github.com/ccfos/nightingale/v6/pkg/ctx"

	"gorm.io/gorm"
)

// OperationLog 记录管理后台的写操作（增删改），用于事后审计。
//
// dh 二开：官方夜莺没有这块能力，本表与相关查询函数是新增文件，落库逻辑见
// pkg/dh/audit（gin 中间件），清理任务见 cron/clean_operation_log.go。
type OperationLog struct {
	Id int64 `json:"id" gorm:"primaryKey"`
	// CreateAt 操作发生时间（unix 秒）
	CreateAt int64 `json:"create_at" gorm:"column:create_at;not null;default:0;index:idx_operation_log_create_at"`
	// UserId/Username 操作人；未登录场景（理论上不应发生，代理已强制登录）落 0/""
	UserId   int64  `json:"user_id" gorm:"column:user_id;not null;default:0"`
	Username string `json:"username" gorm:"column:username;type:varchar(64);not null;default:'';index:idx_operation_log_username"`

	RemoteAddr string `json:"remote_addr" gorm:"column:remote_addr;type:varchar(64);not null;default:''"`
	UserAgent  string `json:"user_agent" gorm:"column:user_agent;type:varchar(512);not null;default:''"`
	Method     string `json:"method" gorm:"column:method;type:varchar(16);not null;default:''"`
	// Path 路由模板（gin FullPath，如 /api/n9e/user/:id/profile），不是带真实 id 的原始 URL
	Path string `json:"path" gorm:"column:path;type:varchar(255);not null;default:''"`
	// ObjectType 对象类型，如 alert_rule/datasource/user/role/board/notify_rule/busi_group，
	// 未命中静态路由映射时落 "other"（保证审计不因为漏配置映射而静默丢记录）
	ObjectType string `json:"object_type" gorm:"column:object_type;type:varchar(64);not null;default:'';index:idx_operation_log_object_type"`
	// ObjectId 从路由参数（:id/:bid/:arid 等）里取到的对象 id，多个参数用逗号拼接；拿不到则为空
	ObjectId string `json:"object_id" gorm:"column:object_id;type:varchar(128);not null;default:''"`
	// Action create/update/delete，按 HTTP method 粗粒度推断（POST=create，PUT/PATCH=update，DELETE=delete）
	Action string `json:"action" gorm:"column:action;type:varchar(16);not null;default:''"`
	// Module 业务模块中文名（如「用户管理」「数据源」），由 routes_map 写入，供列表直接展示
	Module string `json:"module" gorm:"column:module;type:varchar(64);not null;default:''"`
	// ActionLabel 业务动作中文名（如「修改用户资料」「删除数据源」），由 routes_map 按 method 写入
	ActionLabel string `json:"action_label" gorm:"column:action_label;type:varchar(128);not null;default:''"`
	// RequestBody 脱敏后的请求体 JSON（password/token/secret 等字段已替换为占位符），超长会被截断
	RequestBody string `json:"request_body" gorm:"column:request_body;type:text"`
	StatusCode  int    `json:"status_code" gorm:"column:status_code;not null;default:0"`
	// RiskLevel 风险等级：high / medium / low（英文枚举存库，FE 做 i18n）
	RiskLevel string `json:"risk_level" gorm:"column:risk_level;type:varchar(16);not null;default:'low';index:idx_operation_log_risk_level"`
}

func (o *OperationLog) TableName() string {
	return "operation_log"
}

func OperationLogInsert(ctx *ctx.Context, o *OperationLog) error {
	return Insert(ctx, o)
}

// operationLogSession 构造列表/计数共用的过滤条件。
// userId > 0 时表示按操作人 id 收窄（用于"只看自己"场景或不具备 /audit-log 权限点的普通用户）。
func operationLogSession(ctx *ctx.Context, userId int64, username, objectType, riskLevel string, stime, etime int64) *gorm.DB {
	session := DB(ctx).Model(&OperationLog{})

	if userId > 0 {
		session = session.Where("user_id = ?", userId)
	} else if username != "" {
		session = session.Where("username like ?", "%"+username+"%")
	}

	if objectType != "" {
		session = session.Where("object_type = ?", objectType)
	}

	if riskLevel != "" {
		session = session.Where("risk_level = ?", riskLevel)
	}

	if stime > 0 {
		session = session.Where("create_at >= ?", stime)
	}
	if etime > 0 {
		session = session.Where("create_at <= ?", etime)
	}

	return session
}

func OperationLogTotal(ctx *ctx.Context, userId int64, username, objectType, riskLevel string, stime, etime int64) (int64, error) {
	return Count(operationLogSession(ctx, userId, username, objectType, riskLevel, stime, etime))
}

func OperationLogGets(ctx *ctx.Context, userId int64, username, objectType, riskLevel string, stime, etime int64, limit, offset int) ([]OperationLog, error) {
	var lst []OperationLog
	err := operationLogSession(ctx, userId, username, objectType, riskLevel, stime, etime).
		Order("create_at desc, id desc").Limit(limit).Offset(offset).Find(&lst).Error
	return lst, err
}
