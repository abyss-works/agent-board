package model

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Claim outcome values recorded when an issue claim is released.
const (
	ClaimOutcomeDone      = "done"
	ClaimOutcomeAbandoned = "abandoned"
	ClaimOutcomeExpired   = "expired"
	ClaimOutcomeTakenOver = "taken_over"
)

// Holder source values distinguishing stored claims from activity fallbacks.
const (
	HolderSourceClaim    = "claim"
	HolderSourceActivity = "activity"
)

// MaxClaimReasonRunes is the maximum length of a claim release reason after
// trimming surrounding whitespace.
const MaxClaimReasonRunes = 200

// CleanClaimReason checks a claim release reason and returns it with
// surrounding whitespace trimmed. Control characters are rejected before
// trimming; the trimmed string must be 1..MaxClaimReasonRunes runes.
func CleanClaimReason(raw string) (string, string, bool) {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", "제어 문자를 넣을 수 없습니다.", false
		}
	}
	trimmed := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(trimmed); n < 1 || n > MaxClaimReasonRunes {
		return "", fmt.Sprintf("앞뒤 공백 제거 후 1~%d자여야 합니다.", MaxClaimReasonRunes), false
	}
	return trimmed, "", true
}

// IssueClaim is a stored claim on an issue held by a session.
type IssueClaim struct {
	ID         int64   `json:"id"`
	Issue      string  `json:"issue"`
	Actor      string  `json:"actor"`
	SessionID  string  `json:"session_id"`
	ClaimedAt  Time    `json:"claimed_at"`
	ReleasedAt *Time   `json:"released_at"`
	Outcome    *string `json:"outcome"`
	Reason     *string `json:"reason"`
	Active     bool    `json:"active"`
}
