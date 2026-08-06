package audit

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ctx"

	"github.com/gin-gonic/gin"
	"github.com/toolkits/pkg/logger"
)

// maxReadBodyBytes 请求体最多读取的字节数，超过的部分直接丢弃不读（不影响下游
// handler，因为我们会把已读的部分原样塞回 c.Request.Body），避免大文件类接口
// 被审计中间件拖慢或占用过多内存。
const maxReadBodyBytes = 1 << 20 // 1MiB

// pathPrefix 与 center/router/router.go 里的 pagesPrefix 保持一致，仅用于把
// c.FullPath() 的公共前缀砍掉，让 routes_map.go 的匹配规则写得更短。
const pathPrefix = "/api/n9e"

func actionFromMethod(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// objectIdFromParams 拼接路由参数值（:id/:bid/:arid ...）。绝大多数路由只有一个
// 参数；少数嵌套路由（如 /busi-group/:id/board/:bid/clone）有多个，用逗号连接
// 保留全部上下文，而不是只取第一个丢信息。
func objectIdFromParams(c *gin.Context) string {
	if len(c.Params) == 0 {
		return ""
	}
	vals := make([]string, 0, len(c.Params))
	for _, p := range c.Params {
		if p.Value != "" {
			vals = append(vals, p.Value)
		}
	}
	return strings.Join(vals, ",")
}

// clientIP 优先使用 Cloudflare 注入的真实客户端 IP；无该请求头时保留 Gin
// 原有的 X-Forwarded-For / X-Real-IP / RemoteAddr 回退逻辑。
func clientIP(c *gin.Context) string {
	if ip := strings.TrimSpace(c.GetHeader("CF-Connecting-IP")); ip != "" {
		return ip
	}
	return c.ClientIP()
}

type currentUser struct {
	Id       int64
	Username string
}

// currentUserFrom 优先取 rt.user() 中间件塞进 context 的 *models.User；那个中间件
// 没跑到（比如挂了 rt.auth() 但没 rt.user() 的路由）时退化到 rt.auth() 塞的
// username 字符串；再退化到 "anonymous"（理论上不该发生，因为这些路由基本都要求
// 登录，兜底是为了不让审计中间件自己 panic）。
func currentUserFrom(c *gin.Context) currentUser {
	if v, ok := c.Get("user"); ok {
		if u, ok := v.(*models.User); ok && u != nil {
			return currentUser{Id: u.Id, Username: u.Username}
		}
	}
	if v, ok := c.Get("username"); ok {
		if name, ok := v.(string); ok && name != "" {
			return currentUser{Username: name}
		}
	}
	return currentUser{Username: "anonymous"}
}

// Middleware 是本能力唯一的挂载点：center/router/router.go 在 pages 路由组上加
// 一行 pages.Use(rt.dhOperationLog())（薄封装见 center/router/router_dh_audit.go），
// 官方文件只改这一行。
//
// 只处理 POST/PUT/DELETE/PATCH；GET 与命中 noisyContains 的路径直接放行、不记录。
// 落库放在 c.Next() 之后的 goroutine 里，避免拖慢主请求；goroutine 内部 recover，
// 任何 panic 都不应该影响已经返回给客户端的响应。
func Middleware(actx *ctx.Context) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete && method != http.MethodPatch {
			c.Next()
			return
		}

		fullPath := c.FullPath()
		if fullPath == "" {
			// 路由没匹配上（后续会 404），没有审计的必要
			c.Next()
			return
		}
		trimmedPath := strings.TrimPrefix(fullPath, pathPrefix)

		if isNoisy(trimmedPath) {
			c.Next()
			return
		}

		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxReadBodyBytes))
			c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		c.Next()

		rule, matched := matchRoute(trimmedPath)
		objectType := rule.ObjectType
		riskLevel := rule.RiskLevel
		module := rule.Module
		actionLabel := rule.actionLabel(method)
		if !matched {
			// 未命中静态映射不等于不重要——落一条 object_type=other，
			// 保证"漏配置映射"的后果是分类不准而不是静默丢记录。
			objectType = "other"
			riskLevel = RiskLow
			module = "其他"
			actionLabel = fallbackActionLabel(method)
		}
		if riskLevel == "" {
			riskLevel = RiskLow
		}

		user := currentUserFrom(c)
		entry := &models.OperationLog{
			CreateAt:    time.Now().Unix(),
			UserId:      user.Id,
			Username:    user.Username,
			RemoteAddr:  clientIP(c),
			UserAgent:   c.Request.UserAgent(),
			Method:      method,
			Path:        fullPath,
			ObjectType:  objectType,
			ObjectId:    objectIdFromParams(c),
			Action:      actionFromMethod(method),
			Module:      module,
			ActionLabel: actionLabel,
			RequestBody: redactRequestBody(bodyBytes),
			StatusCode:  c.Writer.Status(),
			RiskLevel:   riskLevel,
		}

		if riskLevel == RiskHigh {
			// P0：高危操作的通知能力暂未接现成发送渠道（见本包 doc.go 的落地说明），
			// 先用 WARN 级别结构化日志兜底，方便运维用日志系统/Pharos 自身的日志类
			// 告警规则监控这类操作；后续要接通知时只需在这里加一次调用。
			logger.Warningf("dh_audit high_risk_operation username=%s user_id=%d remote_addr=%s method=%s path=%s object_type=%s object_id=%s module=%s action_label=%s status_code=%d",
				entry.Username, entry.UserId, entry.RemoteAddr, entry.Method, entry.Path, entry.ObjectType, entry.ObjectId, entry.Module, entry.ActionLabel, entry.StatusCode)
		}

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("dh_audit: panic while inserting operation log: %v", r)
				}
			}()
			if err := models.OperationLogInsert(actx, entry); err != nil {
				logger.Errorf("dh_audit: failed to insert operation log: %v", err)
			}
		}()
	}
}
