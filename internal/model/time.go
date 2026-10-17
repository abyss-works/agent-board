package model

import (
	"fmt"
	"time"
)

// TimeLayout is the wire and storage format: RFC 3339 UTC with milliseconds.
// Strings in this layout sort chronologically.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// Time is a UTC timestamp with millisecond precision.
type Time struct{ t time.Time }

// NewTime truncates t to milliseconds and converts it to UTC.
func NewTime(t time.Time) Time {
	return Time{t.UTC().Truncate(time.Millisecond)}
}

// ParseTime parses an RFC 3339 timestamp.
func ParseTime(s string) (Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return Time{}, err
	}
	return NewTime(t), nil
}

// Time returns the underlying time.Time.
func (t Time) Time() time.Time { return t.t }

// IsZero reports whether the timestamp is unset.
func (t Time) IsZero() bool { return t.t.IsZero() }

// String formats the timestamp in TimeLayout.
func (t Time) String() string { return t.t.Format(TimeLayout) }

// Before reports whether t is before u.
func (t Time) Before(u Time) bool { return t.t.Before(u.t) }

// MarshalJSON writes the timestamp as a JSON string in TimeLayout.
func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.String() + `"`), nil
}

// UnmarshalJSON reads an RFC 3339 JSON string.
func (t *Time) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("time must be a JSON string")
	}
	parsed, err := ParseTime(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
