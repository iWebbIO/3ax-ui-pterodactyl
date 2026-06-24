package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestMigrateAwgWgPeerBaseline checks the one-time seeding that backfills the
// per-peer baseline and folds all_time-only traffic into download so the
// per-client traffic column is non-zero immediately after the upgrade.
func TestMigrateAwgWgPeerBaseline(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// InitDB already ran the seeding once on empty tables; undo the record so we
	// can seed real test rows.
	db.Where("seeder_name = ?", "AwgWgPeerBaseline").Delete(&model.HistoryOfSeeders{})

	// Post-bounce client: traffic survived only in all_time.
	lost := &model.AwgClient{Email: "lost", AllTime: 358, Enable: true}
	// Consistent client: up+down already equals all_time.
	ok := &model.AwgClient{Email: "ok", Upload: 10, Download: 20, AllTime: 30, Enable: true}
	if err := db.Create(lost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(ok).Error; err != nil {
		t.Fatal(err)
	}

	migrateAwgWgPeerBaseline()

	var gotLost model.AwgClient
	db.First(&gotLost, lost.Id)
	if gotLost.LastPeerUp != 0 || gotLost.LastPeerDown != 0 {
		t.Fatalf("baseline should be the old upload/download: %+v", gotLost)
	}
	if gotLost.Upload+gotLost.Download != gotLost.AllTime {
		t.Fatalf("up+down should equal all_time after seeding: up=%d down=%d all=%d", gotLost.Upload, gotLost.Download, gotLost.AllTime)
	}
	if gotLost.Download != 358 {
		t.Fatalf("all_time-only traffic should fold into download, got %d", gotLost.Download)
	}

	var gotOk model.AwgClient
	db.First(&gotOk, ok.Id)
	if gotOk.LastPeerUp != 10 || gotOk.LastPeerDown != 20 {
		t.Fatalf("baseline mismatch: %+v", gotOk)
	}
	if gotOk.Download != 20 {
		t.Fatalf("no fold expected when up+down==all_time, got down=%d", gotOk.Download)
	}

	// Idempotent: the seeder record now exists, so a second run is a no-op.
	migrateAwgWgPeerBaseline()
	db.First(&gotLost, lost.Id)
	if gotLost.Download != 358 {
		t.Fatalf("re-run must not double-fold, got down=%d", gotLost.Download)
	}
}
