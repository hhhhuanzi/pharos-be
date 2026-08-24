package migrate

import (
	"strings"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/toolkits/pkg/logger"
	"gorm.io/gorm"
)

const (
	dhServiceTeamTable            = "dh_service_team"
	dhServiceTeamLegacyUniqueName = "idx_dh_svc_team_name_env"
	dhServiceTeamUniqueName       = "idx_dh_svc_team_name_env_gid"
)

// MigrateDhServiceTeam 创建/补齐 dh_service_team，并把 (service_name, env) 唯一扩成 (service_name, env, user_group_id)。
// 旧行本身已是三元组，去掉旧唯一约束即可，不丢数据。新唯一索引交给 AutoMigrate。
func MigrateDhServiceTeam(db *gorm.DB) {
	db = migrationDB(db, "")
	if db.Migrator().HasTable(dhServiceTeamTable) {
		dropLegacyDhServiceTeamNameEnvUnique(db)
	}
	if err := db.AutoMigrate(&models.DhServiceTeam{}); err != nil {
		logger.Errorf("failed to migrate dh_service_team: %v", err)
	}
}

func dropLegacyDhServiceTeamNameEnvUnique(db *gorm.DB) {
	indexes, err := db.Migrator().GetIndexes(dhServiceTeamTable)
	if err != nil {
		logger.Errorf("failed to get indexes of %s: %v", dhServiceTeamTable, err)
		return
	}

	for _, index := range indexes {
		unique, ok := index.Unique()
		if !ok || !unique {
			continue
		}
		cols := index.Columns()
		name := index.Name()
		if matchColumns(cols, "service_name", "env", "user_group_id") || strings.EqualFold(name, dhServiceTeamUniqueName) {
			continue
		}
		if !matchColumns(cols, "service_name", "env") && !strings.EqualFold(name, dhServiceTeamLegacyUniqueName) {
			continue
		}
		if err := db.Migrator().DropIndex(dhServiceTeamTable, name); err != nil {
			logger.Errorf("failed to drop legacy unique index %s on %s: %v", name, dhServiceTeamTable, err)
			return
		}
		logger.Infof("dropped legacy unique index %s on %s (service_name, env)", name, dhServiceTeamTable)
	}
}
