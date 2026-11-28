package model

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUTF8Text(t *testing.T) {
	if err := ValidateUTF8TextString("안녕하세요 # markdown\n"); err != nil {
		t.Fatalf("valid utf8: %v", err)
	}
	if err := ValidateUTF8TextBytes([]byte(strings.Repeat("a", MaxDocumentBytes))); err != nil {
		t.Fatalf("exact 2 MiB: %v", err)
	}
	if err := ValidateUTF8TextBytes([]byte(strings.Repeat("a", MaxDocumentBytes+1))); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("over 2 MiB = %v, want ErrPayloadTooLarge", err)
	}
	if err := ValidateUTF8TextBytes([]byte{0xff, 0xfe, 0x01}); !errors.Is(err, ErrInvalidUTF8) {
		t.Errorf("invalid utf8 = %v, want ErrInvalidUTF8", err)
	}
	if err := ValidateUTF8TextString("hello\x00world"); !errors.Is(err, ErrBinaryPayload) {
		t.Errorf("nul byte = %v, want ErrBinaryPayload", err)
	}
}

func TestDomainEnumsAndGlobalScope(t *testing.T) {
	if GlobalProjectID != 0 || GlobalProjectKey != "GLOBAL" {
		t.Fatalf("global constants = (%d, %q)", GlobalProjectID, GlobalProjectKey)
	}
	if _, ok := ParseScopeKind("global"); !ok {
		t.Error("ParseScopeKind(global) failed")
	}
	if _, ok := ParseScopeKind("project"); !ok {
		t.Error("ParseScopeKind(project) failed")
	}
	if _, ok := ParseScopeKind("other"); ok {
		t.Error("ParseScopeKind(other) succeeded")
	}
	for _, s := range []string{"planned", "active", "closed", "deleted"} {
		if _, ok := ParseMilestoneStageStatus(s); !ok {
			t.Errorf("ParseMilestoneStageStatus(%q) failed", s)
		}
	}
	got, err := ExpandMilestoneStageStatuses("all")
	if err != nil || len(got) != 4 {
		t.Errorf("ExpandMilestoneStageStatuses(all) = %v, %v", got, err)
	}
	for _, k := range []string{"supersedes", "about", "depends_on", "related"} {
		if _, ok := ParseEdgeKind(k); !ok {
			t.Errorf("ParseEdgeKind(%q) failed", k)
		}
	}
	for _, o := range []string{"explicit", "inferred"} {
		if _, ok := ParseEdgeOrigin(o); !ok {
			t.Errorf("ParseEdgeOrigin(%q) failed", o)
		}
	}
	for _, st := range []string{"confirmed", "candidate", "rejected", "removed"} {
		if _, ok := ParseEdgeStatus(st); !ok {
			t.Errorf("ParseEdgeStatus(%q) failed", st)
		}
	}
}
