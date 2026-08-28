package models

import (
	"time"

	"github.com/ccfos/nightingale/v6/pkg/ctx"
	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
	"gorm.io/gorm"
)

// DhServiceTeam 服务 ↔ user_group。所属业务与团队等同，暂不拆。
// 服务没有自有表，用 service_name + env + user_group_id。
//
// env 空字符串 = 服务级默认（覆盖该服务所有环境），本期手动绑定都写这个。
// source 预留给发布系统：本期只写 manual；以后 release 覆盖同一行即可。
// 写入时一个 service_name 只能挂一个 user_group；存量脏数据读侧不猜第一条。
type DhServiceTeam struct {
	Id          int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ServiceName string `json:"service_name" gorm:"type:varchar(255);not null;uniqueIndex:idx_dh_svc_team_name_env_gid;default:''"`
	Env         string `json:"env" gorm:"type:varchar(128);not null;uniqueIndex:idx_dh_svc_team_name_env_gid;default:''"`
	UserGroupId int64  `json:"user_group_id" gorm:"type:bigint;not null;uniqueIndex:idx_dh_svc_team_name_env_gid;index:idx_dh_svc_team_gid;default:0"`
	Source      string `json:"source" gorm:"type:varchar(16);not null;default:'manual'"`
	CreatedAt   int64  `json:"created_at" gorm:"type:bigint;not null;default:0"`
	CreatedBy   string `json:"created_by" gorm:"type:varchar(64);not null;default:''"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:bigint;not null;default:0"`
	UpdatedBy   string `json:"updated_by" gorm:"type:varchar(64);not null;default:''"`

	UserGroupName string `json:"user_group_name" gorm:"-"`
}

func (DhServiceTeam) TableName() string {
	return "dh_service_team"
}

func DhServiceTeamGets(ctx *ctx.Context) ([]DhServiceTeam, error) {
	var lst []DhServiceTeam
	err := DB(ctx).Order("service_name, user_group_id").Find(&lst).Error
	return lst, err
}

func DhServiceTeamGetsByGroupIds(ctx *ctx.Context, ids []int64) ([]DhServiceTeam, error) {
	var lst []DhServiceTeam
	if len(ids) == 0 {
		return lst, nil
	}
	err := DB(ctx).Where("user_group_id in ?", ids).Order("service_name, user_group_id").Find(&lst).Error
	return lst, err
}

func DhServiceTeamGetsByGroupId(ctx *ctx.Context, groupId int64) ([]DhServiceTeam, error) {
	if groupId <= 0 {
		return nil, nil
	}
	return DhServiceTeamGetsByGroupIds(ctx, []int64{groupId})
}

func DhServiceTeamGetsByNameEnv(ctx *ctx.Context, name, env string) ([]DhServiceTeam, error) {
	name = serviceteam.NormalizeName(name)
	env = serviceteam.NormalizeEnv(env)
	var lst []DhServiceTeam
	err := DB(ctx).Where("service_name = ? and env = ?", name, env).Order("user_group_id").Find(&lst).Error
	return lst, err
}

func DhServiceTeamGetByTriple(ctx *ctx.Context, name, env string, groupId int64) (*DhServiceTeam, error) {
	name = serviceteam.NormalizeName(name)
	env = serviceteam.NormalizeEnv(env)
	var lst []DhServiceTeam
	err := DB(ctx).Where("service_name = ? and env = ? and user_group_id = ?", name, env, groupId).Limit(1).Find(&lst).Error
	if err != nil {
		return nil, err
	}
	if len(lst) == 0 {
		return nil, nil
	}
	return &lst[0], nil
}

func DhServiceTeamAdd(ctx *ctx.Context, row *DhServiceTeam) error {
	now := time.Now().Unix()
	row.ServiceName = serviceteam.NormalizeName(row.ServiceName)
	row.Env = serviceteam.NormalizeEnv(row.Env)
	if row.Source == "" {
		row.Source = serviceteam.SourceManual
	}
	if row.CreatedAt == 0 {
		row.CreatedAt = now
	}
	if row.UpdatedAt == 0 {
		row.UpdatedAt = now
	}
	if row.CreatedBy == "" {
		row.CreatedBy = row.UpdatedBy
	}
	if err := assertServiceUnbound(ctx, nil, row.ServiceName, row.Env, row.UserGroupId); err != nil {
		return err
	}
	return DB(ctx).Create(row).Error
}

// DhServiceTeamReplaceForGroup 把某个团队在 env="" 上的服务全集改成 names（差量增删）。
func DhServiceTeamReplaceForGroup(ctx *ctx.Context, groupId int64, names []string, username string) error {
	if groupId <= 0 {
		return nil
	}
	want := serviceteam.NormalizeNames(names)
	wantSet := make(map[string]struct{}, len(want))
	for _, n := range want {
		wantSet[n] = struct{}{}
	}

	return DB(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []DhServiceTeam
		if err := tx.Where("user_group_id = ? and env = ?", groupId, "").Find(&existing).Error; err != nil {
			return err
		}
		have := make(map[string]DhServiceTeam, len(existing))
		for _, row := range existing {
			have[row.ServiceName] = row
		}

		now := time.Now().Unix()
		var adding []string
		for _, n := range want {
			if _, ok := have[n]; ok {
				continue
			}
			adding = append(adding, n)
		}
		if len(adding) > 0 {
			if err := assertGroupNameValid(tx, groupId); err != nil {
				return err
			}
			for _, n := range adding {
				if err := assertServiceUnboundTx(tx, n, "", groupId); err != nil {
					return err
				}
			}
		}
		for _, n := range adding {
			row := DhServiceTeam{
				ServiceName: n,
				Env:         "",
				UserGroupId: groupId,
				Source:      serviceteam.SourceManual,
				CreatedAt:   now,
				CreatedBy:   username,
				UpdatedAt:   now,
				UpdatedBy:   username,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		for n, row := range have {
			if _, ok := wantSet[n]; ok {
				continue
			}
			if err := tx.Delete(&DhServiceTeam{}, row.Id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DhServiceTeamReplaceForService 把某个服务在 env="" 上的团队全集改成 groupIds（差量增删）。
func DhServiceTeamReplaceForService(ctx *ctx.Context, name string, groupIds []int64, username string) error {
	name = serviceteam.NormalizeName(name)
	if name == "" {
		return nil
	}
	want := uniquePositiveIDs(groupIds)
	if len(want) > 1 {
		return serviceteam.ErrMultipleTeams
	}
	wantSet := make(map[int64]struct{}, len(want))
	for _, id := range want {
		wantSet[id] = struct{}{}
	}

	return DB(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []DhServiceTeam
		if err := tx.Where("service_name = ? and env = ?", name, "").Find(&existing).Error; err != nil {
			return err
		}
		have := make(map[int64]DhServiceTeam, len(existing))
		for _, row := range existing {
			have[row.UserGroupId] = row
		}

		now := time.Now().Unix()
		if len(want) == 1 {
			if err := assertGroupNameValid(tx, want[0]); err != nil {
				return err
			}
		}
		for _, id := range want {
			if _, ok := have[id]; ok {
				continue
			}
			row := DhServiceTeam{
				ServiceName: name,
				Env:         "",
				UserGroupId: id,
				Source:      serviceteam.SourceManual,
				CreatedAt:   now,
				CreatedBy:   username,
				UpdatedAt:   now,
				UpdatedBy:   username,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		for id, row := range have {
			if _, ok := wantSet[id]; ok {
				continue
			}
			if err := tx.Delete(&DhServiceTeam{}, row.Id).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func DhServiceTeamDel(ctx *ctx.Context, name, env string) error {
	name = serviceteam.NormalizeName(name)
	env = serviceteam.NormalizeEnv(env)
	return DB(ctx).Where("service_name = ? and env = ?", name, env).Delete(&DhServiceTeam{}).Error
}

func FillDhServiceTeamGroupNames(ctx *ctx.Context, lst []DhServiceTeam) error {
	ids := make([]int64, 0, len(lst))
	seen := make(map[int64]struct{}, len(lst))
	for i := range lst {
		id := lst[i].UserGroupId
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	names, err := UserGroupIdAndNameMap(ctx, ids)
	if err != nil {
		return err
	}
	for i := range lst {
		lst[i].UserGroupName = names[lst[i].UserGroupId]
	}
	return nil
}

func DhServiceTeamToBindings(lst []DhServiceTeam) []serviceteam.Binding {
	out := make([]serviceteam.Binding, 0, len(lst))
	for _, row := range lst {
		out = append(out, serviceteam.Binding{
			ServiceName: row.ServiceName,
			Env:         row.Env,
			UserGroupID: row.UserGroupId,
		})
	}
	return out
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func assertGroupNameValid(tx *gorm.DB, groupId int64) error {
	var ug UserGroup
	if err := tx.Where("id = ?", groupId).Take(&ug).Error; err != nil {
		return err
	}
	return serviceteam.ValidateTeamName(ug.Name)
}

func assertServiceUnbound(ctx *ctx.Context, tx *gorm.DB, name, env string, groupId int64) error {
	db := DB(ctx)
	if tx != nil {
		db = tx
	}
	return assertServiceUnboundTx(db, name, env, groupId)
}

func assertServiceUnboundTx(tx *gorm.DB, name, env string, groupId int64) error {
	name = serviceteam.NormalizeName(name)
	env = serviceteam.NormalizeEnv(env)
	var lst []DhServiceTeam
	if err := tx.Where("service_name = ? and env = ? and user_group_id <> ?", name, env, groupId).Find(&lst).Error; err != nil {
		return err
	}
	if len(lst) == 0 {
		return nil
	}
	otherName := lst[0].UserGroupName
	if otherName == "" {
		var ug UserGroup
		if err := tx.Where("id = ?", lst[0].UserGroupId).Take(&ug).Error; err == nil {
			otherName = ug.Name
		}
	}
	return serviceteam.ErrAlreadyBound(name, otherName)
}
