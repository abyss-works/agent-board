package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits and defaults of a message.
const (
	MaxMessageToRunes = 64
	DefaultMessageTTL = 7 * 24 * time.Hour
	MaxMessageTTL     = 90 * 24 * time.Hour
	minMessageTTL     = time.Minute
)

// Message states. A deleted message stays deleted whatever its expiry.
const (
	MessageActive  = "active"
	MessageExpired = "expired"
	MessageDeleted = "deleted"
)

// Message is a short-lived note in the message space of a project. Body is
// nil in lists, which leave the text out.
type Message struct {
	ID             int64   `json:"id"`
	Project        string  `json:"project"`
	To             *string `json:"to"`
	Title          string  `json:"title"`
	Body           *string `json:"body,omitempty"`
	State          string  `json:"state"`
	Age            string  `json:"age"`
	CreatedAt      Time    `json:"created_at"`
	CreatedBy      string  `json:"created_by"`
	CreatedSession string  `json:"created_session"`
	CreatedHost    string  `json:"created_host"`
	ExpiresAt      Time    `json:"expires_at"`
	DeletedAt      *Time   `json:"deleted_at"`
	DeletedBy      *string `json:"deleted_by"`
}

// MessageTTL reads a time to live: a whole number of days such as 3d, or a
// duration such as 24h or 30m. An empty value is the default. A TTL is at
// least one minute and at most MaxMessageTTL.
func MessageTTL(s string) (time.Duration, error) {
	if s == "" {
		return DefaultMessageTTL, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseUint(days, 10, 16)
		if err != nil {
			return 0, fmt.Errorf("%q는 TTL이 아닙니다", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q는 TTL이 아닙니다", s)
		}
	}
	if d < minMessageTTL || d > MaxMessageTTL {
		return 0, fmt.Errorf("TTL은 1분 이상 90일 이하여야 합니다")
	}
	return d, nil
}

// ValidateMessage checks the fields of a new message. An empty to means the
// message has no addressee and an empty ttl the default.
func ValidateMessage(to, title, body, ttl string) error {
	var e ValidationError
	if to != "" {
		n := utf8.RuneCountInString(to)
		if n > MaxMessageToRunes || strings.IndexFunc(to, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			e.add("to", "공백과 제어 문자 없이 1~%d자여야 합니다.", MaxMessageToRunes)
		}
	}
	if n := utf8.RuneCountInString(title); n < 1 || n > MaxTitleRunes {
		e.add("title", "1~%d자여야 합니다.", MaxTitleRunes)
	}
	if len(body) > MaxBodyBytes {
		e.add("body", "%d바이트를 넘을 수 없습니다.", MaxBodyBytes)
	}
	if _, err := MessageTTL(ttl); err != nil {
		e.add("ttl", "24h, 3d처럼 1분 이상 90일 이하의 기간이어야 합니다.")
	}
	return e.orNil()
}
