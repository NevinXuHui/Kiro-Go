package pool

import (
	"math"
	"os"

	"errors"
	"kiro-go/config"
	"path/filepath"
	"testing"
	"time"
)

func TestOverLimitAccountsAreSkippedByDefault(t *testing.T) {
	p := &AccountPool{}
	normal := config.Account{ID: "normal"}
	overLimit := config.Account{ID: "over", UsageCurrent: 10, UsageLimit: 10}

	p.accounts = []config.Account{normal, overLimit}

	for i := 0; i < 5; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatalf("expected an account")
		}
		if acc.ID == "over" {
			t.Fatalf("expected over-limit account to be skipped when upstream OverageStatus is empty")
		}
	}
}

func TestOverLimitAccountsCanBeSelectedWhenUpstreamOverageEnabled(t *testing.T) {
	p := &AccountPool{}
	overLimit := config.Account{
		ID:            "over",
		UsageCurrent:  10,
		UsageLimit:    10,
		OverageStatus: "ENABLED",
	}

	p.accounts = []config.Account{overLimit}

	acc := p.GetNext()
	if acc == nil {
		t.Fatalf("expected upstream-enabled overage account to be selectable")
	}
	if acc.ID != "over" {
		t.Fatalf("expected overage account, got %q", acc.ID)
	}
}

func TestOverLimitAccountsRemainSkippedWhenUpstreamOverageDisabled(t *testing.T) {
	p := &AccountPool{}
	overLimit := config.Account{
		ID:            "over",
		UsageCurrent:  10,
		UsageLimit:    10,
		OverageStatus: "DISABLED",
	}

	p.accounts = []config.Account{overLimit}

	if acc := p.GetNext(); acc != nil {
		t.Fatalf("expected nil when upstream OverageStatus=DISABLED, got %q", acc.ID)
	}
}

func TestGetNextKeepsFiveMinuteTokenAvailable(t *testing.T) {
	p := &AccountPool{}
	account := config.Account{
		ID:          "acct-1",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().Unix() + 300,
	}

	p.accounts = []config.Account{account}

	got := p.GetNext()
	if got == nil {
		t.Fatalf("expected five-minute token to be available")
	}
	if got.ID != account.ID {
		t.Fatalf("expected account %q, got %q", account.ID, got.ID)
	}
}

// ---------------------------------------------------------------------------
// IsAuthFailure
// ---------------------------------------------------------------------------

func TestIsAuthFailureRecognizes401And403(t *testing.T) {
	positives := []string{
		"HTTP 401 from server",
		"received 403 Forbidden",
		"bad credentials",
		"invalid_grant",
		"invalid_token",
		"token expired",
		"token has expired",
		"unauthorized",
	}
	for _, msg := range positives {
		if !IsAuthFailure(errors.New(msg)) {
			t.Errorf("IsAuthFailure(%q) = false, want true", msg)
		}
	}
}

func TestIsAuthFailureIgnoresFalsePositives(t *testing.T) {
	// hasStatusToken only excludes digit boundaries; e.g. "4011" contains "401"
	// but the trailing '1' is a digit so it does NOT match.
	negatives := []string{
		"status code 4011 found", // digit immediately after 401 → not a standalone token
		"error 14013 exceeded",   // digit before and after 401
		"some random error",
		"status 200 OK",
	}
	for _, msg := range negatives {
		if IsAuthFailure(errors.New(msg)) {
			t.Errorf("IsAuthFailure(%q) = true, want false", msg)
		}
	}
}

func TestIsAuthFailureNilError(t *testing.T) {
	if IsAuthFailure(nil) {
		t.Fatal("IsAuthFailure(nil) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// IsSuspensionError
// ---------------------------------------------------------------------------

func TestIsSuspensionErrorDetectsKnownMessages(t *testing.T) {
	positives := []string{
		"account temporarily_suspended",
		"account temporarily suspended",
		"no available kiro profile",
		"No Available Kiro Profile", // case-insensitive
	}
	for _, msg := range positives {
		if !IsSuspensionError(errors.New(msg)) {
			t.Errorf("IsSuspensionError(%q) = false, want true", msg)
		}
	}
}

func TestIsSuspensionErrorIgnoresUnrelatedErrors(t *testing.T) {
	negatives := []string{
		"some other error",
		"unauthorized",
		"429 too many requests",
	}
	for _, msg := range negatives {
		if IsSuspensionError(errors.New(msg)) {
			t.Errorf("IsSuspensionError(%q) = true, want false", msg)
		}
	}
}

func TestIsSuspensionErrorNilError(t *testing.T) {
	if IsSuspensionError(nil) {
		t.Fatal("IsSuspensionError(nil) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// GetNextForModelExcluding
// ---------------------------------------------------------------------------

func newTestPool(accounts ...config.Account) *AccountPool {
	p := &AccountPool{
		cooldowns:   make(map[string]time.Time),
		errorCounts: make(map[string]int),
		modelLists:  make(map[string]map[string]bool),
	}
	p.accounts = accounts
	return p
}

func TestGetNextForModelExcludingSkipsExcludedAccounts(t *testing.T) {
	p := newTestPool(
		config.Account{ID: "a"},
		config.Account{ID: "b"},
	)
	excluded := map[string]bool{"a": true}
	for i := 0; i < 5; i++ {
		acc := p.GetNextForModelExcluding("model", excluded)
		if acc == nil {
			t.Fatal("expected account b, got nil")
		}
		if acc.ID == "a" {
			t.Fatalf("excluded account a was returned on iteration %d", i)
		}
	}
}

func TestGetNextForModelExcludingReturnsNilWhenAllExcluded(t *testing.T) {
	p := newTestPool(config.Account{ID: "only"})
	acc := p.GetNextForModelExcluding("model", map[string]bool{"only": true})
	if acc != nil {
		t.Fatalf("expected nil when only account is excluded, got %q", acc.ID)
	}
}

func TestGetNextForModelExcludingReturnsNilOnEmptyPool(t *testing.T) {
	p := newTestPool()
	acc := p.GetNextForModelExcluding("model", map[string]bool{})
	if acc != nil {
		t.Fatalf("expected nil for empty pool, got %q", acc.ID)
	}
}

// ---------------------------------------------------------------------------
// DisableAccount
// ---------------------------------------------------------------------------

func TestDisableAccountSetsCooldown(t *testing.T) {
	// Initialize a temporary config so SetAccountBanStatus can persist safely.
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("config.Init: %v", err)
	}

	p := newTestPool()
	p.DisableAccount("test-id", "test reason")

	p.mu.RLock()
	cooldown, ok := p.cooldowns["test-id"]
	p.mu.RUnlock()

	if !ok {
		t.Fatal("expected cooldown to be set after DisableAccount")
	}
	// Safety-net cooldown must be at least 23 hours from now.
	minExpected := time.Now().Add(23 * time.Hour)
	if cooldown.Before(minExpected) {
		t.Fatalf("expected cooldown >= 23h in future, got %v", cooldown)
	}
}

func TestGetNextExcludingSkipsExcludedAccount(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "a", Enabled: true},
			{ID: "b", Enabled: true},
		},
		cooldowns:    make(map[string]time.Time),
		errorCounts:  make(map[string]int),
		modelLists:   make(map[string]map[string]bool),
		currentIndex: ^uint64(0),
	}

	acc := p.GetNextExcluding(map[string]bool{"a": true})
	if acc == nil || acc.ID != "b" {
		t.Fatalf("expected account b, got %#v", acc)
	}
}

func TestGetNextForModelExcludingSkipsExcludedAccount(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "a", Enabled: true},
			{ID: "b", Enabled: true},
		},
		cooldowns:    make(map[string]time.Time),
		errorCounts:  make(map[string]int),
		modelLists:   make(map[string]map[string]bool),
		currentIndex: ^uint64(0),
	}
	p.SetModelList("a", []string{"claude-sonnet-4.5"})
	p.SetModelList("b", []string{"claude-sonnet-4.5"})

	acc := p.GetNextForModelExcluding("claude-sonnet-4.5", map[string]bool{"a": true})
	if acc == nil || acc.ID != "b" {
		t.Fatalf("expected account b, got %#v", acc)
	}
}

// ---------------------------------------------------------------------------
// Reload over-usage filtering
// ---------------------------------------------------------------------------

func TestReloadKeepsOverQuotaAccountWhenAllowOverUsage(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("config.Init: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:           "over",
		Enabled:      true,
		UsageCurrent: 10,
		UsageLimit:   10,
	}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if err := config.UpdateAllowOverUsage(true); err != nil {
		t.Fatalf("UpdateAllowOverUsage: %v", err)
	}

	p := newTestPool()
	p.Reload()

	if got := p.GetNext(); got == nil || got.ID != "over" {
		t.Fatalf("expected over-quota account to remain routable when allowOverUsage=true, got %#v", got)
	}
}

func TestReloadDropsOverQuotaAccountWhenAllowOverUsageDisabled(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("config.Init: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:           "over",
		Enabled:      true,
		UsageCurrent: 10,
		UsageLimit:   10,
	}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	p := newTestPool()
	p.Reload()

	if got := p.GetNext(); got != nil {
		t.Fatalf("expected over-quota account to be dropped, got %q", got.ID)
	}
}

func TestUpdateStatsMemoryOnlyNoImmediatePersist(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("config.Init: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "acc-stats", Enabled: true, AccessToken: "tok"}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	p := &AccountPool{
		accounts: []config.Account{
			{ID: "acc-stats", Enabled: true, AccessToken: "tok"},
			// weighted duplicate copy
			{ID: "acc-stats", Enabled: true, AccessToken: "tok"},
		},
		cooldowns:   make(map[string]time.Time),
		errorCounts: make(map[string]int),
		modelLists:  make(map[string]map[string]bool),
	}

	dbFile := config.GetDatabasePath()
	if dbFile == "" {
		t.Fatal("expected sqlite database path")
	}
	before, err := os.Stat(dbFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	p.UpdateStats("acc-stats", 15, 0.5)
	p.UpdateStats("acc-stats", 5, 0.25)

	// Both copies in weighted list should stay in sync.
	for i, a := range p.accounts {
		if a.RequestCount != 2 || a.TotalTokens != 20 {
			t.Fatalf("account[%d] stats mismatch: %+v", i, a)
		}
	}

	after, err := os.Stat(dbFile)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if after.ModTime().After(before.ModTime()) {
		t.Fatalf("UpdateStats should not persist config immediately")
	}
}

func TestTakeDirtyAccountStatsOnlyReturnsChanged(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "d1", Enabled: true, AccessToken: "t"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "d2", Enabled: true, AccessToken: "t"}); err != nil {
		t.Fatalf("add2: %v", err)
	}
	p := GetPool()
	p.Reload()
	// no dirty yet
	if got := p.TakeDirtyAccountStats(); len(got) != 0 {
		t.Fatalf("expected empty dirty, got %d", len(got))
	}
	p.UpdateStats("d1", 10, 0.1)
	got := p.TakeDirtyAccountStats()
	if len(got) != 1 || got[0].ID != "d1" || got[0].TotalTokens != 10 {
		t.Fatalf("dirty snapshot: %+v", got)
	}
	// second take should be empty
	if got := p.TakeDirtyAccountStats(); len(got) != 0 {
		t.Fatalf("expected cleared dirty, got %+v", got)
	}
}


func TestNewAccountFirstUseIntervalSerializesVirgins(t *testing.T) {
	p := &AccountPool{
		firstUseStarted: make(map[string]struct{}),
		inFlight:        make(map[string]int),
	}
	p.accounts = []config.Account{
		{ID: "new-a", AccessToken: "t", Enabled: true, UsageCurrent: 0, UsageLimit: 50},
		{ID: "new-b", AccessToken: "t", Enabled: true, UsageCurrent: 0, UsageLimit: 50},
	}

	first := p.GetNext()
	if first == nil {
		t.Fatal("expected first virgin account")
	}
	p.ReleaseInFlight(first.ID)

	// Within interval: claimed virgin is no longer "virgin", so it is selected as experienced.
	// The other virgin must wait.
	ids := map[string]int{}
	for i := 0; i < 20; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatalf("iteration %d: expected claimed account still selectable", i)
		}
		if acc.ID != first.ID {
			t.Fatalf("iteration %d: got second virgin %q too early; want only %q", i, acc.ID, first.ID)
		}
		ids[acc.ID]++
		p.ReleaseInFlight(acc.ID)
	}
	if len(ids) != 1 {
		t.Fatalf("expected single active first-use account, got %v", ids)
	}

	p.mu.Lock()
	p.nextNewFirstUseAt = time.Now().Add(-time.Second)
	p.mu.Unlock()

	second := p.GetNextExcluding(map[string]bool{first.ID: true})
	if second == nil {
		t.Fatal("expected second virgin after interval")
	}
	if second.ID == first.ID {
		t.Fatalf("expected the other virgin, got %q again", second.ID)
	}
}

func TestNewAccountFirstUseIntroducesVirginOnIntervalEvenIfExperiencedExists(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	// Interval open + equal remaining: must still introduce the virgin (spaced first-use),
	// not sticky-pick experienced forever.
	p.accounts = []config.Account{
		{ID: "used", AccessToken: "t", RequestCount: 5, LastUsed: time.Now().Unix(), UsageCurrent: 0, UsageLimit: 50},
		{ID: "new", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
	}
	acc := p.GetNext()
	if acc == nil || acc.ID != "new" {
		t.Fatalf("expected virgin introduced when interval open, got %+v", acc)
	}
	p.ReleaseInFlight(acc.ID)

	// Within interval, only experienced (including just-claimed) may be used.
	for i := 0; i < 5; i++ {
		again := p.GetNext()
		if again == nil {
			t.Fatal("expected experienced account within interval")
		}
		// "new" is now claimed so it is eligible as experienced; "used" also eligible.
		if again.ID != "new" && again.ID != "used" {
			t.Fatalf("unexpected account %q", again.ID)
		}
		p.ReleaseInFlight(again.ID)
	}
}

func TestNewAccountFirstUseAllowsOnlyVirginWhenNoExperienced(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	p.accounts = []config.Account{
		{ID: "only-new", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
	}
	acc := p.GetNext()
	if acc == nil || acc.ID != "only-new" {
		t.Fatalf("expected only virgin to be claimable, got %+v", acc)
	}
	p.ReleaseInFlight(acc.ID)
	again := p.GetNext()
	if again == nil || again.ID != "only-new" {
		t.Fatalf("expected claimed virgin reusable, got %+v", again)
	}
}

func TestSelectPrefersHigherRemainingQuota(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	p.accounts = []config.Account{
		{ID: "low", AccessToken: "t", RequestCount: 3, LastUsed: 1, UsageCurrent: 90, UsageLimit: 100},
		{ID: "high", AccessToken: "t", RequestCount: 3, LastUsed: 1, UsageCurrent: 10, UsageLimit: 100},
	}
	for i := 0; i < 15; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatal("expected account")
		}
		if acc.ID != "high" {
			t.Fatalf("iter %d: expected higher remaining quota account, got %q", i, acc.ID)
		}
		p.ReleaseInFlight(acc.ID)
	}
}

func TestSelectHoldsVirginWhileFirstUseIntervalBlocks(t *testing.T) {
	p := &AccountPool{
		firstUseStarted:   make(map[string]struct{}),
		inFlight:          make(map[string]int),
		nextNewFirstUseAt: time.Now().Add(time.Hour),
	}
	p.accounts = []config.Account{
		{ID: "used-low", AccessToken: "t", RequestCount: 2, LastUsed: 1, UsageCurrent: 40, UsageLimit: 50},
		{ID: "new-full", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
	}
	for i := 0; i < 5; i++ {
		acc := p.GetNext()
		if acc == nil || acc.ID != "used-low" {
			t.Fatalf("iter %d: expected used account while virgin first-use gated, got %+v", i, acc)
		}
		p.ReleaseInFlight(acc.ID)
	}
}

func TestSelectNeverForceVirginPastInterval(t *testing.T) {
	p := &AccountPool{
		firstUseStarted:   make(map[string]struct{}),
		inFlight:          make(map[string]int),
		nextNewFirstUseAt: time.Now().Add(time.Hour),
	}
	p.accounts = []config.Account{
		{ID: "new-a", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
		{ID: "new-b", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
	}
	if acc := p.GetNext(); acc != nil {
		t.Fatalf("expected nil while first-use interval blocks all virgins, got %+v", acc)
	}
}

func TestSelectSkipsExhaustedFiftyFiftyEvenIfNeverRequested(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	p.accounts = []config.Account{
		{ID: "exhausted", AccessToken: "t", UsageCurrent: 50, UsageLimit: 50},
		{ID: "used-ok", AccessToken: "t", RequestCount: 1, LastUsed: 1, UsageCurrent: 10, UsageLimit: 50},
	}
	acc := p.GetNext()
	if acc == nil || acc.ID != "used-ok" {
		t.Fatalf("expected usable used account, got %+v", acc)
	}
}

func TestSelectIntroducesBestVirginByRemainingOnInterval(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	p.accounts = []config.Account{
		{ID: "used", AccessToken: "t", RequestCount: 2, LastUsed: 1, UsageCurrent: 5, UsageLimit: 50},
		{ID: "new-low", AccessToken: "t", UsageCurrent: 20, UsageLimit: 50},
		{ID: "new-high", AccessToken: "t", UsageCurrent: 0, UsageLimit: 50},
	}
	acc := p.GetNext()
	if acc == nil || acc.ID != "new-high" {
		t.Fatalf("expected highest-remaining virgin on interval open, got %+v", acc)
	}
}

func TestRemainingQuotaHelper(t *testing.T) {
	if remainingQuota(config.Account{UsageLimit: 100, UsageCurrent: 40}) != 60 {
		t.Fatalf("expected 60")
	}
	if remainingQuota(config.Account{UsageLimit: 0, UsageCurrent: 0}) != math.MaxFloat64 {
		t.Fatalf("unknown limit should rank as plenty")
	}
	if remainingQuota(config.Account{UsageLimit: 10, UsageCurrent: 15}) != 0 {
		t.Fatalf("over-limit remaining should be 0")
	}
	if remainingQuota(config.Account{UsageLimit: 50, UsageCurrent: 50}) != 0 {
		t.Fatalf("50/50 exhausted remaining should be 0")
	}
	if remainingQuota(config.Account{UsageLimit: 50, UsageCurrent: 0}) != 50 {
		t.Fatalf("full new-account remaining should be 50")
	}
}

func TestSelectSpreadsEqualRemainingAcrossInFlight(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	// Same remaining quota: concurrent picks must not all sticky-bind one ID.
	p.accounts = []config.Account{
		{ID: "a", AccessToken: "t", RequestCount: 5, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
		{ID: "b", AccessToken: "t", RequestCount: 5, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
		{ID: "c", AccessToken: "t", RequestCount: 5, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
	}
	seen := map[string]int{}
	held := make([]*config.Account, 0, 6)
	for i := 0; i < 6; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatal("expected account")
		}
		seen[acc.ID]++
		held = append(held, acc)
	}
	if len(seen) < 3 {
		t.Fatalf("expected concurrent equal-remaining picks to spread across accounts, got %v", seen)
	}
	for _, acc := range held {
		p.ReleaseInFlight(acc.ID)
	}
}

func TestSelectRotatesEqualRemainingSerially(t *testing.T) {
	p := &AccountPool{firstUseStarted: make(map[string]struct{}), inFlight: make(map[string]int)}
	p.accounts = []config.Account{
		{ID: "a", AccessToken: "t", RequestCount: 1, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
		{ID: "b", AccessToken: "t", RequestCount: 1, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
		{ID: "c", AccessToken: "t", RequestCount: 1, LastUsed: 1, UsageCurrent: 4, UsageLimit: 50},
	}
	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatal("expected account")
		}
		seen[acc.ID]++
		p.RecordSuccess(acc.ID)
	}
	if len(seen) < 3 {
		t.Fatalf("expected serial equal-remaining picks to rotate, got %v", seen)
	}
}


// 429 cool-down must keep the account out of selection until the window ends.
// The old fallback re-picked earliest-cooldown accounts immediately, defeating
// RecordError(..., true).
func TestSelectSkipsActiveQuotaCooldown(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "a", Email: "a@x", Enabled: true, Weight: 1, RequestCount: 1, LastUsed: 1, UsageLimit: 50, UsageCurrent: 1},
			{ID: "b", Email: "b@x", Enabled: true, Weight: 1, RequestCount: 1, LastUsed: 1, UsageLimit: 50, UsageCurrent: 1},
		},
		cooldowns:   make(map[string]time.Time),
		errorCounts: make(map[string]int),
		inFlight:    make(map[string]int),
		modelLists:  make(map[string]map[string]bool),
	}
	p.RecordError("a", true) // 1h quota cooldown

	// With only a cooling and b free, always get b.
	for i := 0; i < 5; i++ {
		got := p.GetNext()
		if got == nil {
			t.Fatalf("iter %d: expected account b, got nil", i)
		}
		if got.ID != "b" {
			t.Fatalf("iter %d: got %s, want b (a is in quota cooldown)", i, got.ID)
		}
		p.ReleaseInFlight(got.ID)
	}

	// Cool both → nothing available (must NOT force a cooled account).
	p.RecordError("b", true)
	if got := p.GetNext(); got != nil {
		t.Fatalf("expected nil when every account is cooling, got %s", got.ID)
	}
}
