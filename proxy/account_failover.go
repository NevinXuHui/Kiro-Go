package proxy

import (
	"errors"
	"kiro-go/config"
	"kiro-go/logger"
	"strings"
)

const maxAccountRetryAttempts = 3

// maxSameAccountStreamRetries bounds same-account recovery of a truncated
// stream. Kiro IDE caps its truncation retry at one (Dt3 = 1 in extension.js),
// but that budget was set for a single-credential client. This proxy also
// rotates accounts, and the two recover different failures: a same-account
// retry helps when the upstream hiccupped, rotation helps when that account's
// backend is unhealthy. Two is therefore not a copy of the IDE.
//
// Cost of the extra attempt is bounded and small. Truncation returns nil from
// CallKiroAPIContext, so it never reaches the endpoint fallback or
// maxStreamAttemptsPerEndpoint - those fire only on transport errors. Worst
// case across maxAccountRetryAttempts accounts is 3*(1+budget) requests: 9 at
// two retries versus 6 at one.
//
// The payoff lands mostly on the three fully buffered paths, whose canRetry is
// nil: a non-stream client gets a 500 with nothing usable, so one more chance
// is worth more there than on a stream that has already flushed partial text.
const maxSameAccountStreamRetries = 2

// errUpstreamTruncatedResponse is a soft failure raised when a transport-clean
// stream carried content but never a terminal signal. It is retryable on the
// same account and must not mark the account unhealthy.
//
// There is deliberately no empty-response error here. A stream that produced no
// output at all is already caught one layer down: parseEventStreamTracked
// returns errEmptyKiroStream when !sawOutput (proxy/kiro.go), and
// CallKiroAPIContext retries it internally. Since sawOutput is set by exactly
// the three signals classifyStreamIntegrity measures (content, reasoning,
// toolUse), an all-zero measurement can never reach this layer with a nil
// error.
var errUpstreamTruncatedResponse = errors.New("upstream truncated response without stop reason")

// classifyStreamIntegrity decides whether an upstream stream that returned no
// transport error is actually complete. parseEventStream reports success on a
// clean EOF, so a stream that died mid-answer is otherwise indistinguishable
// from a finished one.
//
// Complete when a stopReason arrived, or when a tool call was delivered. Both
// match Kiro IDE, whose empty and truncation predicates each require
// toolCallCount === 0.
//
// Truncated when content arrived without any terminal signal.
//
// Reasoning-only with no answer is STRICTER THAN THE IDE, deliberately. The
// IDE's truncation predicate ends in (contentChars > 0 || !reasoningSeen), so
// reasoning with no answer and no stopReason is treated as complete there and
// is never retried. That is the exact shape of the production symptom this
// proxy exists to fix: thinking streams in full, then the turn dies before the
// answer or the tool call. Handing a client reasoning with no answer as a
// successful turn is what made the failure invisible, so it is classified as
// truncated here.
func classifyStreamIntegrity(contentChars, toolCallCount int, stopReason string, sawReasoning bool) error {
	if strings.TrimSpace(stopReason) != "" {
		return nil
	}
	if toolCallCount > 0 {
		return nil
	}
	if contentChars > 0 || sawReasoning {
		return errUpstreamTruncatedResponse
	}
	// No content, no reasoning, no tools: unreachable through the wired paths
	// (errEmptyKiroStream fires first, see above). Treated as truncated rather
	// than complete so a future caller that bypasses that guard still cannot
	// ship an empty turn as a success.
	return errUpstreamTruncatedResponse
}

// isStreamIntegrityError reports whether err is a soft integrity failure.
// Callers may rotate accounts on these, but must not run them through
// handleAccountFailure: an upstream blip should not mark an account unhealthy.
func isStreamIntegrityError(err error) bool {
	return errors.Is(err, errUpstreamTruncatedResponse)
}

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

// isRateLimitErrorMessage detects transient throttling (HTTP 429 / "too many
// requests") that is NOT monthly/hard quota exhaustion. These should use a
// short cooldown so free-tier numbers re-enter the pool quickly after a burst.
func isRateLimitErrorMessage(msg string) bool {
	if isMonthlyQuotaErrorMessage(msg) || isOverageErrorMessage(msg) {
		return false
	}
	lower := strings.ToLower(msg)
	// Explicit hard-quota phrases stay on the longer quota path.
	if strings.Contains(lower, "quota exhausted") ||
		(strings.Contains(lower, "quota") && !strings.Contains(lower, "rate limited")) {
		return false
	}
	if strings.Contains(lower, "rate limited") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "throttl") {
		return true
	}
	// Bare 429 status token (and our "rate limited (HTTP 429) on …" errors).
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
	// Always drop the select() in-flight claim — this path does not go through RecordError.
	defer h.pool.ReleaseInFlight(account.ID)

	if !account.Enabled && account.BanStatus == banStatus && account.BanReason == banReason {
		return
	}

	if err := config.SetAccountBanStatus(account.ID, banStatus, banReason); err != nil {
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
	case isRateLimitErrorMessage(errMsg):
		// Bare 429 / throttling: short cooldown so the number re-enters after
		// the burst window instead of sitting out a full quota hour.
		h.pool.RecordRateLimit(account.ID)
		logger.Infof("[AccountFailover] Rate-limit cooldown %s after 429", account.Email)
	case isQuotaErrorMessage(errMsg):
		// Explicit quota-exhausted wording (non-monthly): medium cooldown.
		h.pool.RecordError(account.ID, true)
		logger.Infof("[AccountFailover] Cooldown %s after quota/429", account.Email)
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
