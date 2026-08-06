// Package audit 实现 Pharos 的操作审计日志能力（dh 二开，官方夜莺不具备）。
//
// # 落地范围（ROADMAP.md G-11）
//
//   - P0：高危操作（角色/权限/数据源）最小化留痕 + 告警降级为 WARN 日志
//   - P1：通用审计中间件，覆盖 alert_rule/datasource/user/role/board/notify/busi_group 等管理路由
//   - P2：FE 查询页（src/pages/auditLog/）
//
// # 风险等级
//
// 存库字段 risk_level：high / medium / low。划分见 routes_map.go 注释。
// 列表另落 module + action_label 业务文案，避免 FE 再维护一份路径映射。
//
// # 通知能力的决策说明
//
// 高危操作原计划触发一次即时通知。调研后发现仓库里可复用的发送能力
// （alert/sender、alert/dispatch）都强耦合告警事件/通知规则/渠道配置这套模型，
// 从 gin 中间件里直接调用的改造成本和引入的耦合面，超过了这条通知本身的价值。
// 按照"不为了这条通知重新设计一套通知渠道配置"的原则，本期把高危操作的通知
// 降级为一条 WARN 级别的结构化日志（logger.Warningf，见 middleware.go），
// 运维可以用现有日志系统或 Pharos 自身的日志类告警规则订阅这条日志。
// 后续如果要接通知，落点就是 Middleware 函数里 `if riskLevel == RiskHigh` 分支，加一次调用即可。
package audit
