package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// TestMigrateMtprotoClientsTable verifies that both legacy shapes — the interim
// settings.clients[] and the original single-secret — are moved into the
// mtproto_clients table with traffic carried across, and that the migration is
// idempotent and scoped to mtproto.
func TestMigrateMtprotoClientsTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Interim multi-user shape (commit 70a7bac0): settings.clients[].
	multi := &model.Inbound{
		Remark: "multi", Enable: true, Port: 1001, Protocol: model.MTProto, Tag: "inbound-1001",
		// tgId is the string "" and totalGB a string here — exactly how the browser
		// persisted clients in the interim shape; a strict int64 decode would skip
		// the whole inbound (the bug seen on the server).
		Settings: `{"fakeTlsDomain":"www.cloudflare.com","clients":[` +
			`{"id":"old1","email":"alice","secret":"ee0123456789abcdef0123456789abcdef7777772e636c6f7564666c6172652e636f6d","enable":true,"totalGB":"111","tgId":""},` +
			`{"id":"old2","email":"alice","secret":"eefedcba9876543210fedcba98765432107777772e636c6f7564666c6172652e636f6d","enable":false,"tgId":""}]}`,
	}
	// Legacy single-secret shape.
	single := &model.Inbound{
		Remark: "legacy", Enable: true, Port: 1002, Protocol: model.MTProto, Tag: "inbound-1002",
		Settings: `{"fakeTlsDomain":"telegram.org","secret":"ee0123456789abcdef0123456789abcdef74656c656772616d2e6f7267"}`,
	}
	if err := db.Create(multi).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(single).Error; err != nil {
		t.Fatal(err)
	}
	// A client_traffics row whose traffic should be carried onto alice's first row.
	if err := db.Create(&xray.ClientTraffic{InboundId: multi.Id, Email: "alice", Up: 10, Down: 20, AllTime: 30, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	// A non-mtproto inbound + client whose traffic must NOT be touched.
	vless := &model.Inbound{Remark: "vless", Enable: true, Port: 1003, Protocol: model.VLESS, Tag: "inbound-1003", Settings: `{"clients":[]}`}
	if err := db.Create(vless).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: vless.Id, Email: "vless-user", Up: 5, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}

	migrateMtprotoClientsTable()

	var multiRows []model.MtprotoClient
	db.Where("inbound_id = ?", multi.Id).Order("id asc").Find(&multiRows)
	if len(multiRows) != 2 {
		t.Fatalf("expected 2 rows for the multi inbound, got %d", len(multiRows))
	}
	if multiRows[0].Email != "alice" || multiRows[1].Email != "alice" {
		t.Fatal("both migrated rows should keep email alice (now non-unique)")
	}
	if multiRows[0].Uuid == multiRows[1].Uuid || multiRows[0].Uuid == "" {
		t.Fatalf("migrated rows must get distinct uuids: %q %q", multiRows[0].Uuid, multiRows[1].Uuid)
	}
	if multiRows[0].Secret != "ee0123456789abcdef0123456789abcdef7777772e636c6f7564666c6172652e636f6d" {
		t.Fatalf("secret should be preserved (domain already matches): %q", multiRows[0].Secret)
	}
	if multiRows[0].TotalGB != 111 {
		t.Fatalf("totalGB should carry over, got %d", multiRows[0].TotalGB)
	}
	if multiRows[1].Enable {
		t.Fatal("the disabled client should stay disabled")
	}
	// Traffic carried from client_traffics onto the matching (first) alice row.
	if multiRows[0].Upload != 10 || multiRows[0].Download != 20 || multiRows[0].AllTime != 30 {
		t.Fatalf("traffic not carried: up=%d down=%d all=%d", multiRows[0].Upload, multiRows[0].Download, multiRows[0].AllTime)
	}

	var singleRows []model.MtprotoClient
	db.Where("inbound_id = ?", single.Id).Find(&singleRows)
	if len(singleRows) != 1 || singleRows[0].Email != "legacy" {
		t.Fatalf("legacy single-secret should become 1 row labelled by remark, got %+v", singleRows)
	}
	if singleRows[0].Secret != "ee0123456789abcdef0123456789abcdef74656c656772616d2e6f7267" {
		t.Fatalf("legacy secret should be preserved: %q", singleRows[0].Secret)
	}

	// Settings stripped of clients/secret; non-mtproto traffic untouched.
	var reloaded model.Inbound
	db.First(&reloaded, multi.Id)
	if got := model.MtprotoFakeTLSDomain(reloaded.Settings); got != "www.cloudflare.com" {
		t.Fatalf("inbound-level settings must survive, domain=%q", got)
	}
	var leftover int64
	db.Model(&xray.ClientTraffic{}).Where("inbound_id = ?", multi.Id).Count(&leftover)
	if leftover != 0 {
		t.Fatalf("mtproto client_traffics rows should be removed, %d left", leftover)
	}
	var untouched int64
	db.Model(&xray.ClientTraffic{}).Where("email = ?", "vless-user").Count(&untouched)
	if untouched != 1 {
		t.Fatal("non-mtproto client_traffics must not be deleted")
	}

	// Idempotent: a second run adds nothing.
	migrateMtprotoClientsTable()
	var total int64
	db.Model(&model.MtprotoClient{}).Count(&total)
	if total != 3 {
		t.Fatalf("migration not idempotent, total rows = %d", total)
	}
}
