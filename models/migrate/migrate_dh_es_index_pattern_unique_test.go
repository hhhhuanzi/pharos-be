package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newBrokenEsIndexPatternDB 复现 pkg/ormx/database_init.go 里 InitSqliteESIndexPattern
// 建出来的表结构：datasource_id 和 name 各自一个单列唯一索引。
func newBrokenEsIndexPatternDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	stmts := []string{
		"CREATE TABLE `es_index_pattern` (`id` integer PRIMARY KEY AUTOINCREMENT,`datasource_id` integer NOT NULL DEFAULT 0,`name` text NOT NULL,`time_field` text NOT NULL DEFAULT \"@timestamp\")",
		"CREATE UNIQUE INDEX `idx_name` ON `es_index_pattern`(`name`)",
		"CREATE UNIQUE INDEX `idx_datasource` ON `es_index_pattern`(`datasource_id`)",
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	return db
}

func insertPattern(db *gorm.DB, datasourceID int64, name string) error {
	return db.Exec("INSERT INTO es_index_pattern (datasource_id, name) VALUES (?, ?)", datasourceID, name).Error
}

// 同一数据源下建第二个索引模式，修复前会被单列唯一索引拦下，修复后应当放行，
// 同时 (datasource_id, name) 的联合唯一仍然有效。
func TestMigrateEsIndexPatternUniqueIndex_AllowsMultiplePerDatasource(t *testing.T) {
	db := newBrokenEsIndexPatternDB(t)

	if err := insertPattern(db, 1, "k8s-pod*"); err != nil {
		t.Fatalf("插入首条索引模式不应失败: %v", err)
	}
	if err := insertPattern(db, 1, "trade*"); err == nil {
		t.Fatal("修复前同一数据源下建第二条应该失败，但成功了，说明用例没有复现问题")
	}

	MigrateEsIndexPatternUniqueIndex(db)

	if err := insertPattern(db, 1, "trade*"); err != nil {
		t.Fatalf("修复后同一数据源下建第二条应当成功: %v", err)
	}
	if err := insertPattern(db, 2, "trade*"); err != nil {
		t.Fatalf("修复后不同数据源下同名索引模式应当成功: %v", err)
	}
	if err := insertPattern(db, 1, "trade*"); err == nil {
		t.Fatal("同一数据源下重名应当仍被联合唯一索引拦下")
	}

	assertIndexes(t, db, map[string]bool{
		"idx_datasource":      false,
		"idx_name":            false,
		"idx_datasource_name": true,
	})
}

// 迁移每次启动都会跑，必须幂等。
func TestMigrateEsIndexPatternUniqueIndex_Idempotent(t *testing.T) {
	db := newBrokenEsIndexPatternDB(t)

	MigrateEsIndexPatternUniqueIndex(db)
	MigrateEsIndexPatternUniqueIndex(db)

	if err := insertPattern(db, 1, "a*"); err != nil {
		t.Fatalf("insert a*: %v", err)
	}
	if err := insertPattern(db, 1, "b*"); err != nil {
		t.Fatalf("重复执行迁移后仍应允许同数据源多条: %v", err)
	}

	assertIndexes(t, db, map[string]bool{
		"idx_datasource":      false,
		"idx_name":            false,
		"idx_datasource_name": true,
	})
}

// 表结构本来就正确时（MySQL / Postgres 以及 docker/sqlite.sql 建的库）不应被改动。
func TestMigrateEsIndexPatternUniqueIndex_LeavesHealthySchemaAlone(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	stmts := []string{
		"CREATE TABLE `es_index_pattern` (`id` integer PRIMARY KEY AUTOINCREMENT,`datasource_id` integer NOT NULL DEFAULT 0,`name` text NOT NULL)",
		"CREATE UNIQUE INDEX `idx_es_index_pattern_datasource_id_name` ON `es_index_pattern`(`datasource_id`,`name`)",
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	MigrateEsIndexPatternUniqueIndex(db)

	assertIndexes(t, db, map[string]bool{
		"idx_es_index_pattern_datasource_id_name": true,
		"idx_datasource_name":                     false, // 已有正确约束，不应再建一个重复的
	})
}

// 表不存在时应安全跳过，不 panic。
func TestMigrateEsIndexPatternUniqueIndex_NoTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	MigrateEsIndexPatternUniqueIndex(db)
}

func assertIndexes(t *testing.T, db *gorm.DB, want map[string]bool) {
	t.Helper()

	for name, shouldExist := range want {
		var count int64
		if err := db.Raw(
			"SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'es_index_pattern' AND name = ?", name,
		).Scan(&count).Error; err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if got := count > 0; got != shouldExist {
			t.Errorf("索引 %s 存在性: got %v want %v", name, got, shouldExist)
		}
	}
}
