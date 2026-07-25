// Package pool 账号池管理
// 实现轮询负载均衡、错误冷却、Token 刷新
package pool

import (
	"kiro-go/config"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const tokenRefreshSkewSeconds int64 = 120

// defaultNewAccountFirstUseInterval is used only if config is unavailable.
const defaultNewAccountFirstUseInterval = time.Minute

// AccountPool 账号池
type AccountPool struct {
	mu            sync.RWMutex
	accounts      []config.Account
	totalAccounts int
	currentIndex  uint64
	cooldowns     map[string]time.Time       // 账号冷却时间
	errorCounts   map[string]int             // 连续错误计数
	modelLists    map[string]map[string]bool // accountID → set of modelIDs (from ListAvailableModels)
	dirty         map[string]struct{}        // accountIDs with stats/token dirty for deferred persist

	// firstUseStarted marks accounts that already claimed their "first use" slot
	// this process lifetime (survives Reload until stats show real usage).
	firstUseStarted map[string]struct{}
	// nextNewFirstUseAt is the earliest time another virgin account may be claimed.
	nextNewFirstUseAt time.Time
}

var (
	pool     *AccountPool
	poolOnce sync.Once
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:       make(map[string]time.Time),
			errorCounts:     make(map[string]int),
			modelLists:      make(map[string]map[string]bool),
			firstUseStarted: make(map[string]struct{}),
		}
		pool.Reload()
	})
	return pool
}

// Reload rebuilds the weighted account list from config.
// Weight <= 1 → 1 entry; weight >= 2 → weight entries.
// Over-quota accounts are dropped unless either the per-account upstream
// Overages switch (OverageStatus=ENABLED) or the global AllowOverUsage
// setting permits over-quota routing.
func (p *AccountPool) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	enabled := config.GetEnabledAccounts()
	allowOverUsage := config.GetAllowOverUsage()
	var weighted []config.Account
	for _, a := range enabled {
		if isQuotaBlocked(a, allowOverUsage) {
			continue
		}
		w := effectiveWeight(a.Weight)
		for j := 0; j < w; j++ {
			weighted = append(weighted, a)
		}
	}
	p.accounts = weighted
	p.totalAccounts = len(enabled)
}

// accountCopy returns a heap copy so callers can mutate freely without racing Reload.
func accountCopy(a *config.Account) *config.Account {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// isVirginAccount reports whether the account has never completed a generation
// request and has not yet claimed a first-use slot in this process.
// Caller must hold p.mu (read or write).
func (p *AccountPool) isVirginAccount(acc *config.Account) bool {
	if acc == nil {
		return false
	}
	if acc.RequestCount > 0 || acc.LastUsed > 0 {
		return false
	}
	if p.firstUseStarted != nil {
		if _, ok := p.firstUseStarted[acc.ID]; ok {
			return false
		}
	}
	return true
}

// claimVirginFirstUseLocked reserves the global first-use slot for acc.
// Caller must hold p.mu for writing. Returns false if another virgin was
// claimed too recently.
func (p *AccountPool) claimVirginFirstUseLocked(acc *config.Account, now time.Time) bool {
	if acc == nil {
		return false
	}
	if !p.isVirginAccount(acc) {
		return true
	}
	if !p.nextNewFirstUseAt.IsZero() && now.Before(p.nextNewFirstUseAt) {
		return false
	}
	if p.firstUseStarted == nil {
		p.firstUseStarted = make(map[string]struct{})
	}
	p.firstUseStarted[acc.ID] = struct{}{}
	interval := config.GetNewAccountFirstUseInterval()
	if interval <= 0 {
		interval = defaultNewAccountFirstUseInterval
	}
	p.nextNewFirstUseAt = now.Add(interval)
	return true
}

// remainingQuota estimates how much usage headroom an account still has.
// Higher is better. Unknown limits (UsageLimit<=0) rank as "plenty" so cold
// accounts without refreshed quota metadata are not starved.
func remainingQuota(acc config.Account) float64 {
	if acc.UsageLimit <= 0 {
		return math.MaxFloat64
	}
	left := acc.UsageLimit - acc.UsageCurrent
	if left < 0 {
		return 0
	}
	return left
}

// betterAccount reports whether candidate is preferable to current best under
// "prefer more remaining quota, then higher weight" within the same pass.
func betterAccount(candidate, best *config.Account) bool {
	if best == nil {
		return true
	}
	cr, br := remainingQuota(*candidate), remainingQuota(*best)
	if cr != br {
		return cr > br
	}
	// Tie-break: higher operator weight, then lower lifetime request count
	// (spread load), then stable ID order.
	cw, bw := effectiveWeight(candidate.Weight), effectiveWeight(best.Weight)
	if cw != bw {
		return cw > bw
	}
	if candidate.RequestCount != best.RequestCount {
		return candidate.RequestCount < best.RequestCount
	}
	return candidate.ID < best.ID
}

// selectAccountLocked picks the next account. model=="" disables model filter.
// Rules (in order):
//  1. Prefer already-used accounts over never-used (virgin) imports
//  2. Among eligible peers, prefer higher remaining quota
//  3. Virgin first-use is globally rate-limited (one new account per interval)
//
// Caller must hold p.mu for writing (needed to claim virgin first-use).
func (p *AccountPool) selectAccountLocked(excluded map[string]bool, model string) *config.Account {
	if len(p.accounts) == 0 {
		return nil
	}

	allowOverUsage := config.GetAllowOverUsage()
	now := time.Now()
	n := len(p.accounts)

	// Advance RR cursor so repeated calls still rotate when many accounts share
	// the same remaining quota (avoids sticky ID-order under ties).
	start := int(atomic.AddUint64(&p.currentIndex, 1) % uint64(n))

	pickBest := func(allowVirgin bool) *config.Account {
		var best *config.Account
		seen := make(map[string]struct{}, 8)
		for i := 0; i < n; i++ {
			acc := &p.accounts[(start+i)%n]

			if excluded != nil && excluded[acc.ID] {
				continue
			}
			if _, ok := seen[acc.ID]; ok {
				continue
			}
			seen[acc.ID] = struct{}{}

			if model != "" && !p.accountHasModel(acc.ID, model) {
				continue
			}
			if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
				continue
			}
			if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
				continue
			}
			if isQuotaBlocked(*acc, allowOverUsage) {
				continue
			}

			virgin := p.isVirginAccount(acc)
			if virgin && !allowVirgin {
				continue
			}
			// Do not claim yet — only claim the eventual winner to avoid
			// burning the global first-use slot on a non-selected virgin.
			if virgin {
				if !p.nextNewFirstUseAt.IsZero() && now.Before(p.nextNewFirstUseAt) {
					continue
				}
			}

			if betterAccount(acc, best) {
				best = acc
			}
		}
		if best == nil {
			return nil
		}
		if p.isVirginAccount(best) && !p.claimVirginFirstUseLocked(best, now) {
			// Race with interval edge: treat as unavailable this pass.
			return nil
		}
		return accountCopy(best)
	}

	// Pass 1: already-used / previously claimed accounts only.
	if acc := pickBest(false); acc != nil {
		return acc
	}
	// Pass 2: allow one virgin first-use if the global interval permits.
	if acc := pickBest(true); acc != nil {
		return acc
	}

	// Fallback: earliest-cooldown non-virgin (never force a gated virgin here).
	// Prefer the account whose cooldown ends soonest; among equal cooldowns,
	// still prefer higher remaining quota.
	var best *config.Account
	var earliest time.Time
	seen := make(map[string]struct{}, 8)
	for i := range p.accounts {
		acc := &p.accounts[i]
		if excluded != nil && excluded[acc.ID] {
			continue
		}
		if _, ok := seen[acc.ID]; ok {
			continue
		}
		seen[acc.ID] = struct{}{}
		if model != "" && !p.accountHasModel(acc.ID, model) {
			continue
		}
		if isQuotaBlocked(*acc, allowOverUsage) {
			continue
		}
		if p.isVirginAccount(acc) {
			continue
		}
		cooldown, ok := p.cooldowns[acc.ID]
		if !ok {
			// Should have been picked above; return immediately as available.
			return accountCopy(acc)
		}
		if best == nil || cooldown.Before(earliest) ||
			(cooldown.Equal(earliest) && betterAccount(acc, best)) {
			best = acc
			earliest = cooldown
		}
	}
	return accountCopy(best)
}

// GetNext 获取下一个可用账号（加权轮询）
func (p *AccountPool) GetNext() *config.Account {
	return p.GetNextExcluding(nil)
}

// GetNextExcluding 获取下一个可用账号（加权轮询），并跳过指定账号。
// 返回账号的堆拷贝，避免 Reload 替换底层 slice 后产生悬空指针竞态。
// 从未使用过的新账号首次调度受 NewAccountFirstUseInterval 全局间隔限制。
func (p *AccountPool) GetNextExcluding(excluded map[string]bool) *config.Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectAccountLocked(excluded, "")
}

// SetModelList 缓存账号支持的模型集合（由 handler 在刷新后调用）
func (p *AccountPool) SetModelList(accountID string, modelIDs []string) {
	set := make(map[string]bool, len(modelIDs))
	for _, id := range modelIDs {
		set[strings.ToLower(strings.TrimSpace(id))] = true
	}
	p.mu.Lock()
	p.modelLists[accountID] = set
	p.mu.Unlock()
}

// GetModelList 返回该账号缓存的模型 ID 列表（供 admin API 使用）。
// 若尚无缓存则返回空切片。
func (p *AccountPool) GetModelList(accountID string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set, ok := p.modelLists[accountID]
	if !ok || len(set) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}

// accountHasModel 检查账号是否支持指定模型。
// 若该账号尚无模型列表（冷启动），视为支持所有模型。
func (p *AccountPool) accountHasModel(accountID, model string) bool {
	list, ok := p.modelLists[accountID]
	if !ok || len(list) == 0 {
		return true // 冷启动：列表未就绪，乐观放行
	}
	return list[strings.ToLower(strings.TrimSpace(model))]
}

// GetNextForModel 获取下一个支持指定模型的可用账号。
// model 应为去掉 thinking 后缀的实际模型名。
// 若无账号有该模型列表数据，行为与 GetNext 相同（乐观路由）。
func (p *AccountPool) GetNextForModel(model string) *config.Account {
	return p.GetNextForModelExcluding(model, nil)
}

// GetNextForModelExcluding 获取下一个支持指定模型的可用账号，并跳过指定账号。
// 返回账号堆拷贝（见 GetNextExcluding）。
// 从未使用过的新账号首次调度受 NewAccountFirstUseInterval 全局间隔限制。
func (p *AccountPool) GetNextForModelExcluding(model string, excluded map[string]bool) *config.Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectAccountLocked(excluded, model)
}

// GetByID 根据 ID 获取账号
func (p *AccountPool) GetByID(id string) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return accountCopy(&p.accounts[i])
		}
	}
	return nil
}

// RecordSuccess 记录请求成功，清除冷却
func (p *AccountPool) RecordSuccess(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cooldowns, id)
	p.errorCounts[id] = 0
}

// RecordError 记录请求错误，设置冷却
func (p *AccountPool) RecordError(id string, isQuotaError bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.errorCounts[id]++

	if isQuotaError {
		// 配额错误，冷却 1 小时
		p.cooldowns[id] = time.Now().Add(time.Hour)
	} else if p.errorCounts[id] >= 3 {
		// 连续 3 次错误，冷却 1 分钟
		p.cooldowns[id] = time.Now().Add(time.Minute)
	}
}

// IsAuthFailure reports whether an error indicates the refresh token / credentials
// have been revoked or invalidated upstream (401, 403 with auth markers, etc.).
// These accounts cannot be recovered automatically and must be re-authenticated.
func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	// Match HTTP status codes only when they appear as standalone tokens to avoid
	// false positives from arbitrary digits in the error body (e.g. request IDs).
	if hasStatusToken(msg, "401") || hasStatusToken(msg, "403") {
		return true
	}
	if strings.Contains(lower, "bad credentials") ||
		strings.Contains(lower, "invalid_grant") ||
		strings.Contains(lower, "invalid grant") ||
		strings.Contains(lower, "invalid_token") ||
		strings.Contains(lower, "invalid token") ||
		strings.Contains(lower, "token expired") ||
		strings.Contains(lower, "token has expired") ||
		strings.Contains(lower, "unauthorized") {
		return true
	}
	return false
}

// hasStatusToken returns true when status appears in s with non-digit boundaries
// on both sides, so "401" matches "HTTP 401 from ..." but not "request_401abc".
func hasStatusToken(s, status string) bool {
	for {
		idx := strings.Index(s, status)
		if idx < 0 {
			return false
		}
		leftOK := idx == 0 || !isDigit(s[idx-1])
		rightIdx := idx + len(status)
		rightOK := rightIdx >= len(s) || !isDigit(s[rightIdx])
		if leftOK && rightOK {
			return true
		}
		s = s[idx+len(status):]
	}
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// IsSuspensionError reports whether the error indicates the account has been
// temporarily suspended by upstream or has no available Kiro profile.
// Unlike auth failures (revoked credentials), these may be transient, but
// the account should be disabled until an operator re-enables it.
func IsSuspensionError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "temporarily_suspended") ||
		strings.Contains(lower, "temporarily suspended") ||
		strings.Contains(lower, "no available kiro profile")
}

// DisableAccount marks an account as disabled (auth revoked / unrecoverable),
// removes it from the in-memory pool so subsequent requests skip it, and
// persists the change via config.SetAccountBanStatus.
func (p *AccountPool) DisableAccount(id, reason string) {
	if err := config.SetAccountBanStatus(id, "DISABLED", reason); err != nil {
		// best effort — even if persistence fails, drop it from memory
		_ = err
	}
	p.mu.Lock()
	// Long cooldown as a safety net in case Reload races
	p.cooldowns[id] = time.Now().Add(24 * time.Hour)
	p.mu.Unlock()
	p.Reload()
}

// MarkOverLimit marks an account as over usage limit (after a 402 / OVERAGE response).
// With the upstream OverageStatus model, the live status is refreshed via
// FetchOverageStatus from the request handler; here we just cooldown briefly so
// the next attempt picks a different account, then reload.
func (p *AccountPool) MarkOverLimit(id string) {
	p.mu.Lock()
	p.cooldowns[id] = time.Now().Add(time.Hour)
	p.mu.Unlock()
	p.Reload()
}

// UpdateToken 更新账号 Token
func (p *AccountPool) UpdateToken(id, accessToken, refreshToken string, expiresAt int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	updated := false
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].AccessToken = accessToken
			if refreshToken != "" {
				p.accounts[i].RefreshToken = refreshToken
			}
			p.accounts[i].ExpiresAt = expiresAt
			updated = true
		}
	}
	if updated {
		if p.dirty == nil {
			p.dirty = make(map[string]struct{})
		}
		p.dirty[id] = struct{}{}
	}
}

// Count 返回账号总数
func (p *AccountPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.totalAccounts > 0 {
		return p.totalAccounts
	}

	seen := make(map[string]bool)
	for _, acc := range p.accounts {
		seen[acc.ID] = true
	}
	return len(seen)
}

// AvailableCount 返回可用账号数
func (p *AccountPool) AvailableCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	count := 0
	seen := make(map[string]bool)
	for _, acc := range p.accounts {
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			continue
		}
		count++
	}
	return count
}

// UpdateStats 更新账号统计
func (p *AccountPool) UpdateStats(id string, tokens int, credits float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var updated bool
	var requestCount, errorCount, totalTokens int
	var totalCredits float64
	var lastUsed int64
	var dailyRequests, dailyTokens int
	var dailyCredits float64
	today := time.Now().Format("2006-01-02")

	for i := range p.accounts {
		if p.accounts[i].ID == id {
			if !updated {
				// 更新全局统计
				p.accounts[i].RequestCount++
				p.accounts[i].TotalTokens += tokens
				p.accounts[i].TotalCredits += credits
				p.accounts[i].LastUsed = time.Now().Unix()

				// 更新每日统计
				if p.accounts[i].DailyDate != today {
					p.accounts[i].DailyRequests = 0
					p.accounts[i].DailyTokens = 0
					p.accounts[i].DailyCredits = 0
					p.accounts[i].DailyDate = today
				}
				p.accounts[i].DailyRequests++
				p.accounts[i].DailyTokens += tokens
				p.accounts[i].DailyCredits += credits

				requestCount = p.accounts[i].RequestCount
				errorCount = p.accounts[i].ErrorCount
				totalTokens = p.accounts[i].TotalTokens
				totalCredits = p.accounts[i].TotalCredits
				lastUsed = p.accounts[i].LastUsed
				dailyRequests = p.accounts[i].DailyRequests
				dailyTokens = p.accounts[i].DailyTokens
				dailyCredits = p.accounts[i].DailyCredits
				updated = true
				continue
			}
			// 同步同一 ID 的副本（加权列表可能重复）
			p.accounts[i].RequestCount = requestCount
			p.accounts[i].ErrorCount = errorCount
			p.accounts[i].TotalTokens = totalTokens
			p.accounts[i].TotalCredits = totalCredits
			p.accounts[i].LastUsed = lastUsed
			p.accounts[i].DailyRequests = dailyRequests
			p.accounts[i].DailyTokens = dailyTokens
			p.accounts[i].DailyCredits = dailyCredits
			p.accounts[i].DailyDate = today
		}
	}
	// 仅更新内存统计；标记 dirty，由后台 stats saver 批量 flush。
	if updated {
		if p.dirty == nil {
			p.dirty = make(map[string]struct{})
		}
		p.dirty[id] = struct{}{}
	}
}

// TakeDirtyAccountStats returns runtime stats for accounts marked dirty since the
// last take, then clears the dirty set. Used by the background stats saver so we
// do not rewrite thousands of unchanged account rows every 30s under 4k concurrency.
func (p *AccountPool) TakeDirtyAccountStats() []config.AccountRuntimeStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.dirty) == 0 {
		return nil
	}
	out := make([]config.AccountRuntimeStats, 0, len(p.dirty))
	for id := range p.dirty {
		for i := range p.accounts {
			if p.accounts[i].ID != id {
				continue
			}
			a := p.accounts[i]
			out = append(out, config.AccountRuntimeStats{
				ID:            a.ID,
				RequestCount:  a.RequestCount,
				ErrorCount:    a.ErrorCount,
				LastUsed:      a.LastUsed,
				TotalTokens:   a.TotalTokens,
				TotalCredits:  a.TotalCredits,
				DailyRequests: a.DailyRequests,
				DailyTokens:   a.DailyTokens,
				DailyCredits:  a.DailyCredits,
				DailyDate:     a.DailyDate,
			})
			break
		}
	}
	p.dirty = make(map[string]struct{}, len(p.dirty))
	return out
}

// ResetRuntimeStats clears in-memory per-account request/token/credit counters.
// Does not change enablement, ban, tokens, or upstream quota fields.
func (p *AccountPool) ResetRuntimeStats() {
	p.mu.Lock()
	defer p.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	for i := range p.accounts {
		p.accounts[i].RequestCount = 0
		p.accounts[i].ErrorCount = 0
		p.accounts[i].LastUsed = 0
		p.accounts[i].TotalTokens = 0
		p.accounts[i].TotalCredits = 0
		p.accounts[i].DailyRequests = 0
		p.accounts[i].DailyTokens = 0
		p.accounts[i].DailyCredits = 0
		p.accounts[i].DailyDate = today
	}
	// Drop dirty set so background saver does not re-apply old counters.
	p.dirty = make(map[string]struct{})
}

// GetAllAccounts 获取所有账号副本
func (p *AccountPool) GetAllAccounts() []config.Account {

	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]config.Account, len(p.accounts))
	copy(result, p.accounts)
	return result
}

func isOverUsageLimit(acc config.Account) bool {
	return acc.UsageLimit > 0 && acc.UsageCurrent >= acc.UsageLimit
}

// isQuotaBlocked reports whether an over-quota account should be skipped:
// the per-account upstream Overages switch (OverageStatus=ENABLED) and the
// global allowOverUsage setting are the two ways to keep it routable.
func isQuotaBlocked(acc config.Account, allowOverUsage bool) bool {
	return isOverUsageLimit(acc) && !isUpstreamOverageEnabled(acc) && !allowOverUsage
}

// isUpstreamOverageEnabled reports whether the upstream Overages switch is ON for this account.
// "ENABLED" → true; anything else (DISABLED, UNKNOWN, empty) → false.
func isUpstreamOverageEnabled(acc config.Account) bool {
	return strings.EqualFold(acc.OverageStatus, "ENABLED")
}

func effectiveWeight(weight int) int {
	if weight < 1 {
		return 1
	}
	return weight
}
