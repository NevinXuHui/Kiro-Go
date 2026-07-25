package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateSettingsPatchPreservesOmittedAPIKeyFields(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := UpdateSettings("proxy-api-key", true, "admin-password"); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if err := UpdateSettingsPatch(nil, nil, "new-admin-password"); err != nil {
		t.Fatalf("patch settings: %v", err)
	}

	if got := GetApiKey(); got != "proxy-api-key" {
		t.Fatalf("expected API key to be preserved, got %q", got)
	}
	if !IsApiKeyRequired() {
		t.Fatalf("expected requireApiKey to stay enabled")
	}
	if got := GetPassword(); got != "new-admin-password" {
		t.Fatalf("expected password to update, got %q", got)
	}
}

func TestUpdateSettingsPatchCanExplicitlyDisableAPIKey(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := UpdateSettings("proxy-api-key", true, "admin-password"); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	emptyKey := ""
	requireAPIKey := false
	if err := UpdateSettingsPatch(&emptyKey, &requireAPIKey, ""); err != nil {
		t.Fatalf("patch settings: %v", err)
	}

	if got := GetApiKey(); got != "" {
		t.Fatalf("expected API key to be cleared, got %q", got)
	}
	if IsApiKeyRequired() {
		t.Fatalf("expected requireApiKey to be disabled")
	}
	if got := GetPassword(); got != "admin-password" {
		t.Fatalf("expected password to be preserved, got %q", got)
	}
}

// TestAccountAllowOverageMigration verifies that a config.json from before the
// upstream-Overages-switch refactor (which carried `allowOverage: true` per
// account) is migrated into OverageStatus="ENABLED" on first load, and that
// the legacy field is cleared so future saves don't re-emit it.
func TestAccountAllowOverageMigration(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	seed := map[string]interface{}{
		"password":      "p",
		"port":          8080,
		"host":          "0.0.0.0",
		"requireApiKey": false,
		"accounts": []map[string]interface{}{
			{"id": "acc-allow", "enabled": true, "allowOverage": true},
			{"id": "acc-deny", "enabled": true, "allowOverage": false},
			{"id": "acc-already-set", "enabled": true, "allowOverage": true, "overageStatus": "DISABLED"},
		},
	}
	raw, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(cfgFile, raw, 0600); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}

	accounts := GetAccounts()
	byID := map[string]Account{}
	for _, a := range accounts {
		byID[a.ID] = a
	}

	if got := byID["acc-allow"].OverageStatus; got != "ENABLED" {
		t.Fatalf("expected acc-allow to migrate to OverageStatus=ENABLED, got %q", got)
	}
	if byID["acc-allow"].LegacyAllowOverage {
		t.Fatalf("expected legacy allowOverage to be cleared after migration")
	}
	if got := byID["acc-deny"].OverageStatus; got != "" {
		t.Fatalf("expected acc-deny to keep empty OverageStatus, got %q", got)
	}
	// Pre-set OverageStatus must win over the legacy field.
	if got := byID["acc-already-set"].OverageStatus; got != "DISABLED" {
		t.Fatalf("expected acc-already-set OverageStatus to be preserved, got %q", got)
	}
	if byID["acc-already-set"].LegacyAllowOverage {
		t.Fatalf("expected legacy field to still be cleared on acc-already-set")
	}

	// Re-load from SQLite and confirm legacy field is gone (so it doesn't drift
	// back in on later saves).
	if err := Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, a := range GetAccounts() {
		if a.LegacyAllowOverage {
			t.Fatalf("expected allowOverage cleared after persist, got %+v", a)
		}
	}
}

func TestGetMaxInFlightRequestsDefaultAndClamp(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 4500 {
		t.Fatalf("default MaxInFlightRequests = %d, want 4500", got)
	}
	if err := UpdateMaxInFlightRequests(400); err != nil {
		t.Fatalf("update 400: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 400 {
		t.Fatalf("got %d, want 400", got)
	}
	if err := UpdateMaxInFlightRequests(0); err != nil {
		t.Fatalf("update 0: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 1 {
		t.Fatalf("clamp low got %d, want 1", got)
	}
	if err := UpdateMaxInFlightRequests(50000); err != nil {
		t.Fatalf("update 50000: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 20000 {
		t.Fatalf("clamp high got %d, want 20000", got)
	}
}

func TestUpdateRuntimeStatsSnapshotPreservesNonStatsFields(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := AddAccount(Account{
		ID:           "acc-1",
		Enabled:      true,
		AccessToken:  "secret-token",
		ProxyURL:     "socks5://127.0.0.1:1080",
		RequestCount: 1,
		TotalTokens:  10,
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	today := "2026-07-23"
	err := UpdateRuntimeStatsSnapshot(
		100, 90, 10, 1000, 12.5,
		20, 18, 2, 200, 3.5, today,
		[]AccountRuntimeStats{{
			ID:            "acc-1",
			RequestCount:  5,
			ErrorCount:    1,
			LastUsed:      123456,
			TotalTokens:   99,
			TotalCredits:  1.25,
			DailyRequests: 2,
			DailyTokens:   30,
			DailyCredits:  0.5,
			DailyDate:     today,
		}},
	)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	accounts := GetAccounts()
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	a := accounts[0]
	if a.AccessToken != "secret-token" {
		t.Fatalf("AccessToken overwritten: %q", a.AccessToken)
	}
	if a.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("ProxyURL overwritten: %q", a.ProxyURL)
	}
	if !a.Enabled {
		t.Fatalf("Enabled overwritten")
	}
	if a.RequestCount != 5 || a.TotalTokens != 99 || a.ErrorCount != 1 {
		t.Fatalf("stats not applied: %+v", a)
	}
	totalReq, successReq, failedReq, totalTokens, totalCredits := GetStats()
	if totalReq != 100 || successReq != 90 || failedReq != 10 || totalTokens != 1000 || totalCredits != 12.5 {
		t.Fatalf("global stats mismatch: %d %d %d %d %v", totalReq, successReq, failedReq, totalTokens, totalCredits)
	}
	dReq, dSucc, dFail, dTok, dCred, dDate := GetDailyStats()
	if dReq != 20 || dSucc != 18 || dFail != 2 || dTok != 200 || dCred != 3.5 || dDate != today {
		t.Fatalf("daily stats mismatch: %d %d %d %d %v %s", dReq, dSucc, dFail, dTok, dCred, dDate)
	}
}

func TestDailyStatsHistoryArchiveOnDayChange(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seed "yesterday" as the live daily counters.
	yesterday := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	cfgLock.Lock()
	cfg.DailyDate = yesterday
	cfg.DailyRequests = 100
	cfg.DailySuccessRequests = 80
	cfg.DailyFailedRequests = 20
	cfg.DailyTokens = 1000
	cfg.DailyCredits = 5.5
	cfgLock.Unlock()

	// Writing today's stats must archive yesterday instead of discarding it.
	if err := UpdateDailyStats(3, 2, 1, 30, 0.5); err != nil {
		t.Fatalf("UpdateDailyStats: %v", err)
	}

	hist := GetDailyStatsHistory()
	if len(hist) != 1 {
		t.Fatalf("expected 1 history entry, got %d: %+v", len(hist), hist)
	}
	e := hist[0]
	if e.Date != yesterday {
		t.Fatalf("date=%q want %q", e.Date, yesterday)
	}
	if e.Requests != 100 || e.SuccessRequests != 80 || e.FailedRequests != 20 || e.Tokens != 1000 || e.Credits != 5.5 {
		t.Fatalf("archived entry mismatch: %+v", e)
	}

	dReq, dSucc, dFail, dTok, dCred, dDate := GetDailyStats()
	today := time.Now().Format("2006-01-02")
	if dDate != today || dReq != 3 || dSucc != 2 || dFail != 1 || dTok != 30 || dCred != 0.5 {
		t.Fatalf("live daily mismatch: %d %d %d %d %v %s", dReq, dSucc, dFail, dTok, dCred, dDate)
	}

	// Lifetime totals are independent and must not be touched by daily archive.
	if err := UpdateStats(50, 40, 10, 500, 9); err != nil {
		t.Fatalf("UpdateStats: %v", err)
	}
	tr, sr, fr, tt, tc := GetStats()
	if tr != 50 || sr != 40 || fr != 10 || tt != 500 || tc != 9 {
		t.Fatalf("lifetime stats changed: %d %d %d %d %v", tr, sr, fr, tt, tc)
	}
}

func TestArchiveDailyStatsUpsertsMax(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}

	date := "2026-07-20"
	if err := ArchiveDailyStats(date, 10, 7, 3, 100, 1.0); err != nil {
		t.Fatalf("archive1: %v", err)
	}
	// Smaller second write must not shrink the archived totals.
	if err := ArchiveDailyStats(date, 5, 1, 1, 10, 0.1); err != nil {
		t.Fatalf("archive2: %v", err)
	}
	hist := GetDailyStatsHistory()
	if len(hist) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(hist))
	}
	if hist[0].Requests != 10 || hist[0].SuccessRequests != 7 || hist[0].FailedRequests != 3 {
		t.Fatalf("max upsert failed: %+v", hist[0])
	}
	// Larger write expands.
	if err := ArchiveDailyStats(date, 12, 8, 4, 120, 1.2); err != nil {
		t.Fatalf("archive3: %v", err)
	}
	hist = GetDailyStatsHistory()
	if hist[0].Requests != 12 || hist[0].Tokens != 120 {
		t.Fatalf("expand failed: %+v", hist[0])
	}
}


func TestResetAllStatsClearsLifetimeAndHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(CloseStore)

	if err := AddAccount(Account{
		ID:            "acc-1",
		Email:         "a@example.com",
		AccessToken:   "tok",
		Enabled:       true,
		RequestCount:  9,
		ErrorCount:    2,
		TotalTokens:   100,
		TotalCredits:  1.5,
		DailyRequests: 3,
		DailyTokens:   30,
		DailyCredits:  0.3,
		DailyDate:     "2026-07-24",
	}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if err := UpdateStats(50, 40, 10, 500, 12.5); err != nil {
		t.Fatalf("UpdateStats: %v", err)
	}
	if err := ArchiveDailyStats("2026-07-23", 20, 15, 5, 200, 3.0); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := UpdateDailyStats(7, 5, 2, 70, 0.7); err != nil {
		t.Fatalf("UpdateDailyStats: %v", err)
	}

	if err := ResetAllStats(); err != nil {
		t.Fatalf("ResetAllStats: %v", err)
	}

	totReq, totOK, totFail, totTok, totCred := GetStats()
	if totReq != 0 || totOK != 0 || totFail != 0 || totTok != 0 || totCred != 0 {
		t.Fatalf("lifetime not cleared: %d %d %d %d %v", totReq, totOK, totFail, totTok, totCred)
	}
	dReq, dOK, dFail, dTok, dCred, _ := GetDailyStats()
	if dReq != 0 || dOK != 0 || dFail != 0 || dTok != 0 || dCred != 0 {
		t.Fatalf("daily not cleared: %d %d %d %d %v", dReq, dOK, dFail, dTok, dCred)
	}
	if hist := GetDailyStatsHistory(); len(hist) != 0 {
		t.Fatalf("history len=%d want 0", len(hist))
	}
	accs := GetAccounts()
	if len(accs) != 1 {
		t.Fatalf("accounts=%d", len(accs))
	}
	a := accs[0]
	if a.RequestCount != 0 || a.ErrorCount != 0 || a.TotalTokens != 0 || a.TotalCredits != 0 || a.DailyRequests != 0 || a.DailyTokens != 0 || a.DailyCredits != 0 {
		t.Fatalf("account runtime stats not cleared: %+v", a)
	}
}
