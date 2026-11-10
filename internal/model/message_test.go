package model

import (
	"strings"
	"testing"
	"time"
)

func TestMessageTTL(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", DefaultMessageTTL},
		{"30m", 30 * time.Minute},
		{"24h", 24 * time.Hour},
		{"3d", 72 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
		{"2160h", 90 * 24 * time.Hour},
		{"1m", time.Minute},
	}
	for _, c := range cases {
		got, err := MessageTTL(c.in)
		if err != nil || got != c.want {
			t.Errorf("MessageTTL(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if DefaultMessageTTL != 7*24*time.Hour || MaxMessageTTL != 90*24*time.Hour {
		t.Errorf("default %v, max %v", DefaultMessageTTL, MaxMessageTTL)
	}
	for _, in := range []string{"0", "0d", "-1h", "1d12h", "abc", "d", "1.5d", "91d", "2161h", "30s", "1w", " 3d"} {
		if _, err := MessageTTL(in); err == nil {
			t.Errorf("MessageTTL(%q) must fail", in)
		}
	}
}

func fieldsOf(err error) []string {
	verr, ok := err.(*ValidationError)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range verr.Fields {
		out = append(out, f.Field)
	}
	return out
}

func TestValidateMessage(t *testing.T) {
	if err := ValidateMessage("reviewer", "제목", "", "3d"); err != nil {
		t.Errorf("valid message: %v", err)
	}
	if err := ValidateMessage("", "제목", "", ""); err != nil {
		t.Errorf("minimal message: %v", err)
	}
	if err := ValidateMessage(strings.Repeat("가", 64), strings.Repeat("가", 200), strings.Repeat("a", MaxBodyBytes), ""); err != nil {
		t.Errorf("limits: %v", err)
	}
	bad := map[string][4]string{
		"title":        {"", "", "", ""},
		"to":           {"a b", "t", "", ""},
		"ttl":          {"", "t", "", "91d"},
		"body":         {"", "t", strings.Repeat("a", MaxBodyBytes+1), ""},
		"title,to,ttl": {strings.Repeat("가", 65), strings.Repeat("x", 201), "", "x"},
	}
	for want, in := range bad {
		err := ValidateMessage(in[0], in[1], in[2], in[3])
		if err == nil {
			t.Errorf("%q must fail", in)
			continue
		}
		if want == "title,to,ttl" {
			if got := strings.Join(fieldsOf(err), ","); got != "to,title,ttl" {
				t.Errorf("fields = %s", got)
			}
			continue
		}
		if got := strings.Join(fieldsOf(err), ","); got != want {
			t.Errorf("ValidateMessage(%q) fields = %s, want %s", in, got, want)
		}
	}
}
