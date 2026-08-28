package models

import (
	"errors"
	"time"

	"github.com/ccfos/nightingale/v6/pkg/ctx"
)

var ErrBoardNotFound = errors.New("dashboard not found")

// DhBoardFavorite 用户收藏的仪表盘。每人一份，跨端同步。
type DhBoardFavorite struct {
	Id       int64 `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId   int64 `json:"user_id" gorm:"type:bigint;not null;uniqueIndex:idx_dh_board_fav_user_board;index:idx_dh_board_fav_user;default:0"`
	BoardId  int64 `json:"board_id" gorm:"type:bigint;not null;uniqueIndex:idx_dh_board_fav_user_board;index:idx_dh_board_fav_board;default:0"`
	CreateAt int64 `json:"create_at" gorm:"type:bigint;not null;default:0"`
}

func (DhBoardFavorite) TableName() string {
	return "dh_board_favorite"
}

func DhBoardFavoriteIdsByUser(ctx *ctx.Context, userId int64) ([]int64, error) {
	var ids []int64
	err := DB(ctx).Model(&DhBoardFavorite{}).Where("user_id = ?", userId).Order("create_at DESC").Pluck("board_id", &ids).Error
	return ids, err
}

func DhBoardFavoriteAdd(ctx *ctx.Context, userId, boardId int64) error {
	exists, err := BoardExists(ctx, "id = ?", boardId)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBoardNotFound
	}

	var count int64
	if err := DB(ctx).Model(&DhBoardFavorite{}).Where("user_id = ? AND board_id = ?", userId, boardId).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	return DB(ctx).Create(&DhBoardFavorite{
		UserId:   userId,
		BoardId:  boardId,
		CreateAt: time.Now().Unix(),
	}).Error
}

func DhBoardFavoriteDel(ctx *ctx.Context, userId, boardId int64) error {
	return DB(ctx).Where("user_id = ? AND board_id = ?", userId, boardId).Delete(&DhBoardFavorite{}).Error
}
