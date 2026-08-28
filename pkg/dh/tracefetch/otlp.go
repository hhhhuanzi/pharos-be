package tracefetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// TraceServices 是从一条 trace 的 OTLP 响应里提取出的服务归属信息。
type TraceServices struct {
	// Names 是去重后的 service.name，按首次出现顺序。
	Names []string
	// ResourceSpans 是响应里 resourceSpans 条目总数。为 0 表示上游没返回任何数据（trace 不
	// 存在或已过期），与「有数据但都没带 service.name」是两种不同结论，判权时错误码不同。
	ResourceSpans int
}

// otlpKeyValue 只建模判权真正要读的字段：service.name 一定是 stringValue。
type otlpKeyValue struct {
	Key   string `json:"key"`
	Value struct {
		StringValue *string `json:"stringValue"`
	} `json:"value"`
}

type otlpResourceSpans struct {
	Resource struct {
		Attributes []otlpKeyValue `json:"attributes"`
	} `json:"resource"`
}

// otlpChunk 同时兼容 grpc-gateway 的 {"result": TracesData} 流式信封和裸 TracesData。
type otlpChunk struct {
	Result *struct {
		ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
	} `json:"result"`
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

const serviceNameAttr = "service.name"

// ExtractServices 从 api_v3 的 OTLP JSON 响应里提取服务名集合。
//
// 服务端流式 RPC 的 HTTP 网关会把响应写成多个换行分隔的 JSON 值，因此这里按 JSON 值逐个解码
// 到结尾，而不是把整个 body 当成单个对象。
func ExtractServices(body []byte) (TraceServices, error) {
	var out TraceServices
	if len(bytes.TrimSpace(body)) == 0 {
		return out, nil
	}

	seen := make(map[string]struct{})
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		var chunk otlpChunk
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return TraceServices{}, err
		}

		resourceSpans := chunk.ResourceSpans
		if chunk.Result != nil {
			resourceSpans = append(resourceSpans, chunk.Result.ResourceSpans...)
		}
		out.ResourceSpans += len(resourceSpans)

		for _, rs := range resourceSpans {
			for _, attr := range rs.Resource.Attributes {
				if attr.Key != serviceNameAttr || attr.Value.StringValue == nil {
					continue
				}
				name := strings.TrimSpace(*attr.Value.StringValue)
				if name == "" {
					continue
				}
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = struct{}{}
				out.Names = append(out.Names, name)
			}
		}
	}

	return out, nil
}
