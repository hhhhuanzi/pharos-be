package tracesummary

import (
	"regexp"
	"sort"
	"strings"
)

// ListType 是列表「类型」列的短标记，展示文案在前端 locale 里，两侧字符串必须一致。
type ListType string

const (
	TypeWeb      ListType = "web"
	TypeSQL      ListType = "sql"
	TypeRedis    ListType = "redis"
	TypeCache    ListType = "cache"
	TypeMQ       ListType = "mq"
	TypeRPC      ListType = "rpc"
	TypeNacos    ListType = "nacos"
	TypeInternal ListType = "internal"
	TypeUnknown  ListType = ""
)

const httpMethods = `GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS`

var (
	httpWithPathRe = regexp.MustCompile(`(?i)^(` + httpMethods + `)\s+/`)
	httpPrefixRe   = regexp.MustCompile(`(?i)^HTTP\s+(` + httpMethods + `)\b`)
	httpSWColonRe  = regexp.MustCompile(`(?i)^(` + httpMethods + `):/`)
	httpSWBraceRe  = regexp.MustCompile(`(?i)^\{(` + httpMethods + `)\}/`)
	// 裸 HTTP 动词（含 GET）。Redis 的 GET 作为 root 很少见，产品已确认这些是 HTTP。
	bareHTTPRe = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)$`)
	sqlPrefix  = regexp.MustCompile(`(?i)^(SELECT|INSERT|UPDATE|CREATE|ALTER|DROP|WITH|SHOW|EXPLAIN)\b`)
	// `DELETE FROM t` / `DELETE t` 是 SQL；裸 DELETE 与 `DELETE /path` 是 HTTP（先判）。
	sqlDeleteRe = regexp.MustCompile(`(?i)^DELETE\s+(FROM\b|[^\s/])`)
	// 生成帧（Java lambda / CGLIB / JDK proxy / Kotlin lambda）：只匹配结构特征，不匹配业务类名。
	internalFrameRe = regexp.MustCompile(`\$\$Lambda(?:\$|\b)|\$\$[A-Za-z][0-9A-Za-z_]*\$\$|\$Proxy[0-9]+|\$lambda\$`)

	nacosPathTailRe    = regexp.MustCompile(`/nacos([/?#]|$)`)
	nacosOpenAPIPathRe = regexp.MustCompile(`(?i)^/v1/(cs|ns|auth)(/|$)`)
	port8848Re         = regexp.MustCompile(`:8848\b`)

	whitespaceRe = regexp.MustCompile(`\s+`)
)

var redisSystems = map[string]struct{}{"redis": {}, "keydb": {}, "valkey": {}}

var redisCommands = map[string]struct{}{
	"PING": {}, "INFO": {}, "SET": {}, "MSET": {}, "SETEX": {}, "SETNX": {}, "GETSET": {},
	"HGET": {}, "HSET": {}, "HMGET": {}, "HGETALL": {}, "HDEL": {}, "HEXISTS": {},
	"DEL": {}, "UNLINK": {}, "EXISTS": {}, "EXPIRE": {}, "TTL": {}, "INCR": {}, "DECR": {},
	"LPUSH": {}, "RPUSH": {}, "LPOP": {}, "RPOP": {}, "LRANGE": {},
	"SADD": {}, "SMEMBERS": {}, "ZADD": {}, "ZRANGE": {}, "PUBLISH": {}, "SUBSCRIBE": {},
}

func listTypeFromKind(kind SpanKind, tags []Tag) ListType {
	switch kind {
	case KindNacos:
		return TypeNacos
	case KindWeb:
		return TypeWeb
	case KindDB:
		return TypeSQL
	case KindCache:
		system := strings.ToLower(TagValue(tags, dbSystemKeys))
		if _, ok := redisSystems[system]; ok {
			return TypeRedis
		}
		return TypeCache
	case KindMessaging:
		return TypeMQ
	case KindRPC:
		return TypeRPC
	default:
		return TypeUnknown
	}
}

// IsInternalFrame 判定生成帧的操作名（不匹配普通方法名）。
func IsInternalFrame(operationName string) bool {
	if operationName == "" {
		return false
	}
	return internalFrameRe.MatchString(operationName)
}

// ResolveFromOperation 是没有语义属性时的协议动词兜底。
//
// HTTP 动词（含裸 GET）→ web；SQL 动词 → sql；Redis 命令（GET 除外）→ redis。生成帧不是协议
// 动词，在这里保持空（由 ResolveTraceKind 标成 internal）。裸 process / consume 不是 MQ。
func ResolveFromOperation(operationName string) ListType {
	name := strings.TrimSpace(operationName)
	if name == "" {
		return TypeUnknown
	}
	if httpWithPathRe.MatchString(name) || httpPrefixRe.MatchString(name) || httpSWColonRe.MatchString(name) || httpSWBraceRe.MatchString(name) {
		return TypeWeb
	}
	if bareHTTPRe.MatchString(name) {
		return TypeWeb
	}
	if sqlPrefix.MatchString(name) || sqlDeleteRe.MatchString(name) {
		return TypeSQL
	}
	command := strings.ToUpper(whitespaceRe.Split(name, 2)[0])
	if _, ok := redisCommands[command]; ok {
		return TypeRedis
	}
	return TypeUnknown
}

// KindSpan 是 ResolveTraceKind 需要的最小 span 视图。
type KindSpan struct {
	SpanID        string
	OperationName string
	StartTime     int64
	Tags          []Tag
	Parents       []SpanRef
}

// findKindRootSpan 与前端 listType.ts 的 findRootSpan 一致：这里不校验 traceID（调用方传进来的
// 就是同一条 trace 的 span），没有 spanID 可用时退回第一个 span。
func findKindRootSpan(spans []KindSpan) int {
	if len(spans) == 0 {
		return -1
	}
	ids := make(map[string]struct{}, len(spans))
	for _, span := range spans {
		if span.SpanID != "" {
			ids[span.SpanID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return 0
	}

	root := -1
	for i, span := range spans {
		hasInternalParent := false
		for _, parent := range span.Parents {
			if _, ok := ids[parent.SpanID]; ok {
				hasInternalParent = true
				break
			}
		}
		if hasInternalParent {
			continue
		}
		if root == -1 || span.StartTime < spans[root].StartTime {
			root = i
		}
	}
	if root == -1 {
		return 0
	}
	return root
}

// ResolveTraceKind 是 trace 级类型，扫全部 span 而不只是 root：
//
//  1. 任一 span 的属性（root 先看，其余按开始时间）——db / redis / http / mq / rpc / nacos；
//  2. root 只是生成帧时，用子 span 的操作动词；一个子 span 都没有则 internal；
//  3. root 操作名的协议动词（裸 GET → web）。
func ResolveTraceKind(spans []KindSpan) ListType {
	rootIdx := findKindRootSpan(spans)
	if rootIdx < 0 {
		return TypeUnknown
	}
	root := spans[rootIdx]

	rest := make([]KindSpan, 0, len(spans))
	for i, span := range spans {
		if i == rootIdx {
			continue
		}
		rest = append(rest, span)
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].StartTime < rest[j].StartTime })

	if fromTags := listTypeFromKind(ResolveSpanKind(root.Tags), root.Tags); fromTags != TypeUnknown {
		return fromTags
	}
	for _, span := range rest {
		if fromTags := listTypeFromKind(ResolveSpanKind(span.Tags), span.Tags); fromTags != TypeUnknown {
			return fromTags
		}
	}

	if IsInternalFrame(root.OperationName) {
		for _, span := range rest {
			if fromOp := ResolveFromOperation(span.OperationName); fromOp != TypeUnknown {
				return fromOp
			}
		}
		return TypeInternal
	}

	return ResolveFromOperation(root.OperationName)
}
