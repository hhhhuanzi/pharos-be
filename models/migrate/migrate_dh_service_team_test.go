package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSqliteDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// newLegacyDhServiceTeamDB 复现旧版本建出来的表：唯一约束只有 (service_name, env)，
// 也就是一个服务只能挂一个团队。
func newLegacyDhServiceTeamDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := newSqliteDB(t)
	stmts := []string{
		"CREATE TABLE `dh_service_team` (`id` integer PRIMARY KEY AUTOINCREMENT,`service_name` text NOT NULL DEFAULT \"\",`env` text NOT NULL DEFAULT \"\",`user_group_id` integer NOT NULL DEFAULT 0,`source` text NOT NULL DEFAULT \"manual\",`created_at` integer NOT NULL DEFAULT 0,`created_by` text NOT NULL DEFAULT \"\",`updated_at` integer NOT NULL DEFAULT 0,`updated_by` text NOT NULL DEFAULT \"\")",
		"CREATE UNIQUE INDEX `idx_dh_svc_team_name_env` ON `dh_service_team`(`service_name`,`env`)",
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func insertBinding(db *gorm.DB, name, env string, groupID int64) error {
	return db.Exec(
		"INSERT INTO dh_service_team (service_name, env, user_group_id) VALUES (?, ?, ?)", name, env, groupID,
	).Error
}

func countBindings(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var n int64
	if err := db.Raw("SELECT count(*) FROM dh_service_team").Scan(&n).Error; err != nil {
		t.Fatalf("count dh_service_team: %v", err)
	}
	return n
}

// 旧库一个服务只能挂一个团队，迁移后应当允许多团队，且 (service_name, env, user_group_id) 仍然唯一。
func TestMigrateDhServiceTeam_AllowsMultipleTeamsPerService(t *testing.T) {
	db := newLegacyDhServiceTeamDB(t)

	if err := insertBinding(db, "rome-sec-admin", "", 5); err != nil {
		t.Fatalf("插入首条绑定不应失败: %v", err)
	}
	if err := insertBinding(db, "rome-sec-admin", "", 6); err == nil {
		t.Fatal("迁移前同一服务挂第二个团队应该失败，用例没有复现旧约束")
	}

	MigrateDhServiceTeam(db)

	if err := insertBinding(db, "rome-sec-admin", "", 6); err != nil {
		t.Fatalf("迁移后同一服务应当可以挂多个团队: %v", err)
	}
	if err := insertBinding(db, "rome-sec-admin", "", 5); err == nil {
		t.Fatal("同一 (service_name, env, user_group_id) 重复应当仍被联合唯一索引拦下")
	}
}

// 迁移每次启动都会跑，必须幂等，且不能丢已有绑定。
func TestMigrateDhServiceTeam_IdempotentAndKeepsRows(t *testing.T) {
	db := newLegacyDhServiceTeamDB(t)

	if err := insertBinding(db, "rome-sec-auth", "", 5); err != nil {
		t.Fatalf("insert: %v", err)
	}

	MigrateDhServiceTeam(db)
	MigrateDhServiceTeam(db)
	MigrateDhServiceTeam(db)

	if got := countBindings(t, db); got != 1 {
		t.Errorf("重复执行迁移后已有绑定数量: got %d want 1", got)
	}
	if err := insertBinding(db, "rome-sec-auth", "", 6); err != nil {
		t.Fatalf("重复执行迁移后仍应允许多团队: %v", err)
	}
}

// 全新部署：表不存在时应当直接建出正确结构。
func TestMigrateDhServiceTeam_FreshInstall(t *testing.T) {
	db := newSqliteDB(t)

	MigrateDhServiceTeam(db)

	if err := insertBinding(db, "rome-sec-index", "", 5); err != nil {
		t.Fatalf("全新建表后插入应当成功: %v", err)
	}
	if err := insertBinding(db, "rome-sec-index", "", 6); err != nil {
		t.Fatalf("全新建表后同一服务多团队应当成功: %v", err)
	}
	if err := insertBinding(db, "rome-sec-index", "", 5); err == nil {
		t.Fatal("全新建表后重复三元组应当被拦下")
	}
}

// 结构已经正确时不应破坏约束。
func TestMigrateDhServiceTeam_LeavesHealthySchemaAlone(t *testing.T) {
	db := newSqliteDB(t)

	MigrateDhServiceTeam(db)
	if err := insertBinding(db, "rome-sec-calendar", "", 5); err != nil {
		t.Fatalf("insert: %v", err)
	}

	MigrateDhServiceTeam(db)

	if err := insertBinding(db, "rome-sec-calendar", "", 5); err == nil {
		t.Fatal("二次迁移后联合唯一约束不应丢失")
	}
	if got := countBindings(t, db); got != 1 {
		t.Errorf("二次迁移后行数: got %d want 1", got)
	}
}
