package tracesummary

import (
	"net/url"
	"strings"
)

// Tag 是 span / process 上的一个属性。Value 已经按前端 otlpValueToPlain + String() 的口径折成
// 字符串（见 value.go），因为口径判定只按字符串比较，不需要保留原始类型。
type Tag struct {
	Key   string
	Value string
}

// 语义属性键，与前端 summaryFields.ts 的常量一一对应。
var (
	httpMethodKeys      = []string{"http.method", "http.request.method"}
	httpPathKeys        = []string{"http.route", "http.target", "url.path", "http.path", "url", "http.url", "url.full"}
	httpStatusKeys      = []string{"http.status_code", "http.response.status_code"}
	dbSystemKeys        = []string{"db.system", "db.type"}
	messagingSystemKeys = []string{"messaging.system"}
	sqlKeys             = []string{"db.statement", "db.query.text", "sql"}
	nacosURLKeys        = []string{"url.full", "http.url", "http.target", "url.path", "http.path", "url"}
)

// TagValue 返回 keys 里第一个有值的属性。空串与只有空白的值视为没有值。
func TagValue(tags []Tag, keys []string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range keys {
		for _, tag := range tags {
			if tag.Key != key {
				continue
			}
			if text := strings.TrimSpace(tag.Value); text != "" {
				return text
			}
			// 前端 tags.find 只取第一个同名项；同名再出现也不再看。
			break
		}
	}
	return ""
}

func hasNamespace(tags []Tag, ns string) bool {
	prefix := ns + "."
	for _, tag := range tags {
		if tag.Key == ns || strings.HasPrefix(tag.Key, prefix) {
			return true
		}
	}
	return false
}

func skyWalkingLayer(tags []Tag) string {
	return strings.ToUpper(strings.TrimSpace(TagValue(tags, []string{"layer"})))
}

func hasHTTPURL(tags []Tag) bool {
	raw := TagValue(tags, nacosURLKeys)
	lower := strings.ToLower(raw)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func hasHTTPEvidence(tags []Tag) bool {
	return hasNamespace(tags, "http") || TagValue(tags, httpMethodKeys) != "" || TagValue(tags, httpStatusKeys) != "" || hasHTTPURL(tags)
}

func hasNacosInPath(pathname string) bool {
	path := strings.ToLower(pathname)
	if strings.Contains(path, "/nacos/") {
		return true
	}
	return nacosPathTailRe.MatchString(path)
}

// isNacosPathOnDefaultPort：8848 上的 Nacos OpenAPI 可能省略 /nacos 上下文路径。
func isNacosPathOnDefaultPort(pathname string) bool {
	return hasNacosInPath(pathname) || nacosOpenAPIPathRe.MatchString(pathname)
}

func isNacosURL(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if hasNacosInPath(lower) {
		return true
	}

	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if u, err := url.Parse(text); err == nil {
			port := u.Port()
			if port == "" {
				if u.Scheme == "https" {
					port = "443"
				} else {
					port = "80"
				}
			}
			if port == "8848" && isNacosPathOnDefaultPort(u.Path) {
				return true
			}
		}
	}
	return port8848Re.MatchString(lower) && strings.Contains(lower, "nacos")
}

// IsNacosSpan 判定 Nacos 客户端 span（配置长轮询 / 服务发现）。在通用 HTTP 之前判，避免
// `http.request.method` + `url.full=…/nacos/…` 被打成 WEB。
func IsNacosSpan(tags []Tag) bool {
	if len(tags) == 0 {
		return false
	}
	if u := TagValue(tags, nacosURLKeys); u != "" && isNacosURL(u) {
		return true
	}
	if strings.Contains(strings.ToLower(TagValue(tags, []string{"thread.name"})), "nacos") {
		return true
	}
	if TagValue(tags, []string{"server.port"}) == "8848" && hasHTTPEvidence(tags) {
		return true
	}
	return false
}

// IsMessagingSpan 判定 OTel messaging span。`span.kind` consumer/producer 只有在同时带
// `messaging.*` 属性时才算；裸操作名（process / consume）不算，那是 listType 的事。
func IsMessagingSpan(tags []Tag) bool {
	if len(tags) == 0 {
		return false
	}
	if TagValue(tags, messagingSystemKeys) != "" {
		return true
	}
	if skyWalkingLayer(tags) == "MQ" {
		return true
	}
	kind := strings.ToLower(TagValue(tags, []string{"span.kind"}))
	if kind != "consumer" && kind != "producer" {
		return false
	}
	return hasNamespace(tags, "messaging")
}

// SpanKind 是瀑布图行图标的语义分类，取值与前端 SpanKindIcon 一致。
type SpanKind string

const (
	KindWeb       SpanKind = "web"
	KindDB        SpanKind = "db"
	KindCache     SpanKind = "cache"
	KindMessaging SpanKind = "messaging"
	KindRPC       SpanKind = "rpc"
	KindNacos     SpanKind = "nacos"
	KindInternal  SpanKind = "internal"
)

var cacheSystems = map[string]struct{}{
	"redis": {}, "memcached": {}, "memcache": {}, "cache": {}, "keydb": {}, "valkey": {},
}

// ResolveSpanKind 优先级：cache → db → nacos → http → messaging → rpc → internal。
func ResolveSpanKind(tags []Tag) SpanKind {
	dbSystem := strings.ToLower(TagValue(tags, dbSystemKeys))
	if dbSystem != "" {
		if _, ok := cacheSystems[dbSystem]; ok {
			return KindCache
		}
	}

	layer := skyWalkingLayer(tags)
	if layer == "CACHE" {
		return KindCache
	}
	if hasNamespace(tags, "db") || layer == "DATABASE" {
		return KindDB
	}
	if IsNacosSpan(tags) {
		return KindNacos
	}
	if hasNamespace(tags, "http") || layer == "HTTP" {
		return KindWeb
	}
	if IsMessagingSpan(tags) {
		return KindMessaging
	}
	if hasNamespace(tags, "rpc") || layer == "RPC" || layer == "RPCFRAMEWORK" {
		return KindRPC
	}
	if TagValue(tags, httpMethodKeys) != "" || TagValue(tags, httpStatusKeys) != "" || hasHTTPURL(tags) {
		return KindWeb
	}
	return KindInternal
}

// ResolveRootInterface 是列表「接口名称」：method + path，有 SQL 时接在后面；都没有时退回操作名。
func ResolveRootInterface(operationName string, tags []Tag) string {
	method := TagValue(tags, httpMethodKeys)
	rawPath := TagValue(tags, httpPathKeys)
	path := ""
	if rawPath != "" {
		path = extractPath(rawPath)
	}
	sql := TagValue(tags, sqlKeys)

	parts := make([]string, 0, 2)
	if method != "" {
		parts = append(parts, method)
	}
	if path != "" {
		parts = append(parts, path)
	}
	methodPath := strings.Join(parts, " ")

	switch {
	case methodPath != "" && sql != "":
		return methodPath + " " + sql
	case methodPath != "":
		return methodPath
	case sql != "":
		return sql
	default:
		return operationName
	}
}

func extractPath(raw string) string {
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}
