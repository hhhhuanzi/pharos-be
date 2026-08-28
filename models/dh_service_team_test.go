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

func seedUserGroup(t *testing.T, c *ctx.Context, id int64, name string) {
	t.Helper()
	ug := UserGroup{Id: id, Name: name}
	if err := DB(c).Create(&ug).Error; err != nil {
		t.Fatalf("seed user group %d: %v", id, err)
	}
}

func TestDhServiceTeamOneTeamPerService(t *testing.T) {
	c := testServiceTeamCtx(t)
	seedUserGroup(t, c, 10, "turms")
	seedUserGroup(t, c, 20, "rome-sec")

	if err := DhServiceTeamReplaceForGroup(c, 10, []string{"turms-business-service", "rome-sec-admin"}, "root"); err != nil {
		t.Fatalf("replace group 10: %v", err)
	}
	if err := DhServiceTeamReplaceForGroup(c, 20, []string{"turms-business-service"}, "sre"); err == nil {
		t.Fatal("second team on the same service should be rejected")
	}

	lst, err := DhServiceTeamGetsByNameEnv(c, "turms-business-service", "")
	if err != nil || len(lst) != 1 || lst[0].UserGroupId != 10 {
		t.Fatalf("service should stay on first team, got %v %+v", err, lst)
	}

	got, err := DhServiceTeamGetsByGroupId(c, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("group 10 should have two services, got %v %+v", err, got)
	}

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
	if _, ok := names["rome-sec-admin"]; ok {
		t.Fatal("rome-sec-admin should have been removed")
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
		t.Fatal("member of bound team should see service")
	}
	if serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{20}), bindings, "turms-business-service", "") {
		t.Fatal("other team must not see")
	}
	if serviceteam.CanSee(false, serviceteam.GroupIDSet([]int64{10}), bindings, "unbound", "") {
		t.Fatal("regular must not see unbound")
	}
	if !serviceteam.CanSee(true, nil, bindings, "unbound", "") {
		t.Fatal("view-all must see unbound")
	}

	if err := DhServiceTeamReplaceForService(c, "turms-business-service", []int64{10, 20}, "root"); err != serviceteam.ErrMultipleTeams {
		t.Fatalf("want ErrMultipleTeams, got %v", err)
	}

	if err := DhServiceTeamReplaceForService(c, "turms-business-service", nil, "root"); err != nil {
		t.Fatalf("clear service teams: %v", err)
	}
	gone, err := DhServiceTeamGetsByNameEnv(c, "turms-business-service", "")
	if err != nil || len(gone) != 0 {
		t.Fatalf("cleared service still has rows: %v %+v", err, gone)
	}
}

func TestDhServiceTeamReplaceForServiceFixesDirtyRows(t *testing.T) {
	c := testServiceTeamCtx(t)
	seedUserGroup(t, c, 10, "turms")
	seedUserGroup(t, c, 20, "rome-sec")
	if err := DB(c).Create(&DhServiceTeam{ServiceName: "turms-gateway", UserGroupId: 10, Source: serviceteam.SourceManual}).Error; err != nil {
		t.Fatalf("seed first: %v", err)
	}
	if err := DB(c).Create(&DhServiceTeam{ServiceName: "turms-gateway", UserGroupId: 20, Source: serviceteam.SourceManual}).Error; err != nil {
		t.Fatalf("seed dirty second: %v", err)
	}
	if err := DhServiceTeamReplaceForService(c, "turms-gateway", []int64{10}, "root"); err != nil {
		t.Fatalf("reduce dirty rows: %v", err)
	}
	got, err := DhServiceTeamGetsByNameEnv(c, "turms-gateway", "")
	if err != nil || len(got) != 1 || got[0].UserGroupId != 10 {
		t.Fatalf("expected one remaining row, got %v %+v", err, got)
	}
}

func TestDhServiceTeamRejectsInvalidGroupName(t *testing.T) {
	c := testServiceTeamCtx(t)
	seedUserGroup(t, c, 7, "Turms_1")
	if err := DhServiceTeamReplaceForGroup(c, 7, []string{"turms-gateway"}, "root"); err != serviceteam.ErrInvalidTeamName {
		t.Fatalf("want ErrInvalidTeamName, got %v", err)
	}
}

func TestDhServiceTeamLegacyRowsSurviveNewUnique(t *testing.T) {
	c := testServiceTeamCtx(t)
	seedUserGroup(t, c, 7, "rome-sec")
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
