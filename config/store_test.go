package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteInitMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	// Write a minimal legacy JSON with one account + stats.
	legacy := `{
  "password": "secret",
  "port": 8991,
  "host": "0.0.0.0",
  "requireApiKey": false,
  "totalRequests": 10,
  "successRequests": 8,
  "failedRequests": 2,
  "totalTokens": 100,
  "totalCredits": 1.5,
  "dailyRequests": 3,
  "dailySuccessRequests": 2,
  "dailyFailedRequests": 1,
  "dailyTokens": 30,
  "dailyCredits": 0.3,
  "dailyDate": "2026-07-23",
  "accounts": [
    {
      "id": "acc-1",
      "email": "a@example.com",
      "accessToken": "tok",
      "refreshToken": "ref",
      "authMethod": "social",
      "region": "us-east-1",
      "enabled": true
    }
  ]
}`
	if err := os.WriteFile(jsonPath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Init(jsonPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer closeDB()

	if GetDatabasePath() == "" {
		t.Fatal("expected database path")
	}
	if _, err := os.Stat(GetDatabasePath()); err != nil {
		t.Fatalf("db file missing: %v", err)
	}
	// Legacy JSON should be renamed away.
	if _, err := os.Stat(jsonPath); err == nil {
		t.Fatal("expected config.json to be archived after migration")
	}

	accounts := GetAccounts()
	if len(accounts) != 1 || accounts[0].Email != "a@example.com" {
		t.Fatalf("accounts after migrate: %+v", accounts)
	}
	tr, sr, fr, tt, tc := GetStats()
	if tr != 10 || sr != 8 || fr != 2 || tt != 100 || tc != 1.5 {
		t.Fatalf("stats mismatch: %d %d %d %d %v", tr, sr, fr, tt, tc)
	}

	// Reload from DB only (no JSON).
	closeDB()
	cfg = nil
	if err := Init(jsonPath); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	defer closeDB()
	accounts = GetAccounts()
	if len(accounts) != 1 {
		t.Fatalf("reload accounts: %d", len(accounts))
	}
}

func TestSQLiteAccountCRUDAndStats(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	if err := Init(jsonPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer closeDB()

	if err := AddAccount(Account{
		ID:          "a1",
		Email:       "u@test.com",
		AccessToken: "t",
		Enabled:     true,
		Region:      "us-east-1",
		AuthMethod:  "social",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := UpdateAccountToken("a1", "t2", "r2", 123); err != nil {
		t.Fatalf("token: %v", err)
	}
	if err := UpdateStats(5, 4, 1, 50, 0.5); err != nil {
		t.Fatalf("stats: %v", err)
	}
	if err := ArchiveDailyStats("2026-07-20", 9, 7, 2, 90, 1.1); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Reopen and verify row-level persistence.
	closeDB()
	cfg = nil
	if err := Init(jsonPath); err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer closeDB()

	accs := GetAccounts()
	if len(accs) != 1 || accs[0].AccessToken != "t2" || accs[0].RefreshToken != "r2" {
		t.Fatalf("account not persisted: %+v", accs)
	}
	tr, sr, fr, _, _ := GetStats()
	if tr != 5 || sr != 4 || fr != 1 {
		t.Fatalf("stats not persisted: %d %d %d", tr, sr, fr)
	}
	hist := GetDailyStatsHistory()
	if len(hist) != 1 || hist[0].Requests != 9 {
		t.Fatalf("history: %+v", hist)
	}

	n, err := DeleteAccounts([]string{"a1"})
	if err != nil || n != 1 {
		t.Fatalf("delete: n=%d err=%v", n, err)
	}
	closeDB()
	cfg = nil
	if err := Init(jsonPath); err != nil {
		t.Fatalf("reload2: %v", err)
	}
	defer closeDB()
	if len(GetAccounts()) != 0 {
		t.Fatalf("expected empty accounts after delete")
	}
}


func TestRequestLogsPersistAndPrune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(CloseStore)

	entries := []RequestLogEntry{
		{Time: 1, Endpoint: "claude", Model: "m1", AccountEmail: "a@x.com", Status: "error", Error: "e1"},
		{Time: 2, Endpoint: "claude", Model: "m2", AccountEmail: "b@x.com", Status: "success", Tokens: 10},
		{Time: 3, Endpoint: "openai", Model: "m3", AccountEmail: "c@x.com", Status: "error", Error: "e3"},
	}
	if err := InsertRequestLogs(entries, 2); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := ListRequestLogs(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (pruned)", len(got))
	}
	// newest first
	if got[0].Model != "m3" || got[1].Model != "m2" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if err := ClearRequestLogs(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = ListRequestLogs(10)
	if err != nil {
		t.Fatalf("List2: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after clear len=%d", len(got))
	}
}
