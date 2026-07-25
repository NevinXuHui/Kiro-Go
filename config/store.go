package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"kiro-go/logger"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// db is the SQLite handle. Nil means "JSON-only mode" (tests that never open a DB
// still work if they only exercise pure in-memory helpers; production Init always
// opens a database).
var db *sql.DB

// dbPath is the absolute/relative path of the SQLite file.
var dbPath string

// useDB reports whether persistence should go through SQLite.
func useDB() bool {
	return db != nil
}

// openDB opens (or creates) the SQLite database at path and applies schema.
func openDB(path string) error {
	if path == "" {
		return errors.New("empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}
	// _pragma=busy_timeout + WAL: safe concurrent readers while a writer flushes stats.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	handle.SetMaxOpenConns(1) // SQLite single-writer; serialize via one connection
	handle.SetMaxIdleConns(1)
	handle.SetConnMaxLifetime(0)
	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applySchema(handle); err != nil {
		_ = handle.Close()
		return err
	}
	if db != nil {
		_ = db.Close()
	}
	db = handle
	dbPath = path
	return nil
}

func applySchema(handle *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS accounts (
  id   TEXT PRIMARY KEY,
  data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS api_keys (
  id   TEXT PRIMARY KEY,
  data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS daily_stats_history (
  date              TEXT PRIMARY KEY,
  requests          INTEGER NOT NULL DEFAULT 0,
  success_requests  INTEGER NOT NULL DEFAULT 0,
  failed_requests   INTEGER NOT NULL DEFAULT 0,
  tokens            INTEGER NOT NULL DEFAULT 0,
  credits           REAL    NOT NULL DEFAULT 0,
  archived_at       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS schema_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS request_logs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  time          INTEGER NOT NULL,
  endpoint      TEXT    NOT NULL DEFAULT '',
  model         TEXT    NOT NULL DEFAULT '',
  account_id    TEXT    NOT NULL DEFAULT '',
  account_email TEXT    NOT NULL DEFAULT '',
  status        TEXT    NOT NULL DEFAULT '',
  error         TEXT    NOT NULL DEFAULT '',
  error_type    TEXT    NOT NULL DEFAULT '',
  tokens        INTEGER NOT NULL DEFAULT 0,
  credits       REAL    NOT NULL DEFAULT 0,
  duration      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_request_logs_time ON request_logs(time DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_status_id ON request_logs(status, id DESC);
INSERT OR IGNORE INTO schema_meta(key, value) VALUES('version', '1');
`
	if _, err := handle.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// resolveDBPath picks the SQLite file path.
// Priority: DATABASE_PATH env → sibling of config JSON → data/kiro.db.
func resolveDBPath(configJSONPath string) string {
	if env := strings.TrimSpace(os.Getenv("DATABASE_PATH")); env != "" {
		return env
	}
	dir := filepath.Dir(configJSONPath)
	if dir == "" || dir == "." {
		return filepath.Join("data", "kiro.db")
	}
	return filepath.Join(dir, "kiro.db")
}

// dbHasData returns true when the database already holds settings or accounts.
func dbHasData() (bool, error) {
	if db == nil {
		return false, nil
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM meta`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// loadFromDB populates cfg from SQLite. Caller must hold cfgLock.
func loadFromDB() error {
	if db == nil {
		return errors.New("database not open")
	}
	c := defaultConfig()

	// meta: settings + stats as JSON blobs under fixed keys
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		if err := applyMetaKey(&c, key, value); err != nil {
			return fmt.Errorf("meta %s: %w", key, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// accounts
	arows, err := db.Query(`SELECT data FROM accounts`)
	if err != nil {
		return err
	}
	defer arows.Close()
	for arows.Next() {
		var raw string
		if err := arows.Scan(&raw); err != nil {
			return err
		}
		var a Account
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return fmt.Errorf("account json: %w", err)
		}
		c.Accounts = append(c.Accounts, a)
	}
	if err := arows.Err(); err != nil {
		return err
	}

	// api keys
	krows, err := db.Query(`SELECT data FROM api_keys`)
	if err != nil {
		return err
	}
	defer krows.Close()
	for krows.Next() {
		var raw string
		if err := krows.Scan(&raw); err != nil {
			return err
		}
		var k ApiKeyEntry
		if err := json.Unmarshal([]byte(raw), &k); err != nil {
			return fmt.Errorf("api_key json: %w", err)
		}
		c.ApiKeys = append(c.ApiKeys, k)
	}
	if err := krows.Err(); err != nil {
		return err
	}

	// daily history
	hrows, err := db.Query(`
		SELECT date, requests, success_requests, failed_requests, tokens, credits, archived_at
		FROM daily_stats_history ORDER BY date ASC`)
	if err != nil {
		return err
	}
	defer hrows.Close()
	for hrows.Next() {
		var e DailyStatsEntry
		if err := hrows.Scan(&e.Date, &e.Requests, &e.SuccessRequests, &e.FailedRequests, &e.Tokens, &e.Credits, &e.ArchivedAt); err != nil {
			return err
		}
		c.DailyStatsHistory = append(c.DailyStatsHistory, e)
	}
	if err := hrows.Err(); err != nil {
		return err
	}

	cfg = &c
	return nil
}

func defaultConfig() Config {
	return Config{
		Password:      "changeme",
		Port:          8080,
		Host:          "0.0.0.0",
		RequireApiKey: false,
		Accounts:      []Account{},
	}
}

// applyMetaKey merges one meta row into config.
func applyMetaKey(c *Config, key, value string) error {
	switch key {
	case "settings":
		// Partial JSON over settings fields (not accounts/stats/history).
		var s settingsBlob
		if err := json.Unmarshal([]byte(value), &s); err != nil {
			return err
		}
		s.apply(c)
		return nil
	case "stats":
		var s statsBlob
		if err := json.Unmarshal([]byte(value), &s); err != nil {
			return err
		}
		s.apply(c)
		return nil
	default:
		// Ignore unknown keys for forward compatibility.
		return nil
	}
}

// settingsBlob holds non-account, non-stats configuration.
type settingsBlob struct {
	Password                 string             `json:"password,omitempty"`
	Port                     int                `json:"port,omitempty"`
	Host                     string             `json:"host,omitempty"`
	ApiKey                   string             `json:"apiKey,omitempty"`
	RequireApiKey            bool               `json:"requireApiKey,omitempty"`
	KiroVersion              string             `json:"kiroVersion,omitempty"`
	SystemVersion            string             `json:"systemVersion,omitempty"`
	NodeVersion              string             `json:"nodeVersion,omitempty"`
	ThinkingSuffix           string             `json:"thinkingSuffix,omitempty"`
	OpenAIThinkingFormat     string             `json:"openaiThinkingFormat,omitempty"`
	ClaudeThinkingFormat     string             `json:"claudeThinkingFormat,omitempty"`
	PreferredEndpoint        string             `json:"preferredEndpoint,omitempty"`
	EndpointFallback         *bool              `json:"endpointFallback,omitempty"`
	AllowOverUsage           bool               `json:"allowOverUsage,omitempty"`
	ShowExhaustedAccounts    *bool              `json:"showExhaustedAccounts,omitempty"`
	BatchTestConcurrency     int                `json:"batchTestConcurrency,omitempty"`
	ImportConcurrency        int                `json:"importConcurrency,omitempty"`
	MaxInFlightRequests      int                `json:"maxInFlightRequests,omitempty"`
	ProxyURL                 string             `json:"proxyURL,omitempty"`
	FilterClaudeCode         bool               `json:"filterClaudeCode,omitempty"`
	FilterEnvNoise           bool               `json:"filterEnvNoise,omitempty"`
	FilterStripBoundaries    bool               `json:"filterStripBoundaries,omitempty"`
	PromptFilterRules        []PromptFilterRule `json:"promptFilterRules,omitempty"`
	LogLevel                 string             `json:"logLevel,omitempty"`
	SanitizeClaudeCodePrompt bool               `json:"sanitizeClaudeCodePrompt,omitempty"`
}

func settingsFromConfig(c *Config) settingsBlob {
	return settingsBlob{
		Password:                 c.Password,
		Port:                     c.Port,
		Host:                     c.Host,
		ApiKey:                   c.ApiKey,
		RequireApiKey:            c.RequireApiKey,
		KiroVersion:              c.KiroVersion,
		SystemVersion:            c.SystemVersion,
		NodeVersion:              c.NodeVersion,
		ThinkingSuffix:           c.ThinkingSuffix,
		OpenAIThinkingFormat:     c.OpenAIThinkingFormat,
		ClaudeThinkingFormat:     c.ClaudeThinkingFormat,
		PreferredEndpoint:        c.PreferredEndpoint,
		EndpointFallback:         c.EndpointFallback,
		AllowOverUsage:           c.AllowOverUsage,
		ShowExhaustedAccounts:    c.ShowExhaustedAccounts,
		BatchTestConcurrency:     c.BatchTestConcurrency,
		ImportConcurrency:        c.ImportConcurrency,
		MaxInFlightRequests:      c.MaxInFlightRequests,
		ProxyURL:                 c.ProxyURL,
		FilterClaudeCode:         c.FilterClaudeCode,
		FilterEnvNoise:           c.FilterEnvNoise,
		FilterStripBoundaries:    c.FilterStripBoundaries,
		PromptFilterRules:        c.PromptFilterRules,
		LogLevel:                 c.LogLevel,
		SanitizeClaudeCodePrompt: c.SanitizeClaudeCodePrompt,
	}
}

func (s settingsBlob) apply(c *Config) {
	if s.Password != "" {
		c.Password = s.Password
	}
	if s.Port != 0 {
		c.Port = s.Port
	}
	if s.Host != "" {
		c.Host = s.Host
	}
	c.ApiKey = s.ApiKey
	c.RequireApiKey = s.RequireApiKey
	c.KiroVersion = s.KiroVersion
	c.SystemVersion = s.SystemVersion
	c.NodeVersion = s.NodeVersion
	c.ThinkingSuffix = s.ThinkingSuffix
	c.OpenAIThinkingFormat = s.OpenAIThinkingFormat
	c.ClaudeThinkingFormat = s.ClaudeThinkingFormat
	c.PreferredEndpoint = s.PreferredEndpoint
	c.EndpointFallback = s.EndpointFallback
	c.AllowOverUsage = s.AllowOverUsage
	c.ShowExhaustedAccounts = s.ShowExhaustedAccounts
	c.BatchTestConcurrency = s.BatchTestConcurrency
	c.ImportConcurrency = s.ImportConcurrency
	c.MaxInFlightRequests = s.MaxInFlightRequests
	c.ProxyURL = s.ProxyURL
	c.FilterClaudeCode = s.FilterClaudeCode
	c.FilterEnvNoise = s.FilterEnvNoise
	c.FilterStripBoundaries = s.FilterStripBoundaries
	c.PromptFilterRules = s.PromptFilterRules
	c.LogLevel = s.LogLevel
	c.SanitizeClaudeCodePrompt = s.SanitizeClaudeCodePrompt
}

type statsBlob struct {
	TotalRequests        int     `json:"totalRequests,omitempty"`
	SuccessRequests      int     `json:"successRequests,omitempty"`
	FailedRequests       int     `json:"failedRequests,omitempty"`
	TotalTokens          int     `json:"totalTokens,omitempty"`
	TotalCredits         float64 `json:"totalCredits,omitempty"`
	DailyRequests        int     `json:"dailyRequests,omitempty"`
	DailySuccessRequests int     `json:"dailySuccessRequests,omitempty"`
	DailyFailedRequests  int     `json:"dailyFailedRequests,omitempty"`
	DailyTokens          int     `json:"dailyTokens,omitempty"`
	DailyCredits         float64 `json:"dailyCredits,omitempty"`
	DailyDate            string  `json:"dailyDate,omitempty"`
}

func statsFromConfig(c *Config) statsBlob {
	return statsBlob{
		TotalRequests:        c.TotalRequests,
		SuccessRequests:      c.SuccessRequests,
		FailedRequests:       c.FailedRequests,
		TotalTokens:          c.TotalTokens,
		TotalCredits:         c.TotalCredits,
		DailyRequests:        c.DailyRequests,
		DailySuccessRequests: c.DailySuccessRequests,
		DailyFailedRequests:  c.DailyFailedRequests,
		DailyTokens:          c.DailyTokens,
		DailyCredits:         c.DailyCredits,
		DailyDate:            c.DailyDate,
	}
}

func (s statsBlob) apply(c *Config) {
	c.TotalRequests = s.TotalRequests
	c.SuccessRequests = s.SuccessRequests
	c.FailedRequests = s.FailedRequests
	c.TotalTokens = s.TotalTokens
	c.TotalCredits = s.TotalCredits
	c.DailyRequests = s.DailyRequests
	c.DailySuccessRequests = s.DailySuccessRequests
	c.DailyFailedRequests = s.DailyFailedRequests
	c.DailyTokens = s.DailyTokens
	c.DailyCredits = s.DailyCredits
	c.DailyDate = s.DailyDate
}

// saveToDB writes the full in-memory cfg into SQLite (transaction).
// Caller must hold cfgLock.
func saveToDB() error {
	if db == nil {
		return errors.New("database not open")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertMetaTx(tx, "settings", settingsFromConfig(cfg)); err != nil {
		return err
	}
	if err := upsertMetaTx(tx, "stats", statsFromConfig(cfg)); err != nil {
		return err
	}

	// Full replace for accounts / api keys to handle deletes cleanly.
	if _, err := tx.Exec(`DELETE FROM accounts`); err != nil {
		return err
	}
	accStmt, err := tx.Prepare(`INSERT INTO accounts(id, data) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer accStmt.Close()
	for i := range cfg.Accounts {
		raw, err := json.Marshal(cfg.Accounts[i])
		if err != nil {
			return err
		}
		id := cfg.Accounts[i].ID
		if id == "" {
			id = newUUID()
			cfg.Accounts[i].ID = id
		}
		if _, err := accStmt.Exec(id, string(raw)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM api_keys`); err != nil {
		return err
	}
	keyStmt, err := tx.Prepare(`INSERT INTO api_keys(id, data) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer keyStmt.Close()
	for i := range cfg.ApiKeys {
		raw, err := json.Marshal(cfg.ApiKeys[i])
		if err != nil {
			return err
		}
		id := cfg.ApiKeys[i].ID
		if id == "" {
			id = newUUID()
			cfg.ApiKeys[i].ID = id
		}
		if _, err := keyStmt.Exec(id, string(raw)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM daily_stats_history`); err != nil {
		return err
	}
	histStmt, err := tx.Prepare(`
		INSERT INTO daily_stats_history(date, requests, success_requests, failed_requests, tokens, credits, archived_at)
		VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer histStmt.Close()
	for _, e := range cfg.DailyStatsHistory {
		if _, err := histStmt.Exec(e.Date, e.Requests, e.SuccessRequests, e.FailedRequests, e.Tokens, e.Credits, e.ArchivedAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func upsertMetaTx(tx *sql.Tx, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(raw))
	return err
}

// dbUpsertAccount persists a single account row. Caller holds cfgLock.
func dbUpsertAccount(a Account) error {
	if db == nil {
		return errors.New("database not open")
	}
	if a.ID == "" {
		return errors.New("account id empty")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO accounts(id, data) VALUES(?, ?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`, a.ID, string(raw))
	return err
}

// dbDeleteAccount removes one account row. Caller holds cfgLock.
func dbDeleteAccount(id string) error {
	if db == nil {
		return errors.New("database not open")
	}
	_, err := db.Exec(`DELETE FROM accounts WHERE id=?`, id)
	return err
}

// dbDeleteAccounts removes many account rows. Caller holds cfgLock.
func dbDeleteAccounts(ids []string) error {
	if db == nil {
		return errors.New("database not open")
	}
	if len(ids) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`DELETE FROM accounts WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// dbSaveMeta persists settings + stats only (not accounts). Caller holds cfgLock.
func dbSaveMeta() error {
	if db == nil {
		return errors.New("database not open")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertMetaTx(tx, "settings", settingsFromConfig(cfg)); err != nil {
		return err
	}
	if err := upsertMetaTx(tx, "stats", statsFromConfig(cfg)); err != nil {
		return err
	}
	return tx.Commit()
}

// dbSaveStats persists stats + daily history (hot path for runtime flushes).
// Caller holds cfgLock.
func dbSaveStatsAndHistory() error {
	if db == nil {
		return errors.New("database not open")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertMetaTx(tx, "stats", statsFromConfig(cfg)); err != nil {
		return err
	}
	// Upsert history rows (keep existing other dates)
	for _, e := range cfg.DailyStatsHistory {
		if _, err := tx.Exec(`
			INSERT INTO daily_stats_history(date, requests, success_requests, failed_requests, tokens, credits, archived_at)
			VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(date) DO UPDATE SET
				requests=excluded.requests,
				success_requests=excluded.success_requests,
				failed_requests=excluded.failed_requests,
				tokens=excluded.tokens,
				credits=excluded.credits,
				archived_at=excluded.archived_at
		`, e.Date, e.Requests, e.SuccessRequests, e.FailedRequests, e.Tokens, e.Credits, e.ArchivedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// dbUpsertAccounts batch-writes account rows (stats flush). Caller holds cfgLock.
func dbUpsertAccounts(accounts []Account) error {
	if db == nil {
		return errors.New("database not open")
	}
	if len(accounts) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO accounts(id, data) VALUES(?, ?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range accounts {
		if accounts[i].ID == "" {
			continue
		}
		raw, err := json.Marshal(accounts[i])
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(accounts[i].ID, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// dbUpsertApiKey persists one API key. Caller holds cfgLock.
func dbUpsertApiKey(k ApiKeyEntry) error {
	if db == nil {
		return errors.New("database not open")
	}
	if k.ID == "" {
		return errors.New("api key id empty")
	}
	raw, err := json.Marshal(k)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO api_keys(id, data) VALUES(?, ?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`, k.ID, string(raw))
	return err
}

// dbDeleteApiKey removes one API key. Caller holds cfgLock.
func dbDeleteApiKey(id string) error {
	if db == nil {
		return errors.New("database not open")
	}
	_, err := db.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}

// migrateJSONFileToDB loads a legacy config.json into memory and writes SQLite.
// Caller must hold cfgLock. Leaves the original JSON in place (renamed to .migrated).
func migrateJSONFileToDB(jsonPath string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("parse legacy config: %w", err)
	}
	cfg = &c
	if err := saveToDB(); err != nil {
		return fmt.Errorf("write migrated db: %w", err)
	}
	// Preserve legacy file for rollback; do not leave it as the active store.
	bak := jsonPath + ".migrated." + time.Now().Format("20060102-150405")
	if renErr := os.Rename(jsonPath, bak); renErr != nil {
		logger.Warnf("[Store] Migrated to SQLite but could not rename %s: %v", jsonPath, renErr)
	} else {
		logger.Infof("[Store] Legacy config.json archived as %s", bak)
	}
	logger.Infof("[Store] Migrated %d accounts from JSON → SQLite (%s)", len(cfg.Accounts), dbPath)
	return nil
}

// closeDB closes the database handle (tests / shutdown).
func closeDB() {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if db != nil {
		// Best-effort WAL checkpoint so kiro.db is self-contained after stop.
		_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		_ = db.Close()
		db = nil
		dbPath = ""
	}
}



// requestLogMu serializes request-log writes without taking cfgLock on the hot path.
var requestLogMu sync.Mutex

// RequestLogEntry is a persisted admin request log row.
type RequestLogEntry struct {
	Time         int64   `json:"time"`
	Endpoint     string  `json:"endpoint"`
	Model        string  `json:"model"`
	AccountID    string  `json:"accountId"`
	AccountEmail string  `json:"accountEmail,omitempty"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
	ErrorType    string  `json:"errorType,omitempty"`
	Tokens       int     `json:"tokens"`
	Credits      float64 `json:"credits"`
	Duration     int64   `json:"duration"`
}

// InsertRequestLogs appends one or more request logs and prunes to keepMax newest rows.
// keepMax <= 0 skips pruning. Safe when DB is closed (no-op).
func InsertRequestLogs(entries []RequestLogEntry, keepMax int) error {
	if len(entries) == 0 {
		return nil
	}
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if db == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO request_logs(
		time, endpoint, model, account_id, account_email, status, error, error_type, tokens, credits, duration
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range entries {
		if _, err := stmt.Exec(
			e.Time, e.Endpoint, e.Model, e.AccountID, e.AccountEmail,
			e.Status, e.Error, e.ErrorType, e.Tokens, e.Credits, e.Duration,
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	if keepMax > 0 {
		// Faster than NOT IN (subquery): delete rows older than the keepMax-th newest id.
		var cutoff sql.NullInt64
		err := tx.QueryRow(`
			SELECT id FROM request_logs ORDER BY id DESC LIMIT 1 OFFSET ?`, keepMax).Scan(&cutoff)
		if err == nil && cutoff.Valid {
			if _, err := tx.Exec(`DELETE FROM request_logs WHERE id <= ?`, cutoff.Int64); err != nil {
				_ = tx.Rollback()
				return err
			}
		} else if err != nil && err != sql.ErrNoRows {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListRequestLogs returns newest-first request logs, up to limit.
func ListRequestLogs(limit int) ([]RequestLogEntry, error) {
	return ListRequestLogsPage(limit, 0, "", 0)
}

// ListRequestLogsPage returns newest-first logs with offset/status filter.
// errorMaxLen > 0 truncates Error for list payloads (full text remains in DB).
func ListRequestLogsPage(limit, offset int, status string, errorMaxLen int) ([]RequestLogEntry, error) {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	status = strings.TrimSpace(strings.ToLower(status))
	var (
		rows *sql.Rows
		err  error
	)
	if status == "success" || status == "error" {
		rows, err = db.Query(`
			SELECT time, endpoint, model, account_id, account_email, status, error, error_type, tokens, credits, duration
			FROM request_logs
			WHERE status = ?
			ORDER BY id DESC
			LIMIT ? OFFSET ?`, status, limit, offset)
	} else {
		rows, err = db.Query(`
			SELECT time, endpoint, model, account_id, account_email, status, error, error_type, tokens, credits, duration
			FROM request_logs
			ORDER BY id DESC
			LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RequestLogEntry, 0, limit)
	for rows.Next() {
		var e RequestLogEntry
		if err := rows.Scan(
			&e.Time, &e.Endpoint, &e.Model, &e.AccountID, &e.AccountEmail,
			&e.Status, &e.Error, &e.ErrorType, &e.Tokens, &e.Credits, &e.Duration,
		); err != nil {
			return nil, err
		}
		if errorMaxLen > 0 && len(e.Error) > errorMaxLen {
			// Keep rune-safe-ish truncation for UI list; full error stays in DB.
			e.Error = e.Error[:errorMaxLen] + "…"
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountRequestLogs returns total rows, optionally filtered by status (success/error).
func CountRequestLogs(status string) (total, success, errorCount int, err error) {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if db == nil {
		return 0, 0, 0, nil
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&total); err != nil {
		return 0, 0, 0, err
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE status = 'success'`).Scan(&success); err != nil {
		return 0, 0, 0, err
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE status = 'error'`).Scan(&errorCount); err != nil {
		return 0, 0, 0, err
	}
	_ = status
	return total, success, errorCount, nil
}

// ClearRequestLogs deletes all persisted request logs.
func ClearRequestLogs() error {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM request_logs`)
	return err
}

// CloseStore flushes SQLite and closes the handle. Safe to call multiple times.
func CloseStore() {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	closeDB()
}

// GetDatabasePath returns the active SQLite path (empty if not using DB).
func GetDatabasePath() string {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return dbPath
}
