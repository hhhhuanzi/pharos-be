// Package tracesummary 是 dh 二开：把一批 trace 的全量 span 折成列表摘要，摘要计算发生在服务端。
//
// trace 列表原本走 /proxy/:id/api/v3/traces，响应里是全部匹配 trace 的完整 span（含 resource 属性
// 与 SQL），摘要是在浏览器里裁出来的：任何登录用户按别的团队的服务查一次列表，就能从网络响应里
// 拿到那批 trace 的全部 span，不需要点详情。改由服务端取全量 span、算完摘要只回摘要，浏览器侧的
// 全量 span 下载随之消失，重的传输变成 Jaeger -> center 的集群内流量。
//
// 用 Jaeger 的轻量 /api/v3/trace-summaries 换不下来：那个接口不返回 tags，列表「类型」列填不出来
// （rootType 依赖 messaging.system / http.* / db.* 这些 span 属性）。
//
// 本包是 pharos-fe 这几个文件的移植，两侧口径必须一致，改这里时先与它们比对：
//
//	src/dh/trace/contract.ts       traceResponseToSummary
//	src/dh/trace/listType.ts       resolveTraceKind / resolveFromOperation / isInternalFrame
//	src/dh/trace/spanSemantics.ts  resolveSpanKind / isNacosSpan / isMessagingSpan
//	src/dh/trace/summaryFields.ts  tagValue / resolveRootInterface
//	src/dh/trace/adapters/jaeger.ts otlpTracesDataToJaegerResponses（OTLP -> Jaeger 形状）
package tracesummary
