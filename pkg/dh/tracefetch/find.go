package tracefetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FindQuery 是 api_v3 FindTraces 的查询条件，字段与前端 TraceSearchParams / buildFindTracesParams
// （src/dh/trace/adapters/jaeger.ts）一一对应。
type FindQuery struct {
	// Service 必填：判权靠的就是这个参数（服务对当前用户可见 ⇒ 整条 trace 放行），空值请求判不了权。
	Service      string
	StartTimeMin time.Time
	StartTimeMax time.Time
	Operation    string
	// DurationMin / DurationMax 是 Go duration 字符串（前端表单产出 "1.5s" 这种），原样透传。
	DurationMin string
	DurationMax string
	NumTraces   int
	// Attributes 是属性过滤（富化的 hint、环境收窄都走这里），透传成 URL 编码的 JSON string map。
	// 上游会把它下推到 resource / process tags，多个 key 之间是 AND，所以 env 与 hint 可以并存。
	// 环境维度请用 WithEnv 拼装，不要自己写属性名（见 env.go）。
	Attributes map[string]string
}

// findTracesParams 复刻前端 buildFindTracesParams 的参数名与取舍。
//
// 参数名用 api_v3 原始的 snake_case：这是 api_v3 proto 注释里一直在用的写法，所有已发布的
// Jaeger v2.x 都认；很近期的 Jaeger 才把 camelCase 定为「规范」名并把 snake_case 标为兼容别名。
//
// 结果数量字段在 proto 里从 num_traces 改名成 search_depth，这里只打 /api/v3/traces，沿用历史的
// num_traces。
//
// 注意别按「未知参数会被拒绝」来推断上游认不认某个参数：实测塞一个虚构的 query.bogus_param=1，
// 上游返回 HTTP 200 并照常给数据，未知 query 参数是被静默忽略的。所以「发错名字」不会报错，只会
// 悄悄不生效 —— 参数名是否生效必须靠结果差异来验证（例如 query.attributes 带错值返回 0 条、带对
// 值返回数据），不能靠「没报错」当作生效。
func (q FindQuery) findTracesParams() (url.Values, error) {
	values := url.Values{}
	values.Set("query.service_name", q.Service)
	values.Set("query.start_time_min", q.StartTimeMin.UTC().Format(time.RFC3339Nano))
	values.Set("query.start_time_max", q.StartTimeMax.UTC().Format(time.RFC3339Nano))
	if q.Operation != "" {
		values.Set("query.operation_name", q.Operation)
	}
	if q.DurationMin != "" {
		values.Set("query.duration_min", q.DurationMin)
	}
	if q.DurationMax != "" {
		values.Set("query.duration_max", q.DurationMax)
	}
	if q.NumTraces > 0 {
		values.Set("query.num_traces", strconv.Itoa(q.NumTraces))
	}
	if len(q.Attributes) > 0 {
		encoded, err := json.Marshal(q.Attributes)
		if err != nil {
			return nil, err
		}
		values.Set("query.attributes", string(encoded))
	}
	return values, nil
}

// FindTraces 返回上游 /api/v3/traces 的原始响应体（一批 trace 的全量 span，OTLP 形状）。
//
// 与 GetTrace 一样，返回的 error 只描述上游的失败方式、不携带响应体：调用方会把它写进接口错误
// 信息，而响应体里就是尚未判权的 trace 内容。
func (c *JaegerClient) FindTraces(ctx context.Context, query FindQuery) ([]byte, error) {
	if strings.TrimSpace(query.Service) == "" {
		return nil, fmt.Errorf("service is required")
	}
	values, err := query.findTracesParams()
	if err != nil {
		return nil, err
	}

	u := *c.target
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v3/traces"
	u.RawQuery = values.Encode()

	return c.getJSON(ctx, u.String())
}
