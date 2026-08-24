package models

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ccfos/nightingale/v6/pkg/ctx"
	"github.com/ccfos/nightingale/v6/pkg/dh/serviceteam"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testServiceTeamCtx(t *testing.T) *ctx.Context {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "n9e.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&DhServiceTeam{}, &UserGroup{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return ctx.NewContext(context.Background(), db, true)
}

func TestDhServiceTeamManyToManyAndVisibility(t *testing.T) {
	c := testServiceTeamCtx(t)

	if err := DhServiceTeamReplaceForGroup(c, 10, []string{"turms-business-service", "rome-sec"}, "root"); err != nil {
		t.Fatalf("replace group 10: %v", err)
	}
	if err := DhServiceTeamReplaceForGroup(c, 20, []string{"turms-business-service"}, "sre"); err != nil {
		t.Fatalf("replace group 20: %v", err)
	}

	lst, err := DhServiceTeamGetsByNameEnv(c, "turms-business-service", "")
	if err != nil || len(lst) != 2 {
		t.Fatalf("service should have two teams, got %v %+v", err, lst)
	}

	got, err := DhServiceTeamGetsByGroupId(c, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("group 10 should have two services, got %v %+v", err, got)
	}

	// 差量：去掉 rome-sec，保留 turms，再加 trade-api
	if err := DhServiceTeamReplaceForGroup(c, 10, []string{"turms-business-service", "trade-api"}, "sre"); err != nil {
		t.Fatalf("replace group 10 again: %v", err)
	}
	got, err = DhServiceTeamGetsByGroupId(c, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("group 10 after replace: %v %+v", err, got)
	}
	names := map[string]struct{}{}
	for _, row := range got {
		names[row.ServiceName] = struct{}{}
		if row.Source != serviceteam.SourceManual {
			t.Fatalf("source should stay manual, got %+v", row)
		}
	}
	if _, ok := names["rome-sec"]; ok {
		t.Fatal("rome-sec should have been removed")
	}
	if _, ok := names["trade-api"]; !ok {
		t.Fatal("trade-api should have been added")
	}

	all, err := DhServiceTeamGets(c)
	if err != nil {
		t.Fatalf("gets: %v", err)
	}
	bindings := DhServiceTeamToBindings(all)
	if !serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{10}), bindings, "turms-business-service", "prod") {
		t.Fatal("member of first team should see shared service")
	}
	if !serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{20}), bindings, "turms-business-service", "") {
		t.Fatal("member of second team should also see shared service")
	}
	if serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{99}), bindings, "turms-business-service", "") {
		t.Fatal("other team must not see")
	}
	if serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{10}), bindings, "unbound", "") {
		t.Fatal("regular must not see unbound")
	}
	if !serviceteam.CanSee(true, nil, bindings, "unbound", "") {
		t.Fatal("view-all must see unbound")
	}

	if err := DhServiceTeamReplaceForService(c, "turms-business-service", nil, "root"); err != nil {
		t.Fatalf("clear service teams: %v", err)
	}
	gone, err := DhServiceTeamGetsByNameEnv(c, "turms-business-service", "")
	if err != nil || len(gone) != 0 {
		t.Fatalf("cleared service still has rows: %v %+v", err, gone)
	}
}

func TestDhServiceTeamLegacyRowsSurviveNewUnique(t *testing.T) {
	c := testServiceTeamCtx(t)
	if err := DhServiceTeamAdd(c, &DhServiceTeam{ServiceName: "rome-sec", UserGroupId: 7, UpdatedBy: "root"}); err != nil {
		t.Fatalf("seed rome-sec: %v", err)
	}
	if err := DhServiceTeamAdd(c, &DhServiceTeam{ServiceName: "turms-gateway", UserGroupId: 7, UpdatedBy: "root"}); err != nil {
		t.Fatalf("seed turms-gateway: %v", err)
	}
	if err := DhServiceTeamReplaceForGroup(c, 7, []string{"rome-sec", "turms-gateway", "new-svc"}, "sre"); err != nil {
		t.Fatalf("keep existing plus add: %v", err)
	}
	got, err := DhServiceTeamGetsByGroupId(c, 7)
	if err != nil || len(got) != 3 {
		t.Fatalf("expected 3 rows including original rome-sec, got %v %+v", err, got)
	}
}
