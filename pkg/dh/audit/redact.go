package audit

import (
	"encoding/json"
	"strings"
)

// maxStoredBodyLen 落库前的请求体长度上限（字节），避免超大 payload（如批量导入）
// 把审计表撑成大对象；超过时脱敏后再整体截断并加后缀提示。
const maxStoredBodyLen = 8192

const redactedPlaceholder = "***REDACTED***"

// sensitiveKeySubstrings 命中即整值替换，大小写不敏感、按"包含"而非"完全相等"匹配，
// 这样 password/old_password/new_password、access_token/refresh_token 等变体都能覆盖到。
var sensitiveKeySubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"apikey",
	"api_key",
	"credential",
	"private_key",
	"authorization",
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// redactValue 递归脱敏：对象逐 key 判断，数组逐元素递归，其余类型原样返回。
func redactValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, vv := range val {
			if isSensitiveKey(k) {
				val[k] = redactedPlaceholder
			} else {
				val[k] = redactValue(vv)
			}
		}
		return val
	case []interface{}:
		for i, vv := range val {
			val[i] = redactValue(vv)
		}
		return val
	default:
		return v
	}
}

// redactRequestBody 对请求体做脱敏 + 截断后返回可安全落库的字符串。
// 非 JSON（或空）body 原样按字符串截断处理——写操作绝大多数走 JSON，非 JSON 场景
// 保底不落敏感信息的风险很低，但仍然截断避免超长。
func redactRequestBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	var parsed interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return truncate(string(raw), maxStoredBodyLen)
	}

	redacted := redactValue(parsed)
	out, err := json.Marshal(redacted)
	if err != nil {
		// 理论上不会发生（刚 Unmarshal 成功的结构再 Marshal 回去），保底降级为占位符，
		// 绝不能因为序列化失败就把原始（可能含敏感字段）的 raw 落库
		return redactedPlaceholder
	}

	return truncate(string(out), maxStoredBodyLen)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
