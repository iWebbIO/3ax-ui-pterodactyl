// Package database provides database initialization, migration, and management utilities
// for the 3AX-UI panel using GORM with SQLite.
package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/crypto"
	"github.com/coinman-dev/3ax-ui/v2/xray"
	"github.com/google/uuid"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

const (
	defaultUsername = "admin"
	defaultPassword = "admin"
)

func initModels() error {
	models := []any{
		&model.User{},
		&model.Inbound{},
		&model.OutboundTraffics{},
		&model.Setting{},
		&model.InboundClientIps{},
		&xray.ClientTraffic{},
		&model.HistoryOfSeeders{},
		&model.AwgServer{},
		&model.AwgClient{},
		&model.WgServer{},
		&model.WgClient{},
		&model.CustomGeoResource{},
	}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil {
			log.Printf("Error auto migrating model: %v", err)
			return err
		}
	}
	return nil
}

// initUser creates a default admin user if the users table is empty.
func initUser() error {
	empty, err := isTableEmpty("users")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}
	if empty {
		hashedPassword, err := crypto.HashPasswordAsBcrypt(defaultPassword)

		if err != nil {
			log.Printf("Error hashing default password: %v", err)
			return err
		}

		user := &model.User{
			Username: defaultUsername,
			Password: hashedPassword,
		}
		return db.Create(user).Error
	}
	return nil
}

// runSeeders migrates user passwords to bcrypt and records seeder execution to prevent re-running.
func runSeeders(isUsersEmpty bool) error {
	empty, err := isTableEmpty("history_of_seeders")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}

	if empty && isUsersEmpty {
		hashSeeder := &model.HistoryOfSeeders{
			SeederName: "UserPasswordHash",
		}
		return db.Create(hashSeeder).Error
	} else {
		var seedersHistory []string
		db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &seedersHistory)

		if !slices.Contains(seedersHistory, "UserPasswordHash") && !isUsersEmpty {
			var users []model.User
			db.Find(&users)

			for _, user := range users {
				hashedPassword, err := crypto.HashPasswordAsBcrypt(user.Password)
				if err != nil {
					log.Printf("Error hashing password for user '%s': %v", user.Username, err)
					return err
				}
				db.Model(&user).Update("password", hashedPassword)
			}

			hashSeeder := &model.HistoryOfSeeders{
				SeederName: "UserPasswordHash",
			}
			return db.Create(hashSeeder).Error
		}
	}

	return nil
}

// isTableEmpty returns true if the named table contains zero rows.
func isTableEmpty(tableName string) (bool, error) {
	var count int64
	err := db.Table(tableName).Count(&count).Error
	return count == 0, err
}

// InitDB sets up the database connection, migrates models, and runs seeders.
func InitDB(dbPath string) error {
	dir := path.Dir(dbPath)
	err := os.MkdirAll(dir, fs.ModePerm)
	if err != nil {
		return err
	}

	var gormLogger logger.Interface

	if config.IsDebug() {
		gormLogger = logger.Default
	} else {
		gormLogger = logger.Discard
	}

	c := &gorm.Config{
		Logger: gormLogger,
	}
	dsn := dbPath
	separator := "?"
	if strings.Contains(dbPath, "?") {
		separator = "&"
	}
	dsn = fmt.Sprintf("%s%s_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON", dbPath, separator)

	db, err = gorm.Open(sqlite.Open(dsn), c)
	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
		return err
	}
	if err := db.Exec("PRAGMA busy_timeout = 5000;").Error; err != nil {
		return err
	}
	if err := db.Exec("PRAGMA synchronous = NORMAL;").Error; err != nil {
		return err
	}
	if err := db.Exec("PRAGMA foreign_keys = ON;").Error; err != nil {
		return err
	}

	// Add the AmneziaWG 2.0 / split-DNS columns ourselves BEFORE AutoMigrate to
	// avoid a GORM SQLite "duplicate column name" failure when it adds columns
	// while also rebuilding awg_servers for the H1-H4 int→string type change.
	preMigrateAwgWgColumns()

	if err := initModels(); err != nil {
		return err
	}

	// Populate UUIDs for existing AWG clients that don't have one
	migrateAwgClientUUIDs()

	// Convert legacy mixed/http inbounds from settings.accounts[] to settings.clients[]
	// so they share the rich per-user infrastructure (traffic, expiry, quota) with VLESS.
	migrateMixedHttpAccounts()

	// Split the legacy combined AWG/WG `dns` column into dns_ipv4 / dns_ipv6 so
	// an upgrade preserves the installed DNS instead of reverting to defaults.
	migrateAwgWgDnsSplit()

	// Convert legacy single-secret MTProto inbounds to the unified multi-user
	// clients[] shape (existing secret becomes client #1; its link keeps working).
	migrateMtprotoToClients()

	isUsersEmpty, err := isTableEmpty("users")
	if err != nil {
		return err
	}

	if err := initUser(); err != nil {
		return err
	}
	return runSeeders(isUsersEmpty)
}

// migrateAwgClientUUIDs populates UUIDs for existing AWG clients that have empty UUIDs.
func migrateAwgClientUUIDs() {
	var clients []model.AwgClient
	db.Where("uuid = '' OR uuid IS NULL").Find(&clients)
	for _, client := range clients {
		client.UUID = uuid.New().String()
		db.Model(&client).Update("uuid", client.UUID)
	}
}

// migrateAwgWgDnsSplit moves the legacy combined `dns` column of awg_servers /
// wg_servers into the per-family dns_ipv4 / dns_ipv6 columns, so an upgrade
// preserves the installed DNS instead of reverting to defaults. Idempotent: the
// old `dns` value is cleared once split, so it runs only on the first start
// after the upgrade (and is skipped on fresh installs where the column is gone).
func migrateAwgWgDnsSplit() {
	for _, table := range []string{"awg_servers", "wg_servers"} {
		if !legacyDnsColumnExists(table) {
			continue
		}
		type dnsRow struct {
			Id  int
			Dns string
		}
		var rows []dnsRow
		db.Raw(fmt.Sprintf("SELECT id, dns FROM %s WHERE dns IS NOT NULL AND dns != ''", table)).Scan(&rows)
		for _, r := range rows {
			v4, v6 := splitDnsByFamily(r.Dns)
			if err := db.Exec(
				fmt.Sprintf("UPDATE %s SET dns_ipv4 = ?, dns_ipv6 = ?, dns = '' WHERE id = ?", table),
				v4, v6, r.Id,
			).Error; err != nil {
				log.Printf("migrateAwgWgDnsSplit: update %s id=%d failed: %v", table, r.Id, err)
			}
		}
	}
}

// legacyDnsColumnExists reports whether the table still has the old `dns` column.
func legacyDnsColumnExists(table string) bool {
	return columnExists(table, "dns")
}

// tableExists reports whether a table exists in the database.
func tableExists(table string) bool {
	var n int64
	db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
	return n > 0
}

// columnExists reports whether the given table has the given column.
func columnExists(table, col string) bool {
	var n int64
	db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", table), col).Scan(&n)
	return n > 0
}

// preMigrateAwgWgColumns adds the AmneziaWG 2.0 / split-DNS columns to the
// existing awg_servers / wg_servers tables BEFORE GORM AutoMigrate runs. Doing
// the adds ourselves (idempotently) avoids a GORM SQLite quirk: adding columns
// while it also rebuilds awg_servers for the H1-H4 int→string type change can
// fail with "duplicate column name". No-op on fresh installs (the tables don't
// exist yet — AutoMigrate then creates them with the full schema).
func preMigrateAwgWgColumns() {
	add := func(table, col, ddl string) {
		if !tableExists(table) || columnExists(table, col) {
			return
		}
		if err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, ddl)).Error; err != nil {
			log.Printf("preMigrateAwgWgColumns: add %s.%s failed: %v", table, col, err)
		}
	}
	for _, t := range []string{"awg_servers", "wg_servers"} {
		add(t, "dns_ipv4", "text DEFAULT '1.1.1.1'")
		add(t, "dns_ipv6", "text DEFAULT '2606:4700:4700::1111'")
	}
	// AmneziaWG-only obfuscation columns (native WireGuard has no obfuscation).
	add("awg_servers", "s3", "integer DEFAULT 0")
	add("awg_servers", "s4", "integer DEFAULT 0")
	add("awg_servers", "i1", "text DEFAULT ''")
}

// splitDnsByFamily splits a comma-separated DNS list into IPv4 and IPv6 groups
// (IPv6 entries are detected by the ':' character).
func splitDnsByFamily(combined string) (ipv4, ipv6 string) {
	var v4, v6 []string
	for _, p := range strings.Split(combined, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, ":") {
			v6 = append(v6, p)
		} else {
			v4 = append(v4, p)
		}
	}
	return strings.Join(v4, ","), strings.Join(v6, ",")
}

// migrateMixedHttpAccounts rewrites legacy mixed/http inbound settings:
// the old shape stored basic-auth users as settings.accounts[]={user,pass};
// the new shape stores them as settings.clients[]={email,password,...} so the
// shared VLESS-style CRUD, traffic, expiry, and quota machinery applies.
// Idempotent — inbounds already in the new shape are skipped.
func migrateMixedHttpAccounts() {
	var inbounds []model.Inbound
	if err := db.Where("protocol IN ?", []string{"mixed", "http"}).Find(&inbounds).Error; err != nil {
		log.Printf("migrateMixedHttpAccounts: scan inbounds failed: %v", err)
		return
	}
	for i := range inbounds {
		inbound := &inbounds[i]
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			log.Printf("migrateMixedHttpAccounts: inbound %d settings unmarshal failed: %v", inbound.Id, err)
			continue
		}
		if _, hasClients := settings["clients"]; hasClients {
			// Already migrated.
			continue
		}
		rawAccounts, hasAccounts := settings["accounts"]
		if !hasAccounts {
			continue
		}
		accounts, ok := rawAccounts.([]any)
		if !ok || len(accounts) == 0 {
			delete(settings, "accounts")
			if newSettings, err := json.MarshalIndent(settings, "", "  "); err == nil {
				db.Model(inbound).Update("settings", string(newSettings))
			}
			continue
		}

		nowMs := time.Now().UnixMilli()
		clients := make([]map[string]any, 0, len(accounts))
		for _, a := range accounts {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			user, _ := am["user"].(string)
			pass, _ := am["pass"].(string)
			if user == "" {
				continue
			}
			clients = append(clients, map[string]any{
				"email":      user,
				"password":   pass,
				"limitIp":    0,
				"totalGB":    0,
				"expiryTime": 0,
				"enable":     true,
				"tgId":       0,
				"subId":      "",
				"comment":    "",
				"reset":      0,
				"created_at": nowMs,
				"updated_at": nowMs,
			})
		}

		settings["clients"] = clients
		delete(settings, "accounts")

		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			log.Printf("migrateMixedHttpAccounts: inbound %d marshal failed: %v", inbound.Id, err)
			continue
		}
		if err := db.Model(inbound).Update("settings", string(newSettings)).Error; err != nil {
			log.Printf("migrateMixedHttpAccounts: inbound %d save failed: %v", inbound.Id, err)
			continue
		}
		log.Printf("migrateMixedHttpAccounts: migrated %d %s account(s) in inbound %d", len(clients), inbound.Protocol, inbound.Id)
	}
}

// migrateMtprotoToClients converts legacy single-secret MTProto inbounds
// (settings.secret + fakeTlsDomain) into the unified multi-user shape
// (settings.clients[] with one client carrying the existing secret). The
// existing user's t.me link (same port + same secret) keeps working, and a
// client_traffics row is created so it appears in the per-client UI. Idempotent:
// inbounds already carrying a clients[] array are skipped.
func migrateMtprotoToClients() {
	var inbounds []model.Inbound
	if err := db.Where("protocol = ?", model.MTProto).Find(&inbounds).Error; err != nil {
		log.Printf("migrateMtprotoToClients: scan inbounds failed: %v", err)
		return
	}
	for i := range inbounds {
		inbound := &inbounds[i]
		var settings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			log.Printf("migrateMtprotoToClients: inbound %d settings unmarshal failed: %v", inbound.Id, err)
			continue
		}
		if _, has := settings["clients"]; has {
			continue // already migrated
		}
		secret, _ := settings["secret"].(string)
		if strings.TrimSpace(secret) == "" {
			continue // nothing to carry over
		}

		email := strings.TrimSpace(inbound.Remark)
		if email == "" {
			email = fmt.Sprintf("mtproto-%d", inbound.Port)
		}
		email = uniqueClientEmail(email)

		nowMs := time.Now().UnixMilli()
		client := map[string]any{
			"id":         model.GenerateMtprotoClientID(),
			"secret":     secret,
			"email":      email,
			"enable":     true,
			"limitIp":    0,
			"totalGB":    0,
			"expiryTime": 0,
			"tgId":       0,
			"subId":      "",
			"comment":    "",
			"reset":      0,
			"created_at": nowMs,
			"updated_at": nowMs,
		}
		settings["clients"] = []any{client}
		delete(settings, "secret")

		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			log.Printf("migrateMtprotoToClients: inbound %d marshal failed: %v", inbound.Id, err)
			continue
		}
		if err := db.Model(inbound).Update("settings", string(newSettings)).Error; err != nil {
			log.Printf("migrateMtprotoToClients: inbound %d save failed: %v", inbound.Id, err)
			continue
		}
		var cnt int64
		db.Model(&xray.ClientTraffic{}).Where("email = ?", email).Count(&cnt)
		if cnt == 0 {
			if err := db.Create(&xray.ClientTraffic{InboundId: inbound.Id, Email: email, Enable: true}).Error; err != nil {
				log.Printf("migrateMtprotoToClients: inbound %d client_traffics create failed: %v", inbound.Id, err)
			}
		}
		log.Printf("migrateMtprotoToClients: converted inbound %d to 1-client multi-user shape (email %q)", inbound.Id, email)
	}
}

// uniqueClientEmail returns base, or base-2, base-3, ... if a client_traffics row
// with that email already exists (emails are globally unique).
func uniqueClientEmail(base string) string {
	email := base
	for n := 2; n < 1000; n++ {
		var cnt int64
		db.Model(&xray.ClientTraffic{}).Where("email = ?", email).Count(&cnt)
		if cnt == 0 {
			return email
		}
		email = fmt.Sprintf("%s-%d", base, n)
	}
	return email
}

// CloseDB closes the database connection if it exists.
func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

// GetDB returns the global GORM database instance.
func GetDB() *gorm.DB {
	return db
}

func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// IsSQLiteDB checks if the given file is a valid SQLite database by reading its signature.
func IsSQLiteDB(file io.ReaderAt) (bool, error) {
	signature := []byte("SQLite format 3\x00")
	buf := make([]byte, len(signature))
	_, err := file.ReadAt(buf, 0)
	if err != nil {
		return false, err
	}
	return bytes.Equal(buf, signature), nil
}

// Checkpoint performs a WAL checkpoint on the SQLite database to ensure data consistency.
func Checkpoint() error {
	// Update WAL
	err := db.Exec("PRAGMA wal_checkpoint;").Error
	if err != nil {
		return err
	}
	return nil
}

// ValidateSQLiteDB opens the provided sqlite DB path with a throw-away connection
// and runs a PRAGMA integrity_check to ensure the file is structurally sound.
// It does not mutate global state or run migrations.
func ValidateSQLiteDB(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil { // file must exist
		return err
	}
	gdb, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var res string
	if err := gdb.Raw("PRAGMA integrity_check;").Scan(&res).Error; err != nil {
		return err
	}
	if res != "ok" {
		return errors.New("sqlite integrity check failed: " + res)
	}
	return nil
}
