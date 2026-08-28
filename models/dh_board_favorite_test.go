package models

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ccfos/nightingale/v6/pkg/ctx"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testBoardFavoriteCtx(t *testing.T) *ctx.Context {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Board{}, &DhBoardFavorite{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return ctx.NewContext(context.Background(), db, true)
}

func TestDhBoardFavoriteAddListDel(t *testing.T) {
	c := testBoardFavoriteCtx(t)

	board := &Board{Name: "K8S-stateless", GroupId: 1, CreateBy: "root", UpdateBy: "root"}
	if err := DB(c).Create(board).Error; err != nil {
		t.Fatalf("create board: %v", err)
	}

	if err := DhBoardFavoriteAdd(c, 7, board.Id); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := DhBoardFavoriteAdd(c, 7, board.Id); err != nil {
		t.Fatalf("add twice should be idempotent: %v", err)
	}

	ids, err := DhBoardFavoriteIdsByUser(c, 7)
	if err != nil || len(ids) != 1 || ids[0] != board.Id {
		t.Fatalf("list user 7: %v %v", err, ids)
	}

	other, err := DhBoardFavoriteIdsByUser(c, 8)
	if err != nil || len(other) != 0 {
		t.Fatalf("other user should see none: %v %v", err, other)
	}

	if err := DhBoardFavoriteAdd(c, 7, 99999); err != ErrBoardNotFound {
		t.Fatalf("missing board should fail, got %v", err)
	}

	if err := DhBoardFavoriteDel(c, 7, board.Id); err != nil {
		t.Fatalf("del: %v", err)
	}
	ids, err = DhBoardFavoriteIdsByUser(c, 7)
	if err != nil || len(ids) != 0 {
		t.Fatalf("after del: %v %v", err, ids)
	}
}
