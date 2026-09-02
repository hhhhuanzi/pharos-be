package tracesummary

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// otlpAnyValue 建模 OTLP JSON 的 AnyValue。intValue 是 proto3 JSON 的 64 位整数，可能是十进制
// 字符串也可能是数字，所以用 json.RawMessage 自己解。
type otlpAnyValue struct {
	StringValue *string          `json:"stringValue"`
	BoolValue   *bool            `json:"boolValue"`
	IntValue    json.RawMessage  `json:"intValue"`
	DoubleValue *float64         `json:"doubleValue"`
	BytesValue  *string          `json:"bytesValue"`
	ArrayValue  *otlpArrayValue  `json:"arrayValue"`
	KvlistValue *otlpKvlistValue `json:"kvlistValue"`
}

type otlpArrayValue struct {
	Values []otlpAnyValue `json:"values"`
}

type otlpKvlistValue struct {
	Values []otlpKeyValue `json:"values"`
}

type otlpKeyValue struct {
	Key   string        `json:"key"`
	Value *otlpAnyValue `json:"value"`
}

type otlpStatus struct {
	// 0 = UNSET, 1 = OK, 2 = ERROR。
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId"`
	Name              string          `json:"name"`
	Kind              *int            `json:"kind"`
	StartTimeUnixNano json.RawMessage `json:"startTimeUnixNano"`
	EndTimeUnixNano   json.RawMessage `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue  `json:"attributes"`
	Links             []otlpLink      `json:"links"`
	Status            *otlpStatus     `json:"status"`
}

type otlpLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpScopeSpans struct {
	Scope *otlpScope `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpResourceSpans struct {
	Resource   *otlpResource    `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

// otlpChunk 同时兼容 grpc-gateway 的 {"result": TracesData} 流式信封和裸 TracesData。
type otlpChunk struct {
	Result *struct {
		ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
	} `json:"result"`
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

var spanKindNames = map[int]string{2: "server", 3: "client", 4: "producer", 5: "consumer"}

// SpanRef 对应前端 TraceSpanData.references 的一项。
type SpanRef struct {
	SpanID  string
	TraceID string
}

// Span 是转换后的 span，字段对应前端 TraceSpanData 里摘要真正会读的部分。
type Span struct {
	SpanID  string
	TraceID string
	Service string
	// Env 是 resource 属性 deployment.environment.name 的值，未上报该属性时为空串。
	Env       string
	Operation string
	// StartTime / Duration 单位是微秒，与瀑布图一致。
	StartTime int64
	Duration  int64
	Tags      []Tag
	// Parents 是 references，顺序与前端一致；第一项用于 orphan 判定。
	Parents []SpanRef
}

// Trace 是按 traceId 归组后的一条 trace。FindTraces 会把所有命中 trace 的 resourceSpans 拍平进
// 同一个响应，所以归组按 span 上的 traceId 做，不按 resourceSpans 条目做。
type Trace struct {
	TraceID string
	Spans   []Span
}

// DecodeTraces 把 api_v3 的 OTLP JSON 响应转成按 traceId 归组的 trace 列表，顺序为 traceId 首次
// 出现的顺序。服务端流式 RPC 的 HTTP 网关可能写出多个换行分隔的 JSON 值，所以按 JSON 值逐个解到
// 结尾，而不是把整个 body 当成单个对象。
func DecodeTraces(body []byte) ([]Trace, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}

	order := make([]string, 0, 16)
	byTrace := make(map[string][]Span)

	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		var chunk otlpChunk
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}

		resourceSpans := chunk.ResourceSpans
		if chunk.Result != nil {
			resourceSpans = append(resourceSpans, chunk.Result.ResourceSpans...)
		}

		for _, rs := range resourceSpans {
			var resourceTags []Tag
			if rs.Resource != nil {
				resourceTags = attributesToTags(rs.Resource.Attributes)
			}
			serviceName := "unknown"
			env := ""
			for _, tag := range resourceTags {
				switch tag.Key {
				case serviceNameAttr:
					serviceName = tag.Value
				case envAttr:
					env = tag.Value
				}
			}

			for _, ss := range rs.ScopeSpans {
				var scopeTags []Tag
				if ss.Scope != nil {
					if ss.Scope.Name != "" {
						scopeTags = append(scopeTags, Tag{Key: "otel.scope.name", Value: ss.Scope.Name})
					}
					if ss.Scope.Version != "" {
						scopeTags = append(scopeTags, Tag{Key: "otel.scope.version", Value: ss.Scope.Version})
					}
				}

				for _, span := range ss.Spans {
					spanID := strings.ToLower(span.SpanID)
					traceID := strings.ToLower(span.TraceID)
					if spanID == "" || traceID == "" {
						continue
					}
					if _, ok := byTrace[traceID]; !ok {
						order = append(order, traceID)
					}
					byTrace[traceID] = append(byTrace[traceID], toSpan(span, serviceName, env, scopeTags))
				}
			}
		}
	}

	traces := make([]Trace, 0, len(order))
	for _, traceID := range order {
		traces = append(traces, Trace{TraceID: traceID, Spans: byTrace[traceID]})
	}
	return traces, nil
}

const serviceNameAttr = "service.name"

// envAttr 与 tracefetch.EnvAttributeKey 是同一个键，查询侧用它收窄、这里用它填摘要的环境列。
// 本包是纯转换层（pharos-fe 的移植），不引 HTTP 客户端包，所以键名在这里再写一份；
// env_test.go 断言两者相等，防止哪天只改了一侧。
const envAttr = "deployment.environment.name"

// toSpan 的 env 来自 resource 属性，所以同一批 resourceSpans 下的 span 共享一个值；调用方在解
// resource 时已经取好，这里不再重复扫 tags。
func toSpan(span otlpSpan, serviceName string, env string, scopeTags []Tag) Span {
	traceID := strings.ToLower(span.TraceID)
	parents := make([]SpanRef, 0, 1+len(span.Links))
	if !isZeroOrEmptyID(span.ParentSpanID) {
		parents = append(parents, SpanRef{SpanID: strings.ToLower(span.ParentSpanID), TraceID: traceID})
	}
	for _, link := range span.Links {
		if link.SpanID == "" {
			continue
		}
		ref := SpanRef{SpanID: strings.ToLower(link.SpanID), TraceID: traceID}
		if link.TraceID != "" {
			ref.TraceID = strings.ToLower(link.TraceID)
		}
		// 父 span 有时会被重复编码成一条 link，不要重复计入。
		if len(parents) > 0 && parents[0] == ref {
			continue
		}
		parents = append(parents, ref)
	}

	tags := attributesToTags(span.Attributes)
	if span.Kind != nil {
		if name, ok := spanKindNames[*span.Kind]; ok && !hasTagKey(tags, "span.kind") {
			tags = append(tags, Tag{Key: "span.kind", Value: name})
		}
	}
	if span.Status != nil && span.Status.Code == 2 && !hasTagKey(tags, "error") {
		tags = append(tags, Tag{Key: "error", Value: "true"})
		if span.Status.Message != "" {
			tags = append(tags, Tag{Key: "otel.status_description", Value: span.Status.Message})
		}
	}
	tags = append(tags, scopeTags...)

	start := nanoToMicro(span.StartTimeUnixNano)
	end := nanoToMicro(span.EndTimeUnixNano)
	duration := end - start
	if duration < 0 {
		duration = 0
	}

	operation := span.Name
	if operation == "" {
		operation = "unknown"
	}

	return Span{
		SpanID:    strings.ToLower(span.SpanID),
		TraceID:   traceID,
		Service:   serviceName,
		Env:       env,
		Operation: operation,
		StartTime: start,
		Duration:  duration,
		Tags:      tags,
		Parents:   parents,
	}
}

func hasTagKey(tags []Tag, key string) bool {
	for _, tag := range tags {
		if tag.Key == key {
			return true
		}
	}
	return false
}

func isZeroOrEmptyID(id string) bool {
	if id == "" {
		return true
	}
	return strings.Trim(id, "0") == ""
}

func attributesToTags(attributes []otlpKeyValue) []Tag {
	if len(attributes) == 0 {
		return nil
	}
	tags := make([]Tag, 0, len(attributes))
	for _, attr := range attributes {
		tags = append(tags, Tag{Key: attr.Key, Value: anyValueToString(attr.Value)})
	}
	return tags
}

// anyValueToString 复刻前端 otlpValueToPlain + String() 的结果：属性值最终都要按字符串参与
// 语义判定，所以在解码时就折成字符串。
func anyValueToString(value *otlpAnyValue) string {
	if value == nil {
		return ""
	}
	switch {
	case value.StringValue != nil:
		return *value.StringValue
	case value.BoolValue != nil:
		return strconv.FormatBool(*value.BoolValue)
	case len(value.IntValue) > 0:
		return strings.Trim(strings.TrimSpace(string(value.IntValue)), `"`)
	case value.DoubleValue != nil:
		return strconv.FormatFloat(*value.DoubleValue, 'f', -1, 64)
	case value.BytesValue != nil:
		return *value.BytesValue
	case value.ArrayValue != nil:
		parts := make([]string, 0, len(value.ArrayValue.Values))
		for i := range value.ArrayValue.Values {
			parts = append(parts, anyValueToString(&value.ArrayValue.Values[i]))
		}
		return strings.Join(parts, ",")
	case value.KvlistValue != nil:
		// 前端把 kvlist 折成对象，再被 String() 变成这个常量；语义键上不会出现 kvlist。
		return "[object Object]"
	default:
		return ""
	}
}

// nanoToMicro 把 OTLP 的纳秒时间戳（proto3 JSON 里是十进制字符串）转成微秒。用 big.Int 避免
// float64 精度丢失。
func nanoToMicro(raw json.RawMessage) int64 {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0
	}
	if n, ok := new(big.Int).SetString(text, 10); ok {
		return new(big.Int).Div(n, big.NewInt(1000)).Int64()
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return int64(f / 1000)
}

// ServiceSummary 是摘要里的单个服务明细，JSON 字段名与前端 PharosServiceSummary 一致。
type ServiceSummary struct {
	Name           string `json:"name"`
	SpanCount      int    `json:"spanCount"`
	ErrorSpanCount int    `json:"errorSpanCount"`
}

// TraceSummary 是列表一行，JSON 字段名与前端 PharosTraceSummary 一致。
type TraceSummary struct {
	TraceID         string           `json:"traceId"`
	RootService     string           `json:"rootService"`
	RootOperation   string           `json:"rootOperation"`
	RootInterface   string           `json:"rootInterface"`
	RootType        string           `json:"rootType"`
	StartTimeUs     int64            `json:"startTimeUs"`
	DurationUs      int64            `json:"durationUs"`
	SpanCount       int              `json:"spanCount"`
	ErrorSpanCount  int              `json:"errorSpanCount"`
	OrphanSpanCount int              `json:"orphanSpanCount"`
	Services        []ServiceSummary `json:"services"`
	// Envs 是这条 trace 涉及的环境，按首次出现顺序去重，空串（未上报该属性的 span）不计入。
	//
	// 做成数组而不是单值：正常一条 trace 只有一个环境（服务间调用不跨环境，DB 不上报 span，所以
	// 共用 MySQL 也不会把两个环境串进同一条 trace），但数组是廉价的兜底 —— 真出现跨环境时列表能
	// 如实标注，而不用改协议。空数组表示这批 span 都没带环境属性。
	Envs []string `json:"envs"`
}

// isErrorSpan 沿用 Jaeger/OpenTracing 约定：error 属性有真值即失败。
//
// 前端判的是 `value !== false && value !== 'false' && value !== ”`，属性值在本包里统一是字符串，
// 所以等价于「非空且不是 false」。
func isErrorSpan(tags []Tag) bool {
	for _, tag := range tags {
		if tag.Key != "error" {
			continue
		}
		if tag.Value != "" && tag.Value != "false" {
			return true
		}
	}
	return false
}

// findSummaryRootSpan 与前端 contract.ts 的 findRootSpan 一致：父引用指向 trace 之外（或没有引用）
// 的最早 span。这里校验 traceID —— links 可能指向别的 trace，那不算本 trace 内的父节点。
func findSummaryRootSpan(spans []Span, ids map[string]struct{}) int {
	root := -1
	for i, span := range spans {
		hasInternalParent := false
		for _, parent := range span.Parents {
			if parent.TraceID != span.TraceID {
				continue
			}
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
	return root
}

// Summarize 把一条 trace 折成列表一行。没有 traceID 或没有可用 span（startTime 为 0 的 span 视为
// 不可用）时返回 nil，与前端 traceResponseToSummary 返回 null 对齐。
func Summarize(trace Trace) *TraceSummary {
	spans := make([]Span, 0, len(trace.Spans))
	for _, span := range trace.Spans {
		if span.StartTime == 0 {
			continue
		}
		spans = append(spans, span)
	}
	if trace.TraceID == "" || len(spans) == 0 {
		return nil
	}

	ids := make(map[string]struct{}, len(spans))
	for _, span := range spans {
		ids[span.SpanID] = struct{}{}
	}

	var (
		startTimeUs     int64 = 1<<53 - 1
		endTimeUs       int64
		errorSpanCount  int
		orphanSpanCount int
	)
	serviceOrder := make([]string, 0, 8)
	byService := make(map[string]*ServiceSummary)
	envs := make([]string, 0, 1)
	seenEnvs := make(map[string]struct{}, 1)

	for _, span := range spans {
		if span.Env != "" {
			if _, ok := seenEnvs[span.Env]; !ok {
				seenEnvs[span.Env] = struct{}{}
				envs = append(envs, span.Env)
			}
		}
		if span.StartTime < startTimeUs {
			startTimeUs = span.StartTime
		}
		if span.StartTime+span.Duration > endTimeUs {
			endTimeUs = span.StartTime + span.Duration
		}

		isError := isErrorSpan(span.Tags)
		if isError {
			errorSpanCount++
		}
		if len(span.Parents) > 0 {
			if _, ok := ids[span.Parents[0].SpanID]; !ok {
				orphanSpanCount++
			}
		}

		name := span.Service
		if name == "" {
			name = "unknown"
		}
		entry, ok := byService[name]
		if !ok {
			entry = &ServiceSummary{Name: name}
			byService[name] = entry
			serviceOrder = append(serviceOrder, name)
		}
		entry.SpanCount++
		if isError {
			entry.ErrorSpanCount++
		}
	}

	services := make([]ServiceSummary, 0, len(serviceOrder))
	for _, name := range serviceOrder {
		services = append(services, *byService[name])
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].SpanCount > services[j].SpanCount })

	rootIdx := findSummaryRootSpan(spans, ids)
	rootOperation := ""
	rootService := ""
	var rootTags []Tag
	if rootIdx >= 0 {
		rootOperation = spans[rootIdx].Operation
		rootService = spans[rootIdx].Service
		if rootService == "" {
			rootService = "unknown"
		}
		rootTags = spans[rootIdx].Tags
	}

	rootInterface := ResolveRootInterface(rootOperation, rootTags)
	if rootInterface == "" {
		rootInterface = rootOperation
	}

	durationUs := endTimeUs - startTimeUs
	if durationUs < 0 {
		durationUs = 0
	}

	return &TraceSummary{
		TraceID:         strings.ToLower(trace.TraceID),
		RootService:     rootService,
		RootOperation:   rootOperation,
		RootInterface:   rootInterface,
		RootType:        string(ResolveTraceKind(toKindSpans(spans))),
		StartTimeUs:     startTimeUs,
		DurationUs:      durationUs,
		SpanCount:       len(spans),
		ErrorSpanCount:  errorSpanCount,
		OrphanSpanCount: orphanSpanCount,
		Services:        services,
		Envs:            envs,
	}
}

func toKindSpans(spans []Span) []KindSpan {
	out := make([]KindSpan, 0, len(spans))
	for _, span := range spans {
		out = append(out, KindSpan{
			SpanID:        span.SpanID,
			OperationName: span.Operation,
			StartTime:     span.StartTime,
			Tags:          span.Tags,
			Parents:       span.Parents,
		})
	}
	return out
}

// SummarizeAll 折算一批 trace，跳过折不出摘要的（没有 traceID / 没有可用 span）。
func SummarizeAll(traces []Trace) []TraceSummary {
	out := make([]TraceSummary, 0, len(traces))
	for _, trace := range traces {
		if summary := Summarize(trace); summary != nil {
			out = append(out, *summary)
		}
	}
	return out
}
