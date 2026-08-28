package servicegraph

import (
	"sort"

	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
)

// NodeNames 按首次出现顺序列出图里的节点。
func NodeNames(edges []Edge) []string {
	seen := make(map[string]struct{}, len(edges)*2)
	out := make([]string, 0, len(edges)*2)
	add := func(id string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, edge := range edges {
		add(edge.Client)
		add(edge.Server)
	}
	return out
}

// CanSeeAnyEnv 判定服务名对当前用户是否可见。
//
// 拓扑指标的 by 子句里只有 client / server / connection_type，没有 env 维度，因此拿不到边两端
// 的环境。规则本体在 serviceteam.CanSeeAnyEnv（trace 详情判权同样按服务名判、拿不到 env），
// 这里只保留拓扑侧的调用入口。
func CanSeeAnyEnv(viewAll bool, groupIDs map[int64]struct{}, bindings []serviceteam.Binding, name string) bool {
	return serviceteam.CanSeeAnyEnv(viewAll, groupIDs, bindings, name)
}

// CanSeeFunc 判定单个节点名是否对当前用户可见。
type CanSeeFunc func(name string) bool

type FilterInput struct {
	Edges   []Edge
	ViewAll bool
	CanSee  CanSeeFunc
	// MaxEdges > 0 时截断返回的边（已按错误率 / P95 / 调用量排序），保护大基数查询。
	MaxEdges int
}

type Result struct {
	Edges []Edge `json:"edges"`
	// VisibleServices 是本次返回的边上出现过、且对当前用户可见的节点名。
	VisibleServices []string `json:"visible_services"`
	Truncated       bool     `json:"truncated"`
}

// Filter 按团队可见性裁剪拓扑：一条边至少有一端能担保（viewAll，或该节点名存在与我的组相交的
// 绑定）才保留，否则整条丢弃。
//
// 中间件（mysql / redis / …）、unknown、user 在 dh_service_team 里没有绑定行，因此永远担保不了
// 任何边，只挂在被丢弃边上的虚拟节点会随之消失——这就是「虚拟节点不独立判权、可见性由边推导」。
// 担保不看节点是服务还是虚拟节点：叶子服务可能只有一条 user 入边，靠形状判定会把它误杀。
func Filter(in FilterInput) Result {
	cache := make(map[string]bool)
	canVouch := func(name string) bool {
		if in.ViewAll {
			return true
		}
		if vouched, ok := cache[name]; ok {
			return vouched
		}
		vouched := in.CanSee != nil && in.CanSee(name)
		cache[name] = vouched
		return vouched
	}

	kept := make([]Edge, 0, len(in.Edges))
	for _, edge := range in.Edges {
		if canVouch(edge.Client) || canVouch(edge.Server) {
			kept = append(kept, edge)
		}
	}

	truncated := false
	if in.MaxEdges > 0 && len(kept) > in.MaxEdges {
		kept = kept[:in.MaxEdges]
		truncated = true
	}

	names := make([]string, 0, len(kept))
	for _, name := range NodeNames(kept) {
		if canVouch(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	return Result{Edges: kept, VisibleServices: names, Truncated: truncated}
}
