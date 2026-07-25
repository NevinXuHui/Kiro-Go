package proxy

import (
	"fmt"
	"kiro-go/config"
	accountpool "kiro-go/pool"
	"testing"
)

func TestAccountFailureClassifiers(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) bool
		msg  string
	}{
		{name: "quota", fn: isQuotaErrorMessage, msg: "HTTP 429: quota exhausted"},
		{name: "overage", fn: isOverageErrorMessage, msg: "HTTP 402 from Kiro IDE: OVERAGE limit exceeded"},
		{name: "suspension", fn: isSuspensionErrorMessage, msg: "Your User ID temporarily is suspended"},
		{name: "profile", fn: isProfileUnavailableErrorMessage, msg: "no available Kiro profile"},
		{name: "auth", fn: isAuthErrorMessage, msg: "Authentication failed - token invalid or expired"},
	}

	for _, tc := range tests {
		if !tc.fn(tc.msg) {
			t.Fatalf("%s classifier did not match %q", tc.name, tc.msg)
		}
	}
}

func TestQuotaClassifierIgnoresUUIDFragment429(t *testing.T) {
	// Real Kiro body embeds User ID e4980498-90b1-70de-4429-1c61bb57cdb5.
	// The "4429" substring must NOT be treated as HTTP 429.
	msg := `account=user@example.com id=acc-1 userId=e4980498-90b1-70de-4429-1c61bb57cdb5: HTTP 403 from Kiro IDE: {"message":"Your User ID (e4980498-90b1-70de-4429-1c61bb57cdb5) temporarily is suspended. We've locked your account as a security precaution. To restore access, please contact our support team to verify your identity: https://app.kiro.dev/account/usage?support_form","reason":null}`
	if isQuotaErrorMessage(msg) {
		t.Fatalf("UUID fragment 4429 must not classify as quota: %q", msg)
	}
	if !isSuspensionErrorMessage(msg) {
		t.Fatalf("expected suspension classifier to match: %q", msg)
	}
	if isAuthErrorMessage(msg) {
		t.Fatalf("suspension must not also classify as generic auth: %q", msg)
	}
	if classifyError(msg) != "suspended" {
		t.Fatalf("classifyError=%q want suspended", classifyError(msg))
	}
}

func TestHandleAccountFailureBansSuspendedAccount(t *testing.T) {
	cfgFile := t.TempDir() + "/config.json"
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(config.CloseStore)

	acc := config.Account{
		ID:          "acc-suspend-1",
		Email:       "suspend@example.com",
		UserId:      "e4980498-90b1-70de-4429-1c61bb57cdb5",
		AccessToken: "tok",
		Enabled:     true,
		AuthMethod:  "social",
		Region:      "us-east-1",
	}
	if err := config.AddAccount(acc); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	p := &accountpool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	err := fmt.Errorf(
		`account=%s id=%s userId=%s: HTTP 403 from Kiro IDE: {"message":"Your User ID (%s) temporarily is suspended. We've locked your account as a security precaution.","reason":null}`,
		acc.Email, acc.ID, acc.UserId, acc.UserId,
	)
	h.handleAccountFailure(&acc, err)

	var got config.Account
	found := false
	for _, a := range config.GetAccounts() {
		if a.ID == acc.ID {
			got = a
			found = true
			break
		}
	}
	if !found {
		t.Fatal("account missing after failure handling")
	}
	if got.Enabled {
		t.Fatalf("expected account disabled after suspension, got enabled=true banStatus=%q reason=%q", got.BanStatus, got.BanReason)
	}
	if got.BanStatus != "BANNED" {
		t.Fatalf("BanStatus=%q want BANNED", got.BanStatus)
	}
	if got.BanReason == "" {
		t.Fatal("expected BanReason to be set")
	}
}

func TestRecordFailureParsesAccountFromErrorWhenIDMissing(t *testing.T) {
	h := &Handler{}
	err := fmt.Errorf(`account=user@example.com id=acc-42: HTTP 400 from AmazonQ: {"message":"Input is too long.","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`)
	h.recordFailureWithDetails("claude", "claude-sonnet-4.5", "", err)
	logs := h.getRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("logs=%d want 1", len(logs))
	}
	if logs[0].AccountID != "acc-42" {
		t.Fatalf("AccountID=%q want acc-42", logs[0].AccountID)
	}
	if logs[0].AccountEmail != "user@example.com" {
		t.Fatalf("AccountEmail=%q want user@example.com", logs[0].AccountEmail)
	}
}
