// Package serviceteam 是 dh 二开：服务 ↔ Nightingale user_group 的可见性规则。
//
// 服务本身没有自有表，是前端从 spanmetrics / Jaeger 动态聚合出来的，
// 主键按服务名认定（env 预留给以后按环境覆盖）。本期绑定一律写 env=""。
package serviceteam

import "strings"

const (
	ViewAllPerm = "/service/view-all"
	ManagePerm  = "/service/manage"

	SourceManual  = "manual"
	SourceRelease = "release"
)

// Binding 是一条服务-团队关联（与 dh_service_team 行对应，不含 DB 字段）。
type Binding struct {
	ServiceName string
	Env         string
	UserGroupID int64
}

// Ref 是目录里的一行服务（name + 可选 env）。
type Ref struct {
	Name string
	Env  string
}

// NormalizeEnv 把空/空白 env 收成 ""，与表里默认值、联合唯一键对齐。
func NormalizeEnv(env string) string {
	return strings.TrimSpace(env)
}

func NormalizeName(name string) string {
	return strings.TrimSpace(name)
}

// NormalizeNames 去空白、去空串、去重，保持首次出现的顺序。
func NormalizeNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		n := NormalizeName(raw)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// IsOpsRole 判定运维 / SRE：角色名大小写不敏感地等于 "SRE" 或 "运维"。
// 仓库里没有现成的 SRE 角色枚举，这是产品约定的旁路；Admin 走 IsAdmin，不走这里。
func IsOpsRole(roles []string) bool {
	for _, raw := range roles {
		n := strings.ToLower(strings.TrimSpace(raw))
		if n == "sre" || n == "运维" {
			return true
		}
	}
	return false
}

// CanViewAll 能看全部服务（含未关联团队的）。
func CanViewAll(admin bool, roles []string, hasViewAllPerm bool) bool {
	return admin || IsOpsRole(roles) || hasViewAllPerm
}

// CanManage 能手动改服务所属团队。
func CanManage(admin bool, roles []string, hasManagePerm bool) bool {
	return admin || IsOpsRole(roles) || hasManagePerm
}

// ResolveBindings 返回该服务在当前 env 上生效的全部团队。
// 先收集精确 (name, env)，没有再回落到服务级 (name, "")。
func ResolveBindings(bindings []Binding, name, env string) []Binding {
	name = NormalizeName(name)
	env = NormalizeEnv(env)
	if name == "" {
		return nil
	}
	exact := make([]Binding, 0)
	fallback := make([]Binding, 0)
	for i := range bindings {
		b := bindings[i]
		if NormalizeName(b.ServiceName) != name {
			continue
		}
		bEnv := NormalizeEnv(b.Env)
		if bEnv == env {
			exact = append(exact, b)
			continue
		}
		if bEnv == "" {
			fallback = append(fallback, b)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return fallback
}

// ResolveBinding 兼容单团队调用：返回 ResolveBindings 的第一条。
func ResolveBinding(bindings []Binding, name, env string) *Binding {
	lst := ResolveBindings(bindings, name, env)
	if len(lst) == 0 {
		return nil
	}
	return &lst[0]
}

// CanSee 普通用户只要属于服务所挂的任一团队即可看见；未关联的只有 viewAll 能看。
func CanSee(viewAll bool, groupIDs map[int64]struct{}, bindings []Binding, name, env string) bool {
	if viewAll {
		return true
	}
	for _, b := range ResolveBindings(bindings, name, env) {
		if b.UserGroupID <= 0 {
			continue
		}
		if _, ok := groupIDs[b.UserGroupID]; ok {
			return true
		}
	}
	return false
}

// FilterRefs 按 CanSee 收窄目录。viewAll 时原样返回（顺序不变）。
func FilterRefs(viewAll bool, groupIDs map[int64]struct{}, bindings []Binding, refs []Ref) []Ref {
	if viewAll {
		return refs
	}
	out := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		if CanSee(false, groupIDs, bindings, ref.Name, ref.Env) {
			out = append(out, ref)
		}
	}
	return out
}

// GroupIDSet 把团队 id 列表收成查找表。
func GroupIDSet(ids []int64) map[int64]struct{} {
	m := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			m[id] = struct{}{}
		}
	}
	return m
}
