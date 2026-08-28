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
	// Attributes 是属性过滤（富化的 hint 走这里），透传成 URL 编码的 JSON string map。
	Attributes map[string]string
}

// findTracesParams 复刻前端 buildFindTracesParams 的参数名与取舍。
//
// 参数名用 api_v3 原始的 snake_case：这是 api_v3 proto 注释里一直在用的写法，所有已发布的
// Jaeger v2.x 都认；很近期的 Jaeger 才把 camelCase 定为「规范」名并把 snake_case 标为兼容别名。
//
// 结果数量字段在 proto 里从 num_traces 改名成 search_depth，而 grpc-gateway 会直接拒绝未知的
// query 参数，所以不能两个都发。这里只打 /api/v3/traces，沿用历史的 num_traces。
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
