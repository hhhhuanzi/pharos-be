package migrate

import (
	"sort"
	"strings"

	"github.com/toolkits/pkg/logger"
	"gorm.io/gorm"
)

const (
	esIndexPatternTableName       = "es_index_pattern"
	esIndexPatternUniqueIndexName = "idx_datasource_name"
)

// MigrateEsIndexPatternUniqueIndex 修正 es_index_pattern 上错误的唯一约束。
//
// pkg/ormx/database_init.go 的 InitSqliteESIndexPattern 把 datasource_id 和 name 声明成了
// 两个独立的单列唯一索引（idx_datasource / idx_name），而不是 MySQL / Postgres 那样的
// (datasource_id, name) 联合唯一索引。结果是 SQLite 建的库里一个数据源只能存在一条索引模式，
// 建第二条时报 UNIQUE constraint failed: es_index_pattern.datasource_id。
//
// 只有真的检测到这类单列唯一索引时才动手，避免影响 MySQL / Postgres 以及用
// docker/sqlite.sql 建的库。
func MigrateEsIndexPatternUniqueIndex(db *gorm.DB) {
	if !db.Migrator().HasTable(esIndexPatternTableName) {
		return
	}

	indexes, err := db.Migrator().GetIndexes(esIndexPatternTableName)
	if err != nil {
		logger.Errorf("failed to get indexes of %s: %v", esIndexPatternTableName, err)
		return
	}

	var legacyIndexNames []string
	hasCompositeUnique := false

	for _, index := range indexes {
		unique, ok := index.Unique()
		if !ok || !unique {
			continue
		}

		switch {
		case matchColumns(index.Columns(), "datasource_id"), matchColumns(index.Columns(), "name"):
			legacyIndexNames = append(legacyIndexNames, index.Name())
		case matchColumns(index.Columns(), "datasource_id", "name"):
			hasCompositeUnique = true
		}
	}

	if len(legacyIndexNames) == 0 {
		return
	}

	// 联合唯一比单列唯一宽松，正常不会有重复行；真出现了说明数据本身有问题，
	// 此时保留旧索引并报错，避免把库改成完全没有唯一保护的状态。
	var duplicated int64
	err = db.Raw("SELECT count(*) FROM (SELECT datasource_id, name FROM " + esIndexPatternTableName +
		" GROUP BY datasource_id, name HAVING count(*) > 1) t").Scan(&duplicated).Error
	if err != nil {
		logger.Errorf("failed to check duplicated (datasource_id, name) in %s: %v", esIndexPatternTableName, err)
		return
	}
	if duplicated > 0 {
		logger.Errorf("found %d duplicated (datasource_id, name) in %s, skip fixing unique index", duplicated, esIndexPatternTableName)
		return
	}

	for _, name := range legacyIndexNames {
		if err := db.Migrator().DropIndex(esIndexPatternTableName, name); err != nil {
			logger.Errorf("failed to drop legacy unique index %s on %s: %v", name, esIndexPatternTableName, err)
			return
		}
		logger.Infof("dropped legacy single-column unique index %s on %s", name, esIndexPatternTableName)
	}

	if hasCompositeUnique {
		return
	}

	if err := db.Exec("CREATE UNIQUE INDEX " + esIndexPatternUniqueIndexName +
		" ON " + esIndexPatternTableName + " (datasource_id, name)").Error; err != nil {
		logger.Errorf("failed to create unique index %s on %s: %v", esIndexPatternUniqueIndexName, esIndexPatternTableName, err)
		return
	}
	logger.Infof("created unique index %s on %s (datasource_id, name)", esIndexPatternUniqueIndexName, esIndexPatternTableName)
}

// matchColumns 判断索引覆盖的列是否恰好等于 want（忽略列顺序与大小写）。
func matchColumns(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}

	normalize := func(in []string) []string {
		out := make([]string, len(in))
		for i := range in {
			out[i] = strings.ToLower(strings.Trim(in[i], "`\"[]"))
		}
		sort.Strings(out)
		return out
	}

	gotColumns, wantColumns := normalize(got), normalize(want)
	for i := range gotColumns {
		if gotColumns[i] != wantColumns[i] {
			return false
		}
	}

	return true
}
