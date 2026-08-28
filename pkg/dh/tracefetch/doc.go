// Package tracefetch 是 dh 二开：按 traceID 从 tracing 数据源取整条 trace，并从响应里提取
// 这条 trace 涉及的服务名，供调用方做团队可见性判权。
//
// trace 详情请求里只有 traceID，没有服务维度，通用数据源代理 /proxy/:id/*（见
// center/router/router_dh_proxy.go）不解析响应体，只能做到数据源级判权，判不出这条 trace
// 属于谁。所以判权必须发生在一个会读 trace 内容的服务端接口上。
//
// 这里只做「取回 + 认出服务名」，不做 OTLP -> Jaeger 的结构转换：判权通过后原始响应体原样
// 回传，前端沿用既有的转换与瀑布图渲染。
package tracefetch
