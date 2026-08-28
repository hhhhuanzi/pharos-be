package migrate

import (
	"github.com/ccfos/nightingale/v6/models"
	"github.com/toolkits/pkg/logger"
	"gorm.io/gorm"
)

func MigrateDhBoardFavorite(db *gorm.DB) {
	db = migrationDB(db, "")
	if err := db.AutoMigrate(&models.DhBoardFavorite{}); err != nil {
		logger.Errorf("failed to migrate dh_board_favorite: %v", err)
	}
}
