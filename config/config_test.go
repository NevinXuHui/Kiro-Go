package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

	// Re-read the file and confirm legacy field is gone (so it doesn't drift
	// back in on later saves).
	on_disk, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var reloaded struct {
		Accounts []map[string]interface{} `json:"accounts"`
	}
	if err := json.Unmarshal(on_disk, &reloaded); err != nil {
		t.Fatalf("decode reload: %v", err)
	}
	for _, a := range reloaded.Accounts {
		if _, ok := a["allowOverage"]; ok {
			t.Fatalf("expected allowOverage to be omitted from persisted file, got %+v", a)
		}
	}
}

func TestGetMaxInFlightRequestsDefaultAndClamp(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 500 {
		t.Fatalf("default MaxInFlightRequests = %d, want 500", got)
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
	if err := UpdateMaxInFlightRequests(20000); err != nil {
		t.Fatalf("update 20000: %v", err)
	}
	if got := GetMaxInFlightRequests(); got != 10000 {
		t.Fatalf("clamp high got %d, want 10000", got)
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
