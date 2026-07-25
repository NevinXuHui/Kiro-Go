package proxy

import (
	"kiro-go/config"
	"kiro-go/logger"
	"strings"
	"time"
)

const maxAccountRetryAttempts = 3

// hasHTTPStatusToken matches status codes as standalone tokens so UUID fragments
// like "4429" do not falsely match "429".
func hasHTTPStatusToken(s, status string) bool {
	for {
		idx := strings.Index(s, status)
		if idx < 0 {
			return false
		}
		leftOK := idx == 0 || !isASCIIDigit(s[idx-1])
		rightIdx := idx + len(status)
		rightOK := rightIdx >= len(s) || !isASCIIDigit(s[rightIdx])
		if leftOK && rightOK {
			return true
		}
		s = s[idx+len(status):]
	}
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isQuotaErrorMessage(msg string) bool {
	lower := strings.ToLower(msg)
	// Prefer explicit quota wording; only treat 429 as a status token so Kiro
	// User IDs containing "4429" are not misclassified as rate-limit/quota.
	if strings.Contains(lower, "quota") || strings.Contains(lower, "quota exhausted") {
		return true
	}
	return hasHTTPStatusToken(msg, "429") || hasHTTPStatusToken(lower, "429")
}

func isOverageErrorMessage(msg string) bool {
	msg = strings.ToLower(msg)
	return (hasHTTPStatusToken(msg, "402") || strings.Contains(msg, "http 402")) &&
		strings.Contains(msg, "overage")
}

// isMonthlyQuotaErrorMessage detects free-tier / monthly request count exhaustion
// (e.g. HTTP 402 ... "You have reached the limit." reason MONTHLY_REQUEST_COUNT).
func isMonthlyQuotaErrorMessage(msg string) bool {
	msg = strings.ToLower(msg)
	if strings.Contains(msg, "monthly_request_count") {
		return true
	}
	if strings.Contains(msg, "reached the limit") {
		return true
	}
	// 402 with limit wording but not necessarily "overage"
	if (hasHTTPStatusToken(msg, "402") || strings.Contains(msg, "http 402")) &&
		strings.Contains(msg, "limit") && !strings.Contains(msg, "overage") {
		return true
	}
	return false
}

func isSuspensionErrorMessage(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "temporarily_suspended") ||
		strings.Contains(msg, "temporarily is suspended") ||
		strings.Contains(msg, "account suspended") ||
		// Kiro support form URL is a strong signal of account lock/suspension.
		strings.Contains(msg, "locked your account as a security precaution") ||
		(strings.Contains(msg, "suspended") && strings.Contains(msg, "kiro.dev/account/usage"))
}

func isProfileUnavailableErrorMessage(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "no available kiro profile")
}

func isAuthErrorMessage(msg string) bool {
	// Suspension is more specific than bare 403; callers should check it first.
	if isSuspensionErrorMessage(msg) {
		return false
	}
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "http 401") ||
		strings.Contains(msg, "http 403") ||
		hasHTTPStatusToken(msg, "401") ||
		hasHTTPStatusToken(msg, "403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "token invalid") ||
		strings.Contains(msg, "token expired") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "access token expired") ||
		strings.Contains(msg, "refresh token expired")
}

func (h *Handler) markAccountQuotaExhausted(account *config.Account) {
	if account == nil {
		return
	}
	if err := config.MarkAccountUsageExhausted(account.ID); err != nil {
		logger.Warnf("[AccountFailover] Failed to mark %s exhausted: %v", account.Email, err)
		return
	}
	// Keep local snapshot consistent for callers still holding the pointer.
	if account.UsageLimit > 0 {
		if account.UsageCurrent < account.UsageLimit {
			account.UsageCurrent = account.UsageLimit
		}
	} else if account.UsageCurrent > 0 {
		account.UsageLimit = account.UsageCurrent
	} else {
		account.UsageLimit = 1
		account.UsageCurrent = 1
	}
	account.UsagePercent = 1
	logger.Infof("[AccountFailover] Marked %s as quota exhausted (usage %.1f/%.1f)", account.Email, account.UsageCurrent, account.UsageLimit)
	h.pool.Reload()
}

func (h *Handler) disableAccount(account *config.Account, banStatus, banReason string) {
	if account == nil {
		return
	}

	updatedAccount := *account
	if !updatedAccount.Enabled && updatedAccount.BanStatus == banStatus && updatedAccount.BanReason == banReason {
		return
	}

	updatedAccount.Enabled = false
	updatedAccount.BanStatus = banStatus
	updatedAccount.BanReason = banReason
	updatedAccount.BanTime = time.Now().Unix()

	if err := config.UpdateAccount(account.ID, updatedAccount); err != nil {
		logger.Warnf("[AccountFailover] Failed to disable %s: %v", account.Email, err)
		return
	}

	logger.Warnf("[AccountFailover] Disabled %s: %s", account.Email, banReason)
	h.pool.Reload()
}

func (h *Handler) disableAccountOverage(account *config.Account) {
	if account == nil {
		return
	}

	snap, fetchErr := FetchOverageStatus(account)
	if fetchErr != nil {
		logger.Warnf("[AccountFailover] Failed to refresh overage status for %s: %v", account.Email, fetchErr)
		return
	}
	if persistErr := PersistOverageSnapshot(account.ID, snap); persistErr != nil {
		logger.Warnf("[AccountFailover] Failed to persist overage snapshot for %s: %v", account.Email, persistErr)
		return
	}

	logger.Warnf("[AccountFailover] Refreshed overage status for %s after upstream overage limit error: %s", account.Email, snap.Status)
	h.pool.Reload()
}

func (h *Handler) handleAccountFailure(account *config.Account, err error) {
	if account == nil || err == nil {
		return
	}

	errMsg := err.Error()
	switch {
	// Suspension must be classified before quota/auth: Kiro User IDs can embed
	// digit sequences like "4429" that used to false-positive as HTTP 429.
	case isSuspensionErrorMessage(errMsg):
		h.disableAccount(account, "BANNED", "AWS temporarily suspended - unusual user activity detected")
	case isOverageErrorMessage(errMsg):
		h.disableAccountOverage(account)
		h.pool.RecordError(account.ID, false)
	case isMonthlyQuotaErrorMessage(errMsg):
		h.markAccountQuotaExhausted(account)
		h.pool.MarkOverLimit(account.ID)
	case isQuotaErrorMessage(errMsg):
		h.pool.RecordError(account.ID, true)
	case isProfileUnavailableErrorMessage(errMsg):
		// Profile ARN may be transiently unresolvable (upstream blip, stale token).
		// Treat as a soft failure: short cooldown so the next request rotates account,
		// but never auto-disable — operators can still investigate via warn logs.
		h.pool.RecordError(account.ID, false)
	case isAuthErrorMessage(errMsg):
		h.disableAccount(account, "BANNED", "Authentication failed - token invalid or expired")
	default:
		h.pool.RecordError(account.ID, false)
	}
}
