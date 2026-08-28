package servicegraph

import "strings"

// 移植 pharos-fe src/dh/trace/dependencies/envScope.ts：service_graph 序列还没刮到
// client_ / server_ env dimension 时，用本环境 CLIENT span 的 span_name 剔除外环境的
// database / virtual_node peer。有了 env dimension 之后这条回退路径就不会再被走到。

// 不是环境相关 db.name 的通用 peer 名，回退图上要保留，否则会把真实的 unknown / redis 藏掉。
var genericPeers = map[string]struct{}{
	"user":      {},
	"unknown":   {},
	"redis":     {},
	"other_sql": {},
}

// SpanNamePeerTokens 从 CLIENT span_name 里取能与 service_graph server（db.name）对上的 token。
//
//	Mongo: `find turms-config-pre.groupType` → `turms-config-pre`
//	SQL:   `SELECT turms.t_chatroom_admin` → `turms`
//
// 先按空白 / `/` 切，再按 `.` 切。不能按 `-` 切，否则 `turms` 会命中 `turms-config-pre`。
func SpanNamePeerTokens(spanName string) []string {
	tokens := make([]string, 0, 4)
	for _, part := range strings.FieldsFunc(spanName, func(r rune) bool {
		return r == '/' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
	}) {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		tokens = append(tokens, trimmed)
		for _, bit := range strings.Split(trimmed, ".") {
			if bit != "" && bit != trimmed {
				tokens = append(tokens, bit)
			}
		}
	}
	return tokens
}

func PeerTokensFromSpanNames(spanNames []string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, name := range spanNames {
		for _, token := range SpanNamePeerTokens(name) {
			tokens[token] = struct{}{}
		}
	}
	return tokens
}

// FilterEdgesByPeerTokens 丢掉 server / client 不在本环境 span_name token 里的 database /
// virtual_node peer。RPC 边保留：没有 env dimension 时它们本来就分不开，同名 RPC peer 是同一个串。
func FilterEdgesByPeerTokens(edges []Edge, focusService string, tokens map[string]struct{}) []Edge {
	if focusService == "" {
		return edges
	}
	out := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		peer := ""
		switch {
		case edge.Client == focusService:
			peer = edge.Server
		case edge.Server == focusService:
			peer = edge.Client
		}
		if peer == "" {
			continue
		}
		if edge.ConnectionType != "database" && edge.ConnectionType != "virtual_node" {
			out = append(out, edge)
			continue
		}
		if _, ok := genericPeers[strings.ToLower(peer)]; ok {
			out = append(out, edge)
			continue
		}
		if _, ok := tokens[peer]; ok {
			out = append(out, edge)
		}
	}
	return out
}
