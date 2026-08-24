package router

import (
	"net/http"
	"strconv"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

type dhServiceTeamRef struct {
	Name string `json:"name"`
	Env  string `json:"env"`
}

type dhServiceTeamFilterForm struct {
	Services []dhServiceTeamRef `json:"services"`
}

type dhServiceTeamPutForm struct {
	ServiceName  string  `json:"service_name"`
	Env          string  `json:"env"`
	UserGroupId  int64   `json:"user_group_id"`
	UserGroupIds []int64 `json:"user_group_ids"`
}

type dhServiceTeamByGroupForm struct {
	UserGroupId  int64    `json:"user_group_id" binding:"required"`
	ServiceNames []string `json:"service_names"`
}

type dhNamedTeam struct {
	Id   int64  `json:"id"`
	Name string `json:"name"`
}

type dhServiceTeamItem struct {
	Name          string        `json:"name"`
	Env           string        `json:"env"`
	UserGroupId   int64         `json:"user_group_id,omitempty"`
	UserGroupName string        `json:"user_group_name,omitempty"`
	UserGroups    []dhNamedTeam `json:"user_groups,omitempty"`
	Source        string        `json:"source"`
}

func (rt *Router) dhServiceViewer(user *models.User) (viewAll bool, canManage bool, err error) {
	if user.IsAdmin() || serviceteam.IsOpsRole(user.RolesLst) {
		return true, true, nil
	}
	viewAll, err = user.CheckPerm(rt.Ctx, serviceteam.ViewAllPerm)
	if err != nil {
		return false, false, err
	}
	canManage, err = user.CheckPerm(rt.Ctx, serviceteam.ManagePerm)
	return viewAll, canManage, err
}

func (rt *Router) dhServiceTeamBindingsFor(user *models.User, viewAll bool) ([]models.DhServiceTeam, error) {
	if viewAll {
		lst, err := models.DhServiceTeamGets(rt.Ctx)
		if err != nil {
			return nil, err
		}
		return lst, models.FillDhServiceTeamGroupNames(rt.Ctx, lst)
	}
	ids, err := models.MyGroupIds(rt.Ctx, user.Id)
	if err != nil {
		return nil, err
	}
	lst, err := models.DhServiceTeamGetsByGroupIds(rt.Ctx, ids)
	if err != nil {
		return nil, err
	}
	return lst, models.FillDhServiceTeamGroupNames(rt.Ctx, lst)
}

func (rt *Router) dhServiceTeamAllBindings() ([]models.DhServiceTeam, error) {
	lst, err := models.DhServiceTeamGets(rt.Ctx)
	if err != nil {
		return nil, err
	}
	return lst, models.FillDhServiceTeamGroupNames(rt.Ctx, lst)
}

func dhBindingIndex(lst []models.DhServiceTeam) map[string][]models.DhServiceTeam {
	m := make(map[string][]models.DhServiceTeam, len(lst))
	for _, row := range lst {
		key := row.ServiceName + "\x00" + row.Env
		m[key] = append(m[key], row)
	}
	return m
}

func dhLookupTeamRows(idx map[string][]models.DhServiceTeam, name, env string) []models.DhServiceTeam {
	name = serviceteam.NormalizeName(name)
	env = serviceteam.NormalizeEnv(env)
	if rows, ok := idx[name+"\x00"+env]; ok && len(rows) > 0 {
		return rows
	}
	if env != "" {
		if rows, ok := idx[name+"\x00"]; ok {
			return rows
		}
	}
	return nil
}

func dhRowsToNamedTeams(rows []models.DhServiceTeam) []dhNamedTeam {
	out := make([]dhNamedTeam, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if row.UserGroupId <= 0 {
			continue
		}
		if _, ok := seen[row.UserGroupId]; ok {
			continue
		}
		seen[row.UserGroupId] = struct{}{}
		out = append(out, dhNamedTeam{Id: row.UserGroupId, Name: row.UserGroupName})
	}
	return out
}

func dhItemFromRows(name, env string, rows []models.DhServiceTeam) dhServiceTeamItem {
	item := dhServiceTeamItem{
		Name:       serviceteam.NormalizeName(name),
		Env:        serviceteam.NormalizeEnv(env),
		UserGroups: dhRowsToNamedTeams(rows),
	}
	if len(rows) > 0 {
		item.UserGroupId = rows[0].UserGroupId
		item.UserGroupName = rows[0].UserGroupName
		item.Source = rows[0].Source
	}
	return item
}

func (rt *Router) dhServiceTeamTeams() ([]dhNamedTeam, error) {
	lst, err := models.UserGroupGetAll(rt.Ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dhNamedTeam, 0, len(lst))
	for _, ug := range lst {
		if ug == nil {
			continue
		}
		out = append(out, dhNamedTeam{Id: ug.Id, Name: ug.Name})
	}
	return out, nil
}

func (rt *Router) dhUserInGroup(user *models.User, groupId int64) (bool, error) {
	if groupId <= 0 {
		return false, nil
	}
	ids, err := models.MyGroupIds(rt.Ctx, user.Id)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == groupId {
			return true, nil
		}
	}
	return false, nil
}

func (rt *Router) dhServiceTeamVisibility(c *gin.Context) {
	user := c.MustGet("user").(*models.User)
	viewAll, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)

	lst, err := rt.dhServiceTeamBindingsFor(user, viewAll || canManage)
	ginx.Dangerous(err)

	items := make([]dhServiceTeamItem, 0, len(lst))
	for _, row := range lst {
		items = append(items, dhServiceTeamItem{
			Name:          row.ServiceName,
			Env:           row.Env,
			UserGroupId:   row.UserGroupId,
			UserGroupName: row.UserGroupName,
			UserGroups:    []dhNamedTeam{{Id: row.UserGroupId, Name: row.UserGroupName}},
			Source:        row.Source,
		})
	}

	payload := gin.H{
		"view_all":   viewAll,
		"can_manage": canManage,
		"bindings":   items,
	}
	if canManage {
		teams, err := rt.dhServiceTeamTeams()
		ginx.Dangerous(err)
		payload["teams"] = teams
	}
	ginx.NewRender(c).Data(payload, nil)
}

func (rt *Router) dhServiceTeamFilter(c *gin.Context) {
	var f dhServiceTeamFilterForm
	ginx.BindJSON(c, &f)

	user := c.MustGet("user").(*models.User)
	viewAll, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)

	all, err := rt.dhServiceTeamAllBindings()
	ginx.Dangerous(err)

	ids, err := models.MyGroupIds(rt.Ctx, user.Id)
	ginx.Dangerous(err)
	groupSet := serviceteam.GroupIDSet(ids)
	bindings := models.DhServiceTeamToBindings(all)
	idx := dhBindingIndex(all)

	items := make([]dhServiceTeamItem, 0, len(f.Services))
	for _, ref := range f.Services {
		if !serviceteam.CanSee(viewAll, groupSet, bindings, ref.Name, ref.Env) {
			continue
		}
		items = append(items, dhItemFromRows(ref.Name, ref.Env, dhLookupTeamRows(idx, ref.Name, ref.Env)))
	}

	payload := gin.H{
		"view_all":   viewAll,
		"can_manage": canManage,
		"items":      items,
	}
	if canManage {
		teams, err := rt.dhServiceTeamTeams()
		ginx.Dangerous(err)
		payload["teams"] = teams
	}
	ginx.NewRender(c).Data(payload, nil)
}

func (rt *Router) dhServiceTeamCheck(c *gin.Context) {
	name := ginx.QueryStr(c, "service_name", "")
	env := ginx.QueryStr(c, "env", "")
	if serviceteam.NormalizeName(name) == "" {
		ginx.Bomb(http.StatusBadRequest, "service_name is required")
	}

	user := c.MustGet("user").(*models.User)
	viewAll, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)

	all, err := rt.dhServiceTeamAllBindings()
	ginx.Dangerous(err)

	ids, err := models.MyGroupIds(rt.Ctx, user.Id)
	ginx.Dangerous(err)

	if !serviceteam.CanSee(viewAll, serviceteam.GroupIDSet(ids), models.DhServiceTeamToBindings(all), name, env) {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}

	rows := dhLookupTeamRows(dhBindingIndex(all), name, env)
	item := dhItemFromRows(name, env, rows)
	payload := gin.H{
		"visible":     true,
		"can_manage":  canManage,
		"user_groups": item.UserGroups,
		"bindings":    rowsToItems(rows),
	}
	if len(rows) > 0 {
		payload["binding"] = item
	}
	if canManage {
		teams, err := rt.dhServiceTeamTeams()
		ginx.Dangerous(err)
		payload["teams"] = teams
	}
	ginx.NewRender(c).Data(payload, nil)
}

func rowsToItems(rows []models.DhServiceTeam) []dhServiceTeamItem {
	out := make([]dhServiceTeamItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, dhServiceTeamItem{
			Name:          row.ServiceName,
			Env:           row.Env,
			UserGroupId:   row.UserGroupId,
			UserGroupName: row.UserGroupName,
			UserGroups:    []dhNamedTeam{{Id: row.UserGroupId, Name: row.UserGroupName}},
			Source:        row.Source,
		})
	}
	return out
}

func (rt *Router) dhServiceTeamByGroupGet(c *gin.Context) {
	groupId, err := strconv.ParseInt(ginx.QueryStr(c, "user_group_id", "0"), 10, 64)
	if err != nil || groupId <= 0 {
		ginx.Bomb(http.StatusBadRequest, "user_group_id is required")
	}

	user := c.MustGet("user").(*models.User)
	_, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)
	inGroup, err := rt.dhUserInGroup(user, groupId)
	ginx.Dangerous(err)
	if !canManage && !inGroup {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}

	lst, err := models.DhServiceTeamGetsByGroupId(rt.Ctx, groupId)
	ginx.Dangerous(err)
	ginx.Dangerous(models.FillDhServiceTeamGroupNames(rt.Ctx, lst))

	names := make([]string, 0, len(lst))
	items := make([]dhServiceTeamItem, 0, len(lst))
	seen := make(map[string]struct{}, len(lst))
	for _, row := range lst {
		if row.Env != "" {
			continue
		}
		if _, ok := seen[row.ServiceName]; ok {
			continue
		}
		seen[row.ServiceName] = struct{}{}
		names = append(names, row.ServiceName)
		items = append(items, dhServiceTeamItem{
			Name:          row.ServiceName,
			Env:           row.Env,
			UserGroupId:   row.UserGroupId,
			UserGroupName: row.UserGroupName,
			Source:        row.Source,
		})
	}

	ginx.NewRender(c).Data(gin.H{
		"user_group_id": groupId,
		"can_manage":    canManage,
		"service_names": names,
		"items":         items,
	}, nil)
}

func (rt *Router) dhServiceTeamByGroupPut(c *gin.Context) {
	var f dhServiceTeamByGroupForm
	ginx.BindJSON(c, &f)

	user := c.MustGet("user").(*models.User)
	_, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)
	if !canManage {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}
	if f.UserGroupId <= 0 {
		ginx.Bomb(http.StatusBadRequest, "user_group_id is required")
	}

	ug, err := models.UserGroupGetById(rt.Ctx, f.UserGroupId)
	ginx.Dangerous(err)
	if ug == nil {
		ginx.Bomb(http.StatusBadRequest, "user group not found")
	}

	ginx.Dangerous(models.DhServiceTeamReplaceForGroup(rt.Ctx, f.UserGroupId, f.ServiceNames, user.Username))

	lst, err := models.DhServiceTeamGetsByGroupId(rt.Ctx, f.UserGroupId)
	ginx.Dangerous(err)
	names := make([]string, 0, len(lst))
	for _, row := range lst {
		if row.Env == "" {
			names = append(names, row.ServiceName)
		}
	}
	ginx.NewRender(c).Data(gin.H{
		"user_group_id": f.UserGroupId,
		"service_names": serviceteam.NormalizeNames(names),
	}, nil)
}

func (rt *Router) dhServiceTeamPut(c *gin.Context) {
	var f dhServiceTeamPutForm
	ginx.BindJSON(c, &f)

	user := c.MustGet("user").(*models.User)
	_, canManage, err := rt.dhServiceViewer(user)
	ginx.Dangerous(err)
	if !canManage {
		ginx.Bomb(http.StatusForbidden, "forbidden")
	}

	name := serviceteam.NormalizeName(f.ServiceName)
	if name == "" {
		ginx.Bomb(http.StatusBadRequest, "service_name is required")
	}

	ids := f.UserGroupIds
	if len(ids) == 0 && f.UserGroupId > 0 {
		ids = []int64{f.UserGroupId}
	}
	for _, id := range ids {
		ug, err := models.UserGroupGetById(rt.Ctx, id)
		ginx.Dangerous(err)
		if ug == nil {
			ginx.Bomb(http.StatusBadRequest, "user group not found")
		}
	}

	ginx.Dangerous(models.DhServiceTeamReplaceForService(rt.Ctx, name, ids, user.Username))
	lst, err := models.DhServiceTeamGetsByNameEnv(rt.Ctx, name, "")
	ginx.Dangerous(err)
	ginx.Dangerous(models.FillDhServiceTeamGroupNames(rt.Ctx, lst))
	ginx.NewRender(c).Data(dhItemFromRows(name, "", lst), nil)
}
