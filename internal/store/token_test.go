package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// tokenStore is seeded with owner (user) and claude, codex (agents).
func tokenStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}, {ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func mustToken(t *testing.T, s *Store, actor, host, secret string) *model.Token {
	t.Helper()
	tok, err := s.CreateToken(t.Context(), TokenInput{Actor: actor, ClientHost: host, Secret: secret, CreatedBy: "owner"})
	if err != nil {
		t.Fatalf("CreateToken(%s, %s): %v", actor, host, err)
	}
	return tok
}

func tokenIDs(items []model.Token) []int64 {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

// clockAt fixes the store clock at *now, which the test moves.
func clockAt(s *Store, now *time.Time) { s.SetClock(func() time.Time { return *now }) }

func TestCreateToken(t *testing.T) {
	s, ctx := tokenStore(t)
	tok, err := s.CreateToken(ctx, TokenInput{Actor: "codex", ClientHost: "DESKTOP-RPLTFHM", Secret: "agb_first", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.ID < 1 || tok.Actor != "codex" || tok.ClientHost != "DESKTOP-RPLTFHM" || tok.CreatedBy != "owner" || tok.CreatedAt.IsZero() ||
		tok.LastUsedAt != nil || tok.RevokedAt != nil || tok.RevokedBy != nil {
		t.Errorf("token = %+v", tok)
	}
	second := mustToken(t, s, "codex", "laptop", "agb_second")
	if second.ID <= tok.ID {
		t.Errorf("ids = %d, %d: a new token must have a higher id", tok.ID, second.ID)
	}

	// Only the hash of the secret is stored.
	var hash string
	if err := s.db.QueryRow(`select token_hash from token where id = ?`, tok.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "agb_first" || hash != hashToken("agb_first") || len(hash) != 64 {
		t.Errorf("token_hash = %q, want the 64 hex characters of the SHA-256 of the secret", hash)
	}
	// The SHA-256 test vector for "abc".
	if got := hashToken("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("hashToken(abc) = %s", got)
	}
}

func TestCreateTokenNeedsDeclaredActors(t *testing.T) {
	s, ctx := tokenStore(t)
	if _, err := s.CreateToken(ctx, TokenInput{Actor: "ghost", ClientHost: "pc", Secret: "agb_x", CreatedBy: "owner"}); err == nil {
		t.Error("a token for an undeclared actor was accepted")
	}
	if _, err := s.CreateToken(ctx, TokenInput{Actor: "claude", ClientHost: "pc", Secret: "agb_x", CreatedBy: "ghost"}); err == nil {
		t.Error("a token approved by an undeclared actor was accepted")
	}
	var n int
	if err := s.db.QueryRow(`select count(*) from token`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tokens after the rejected writes = %d, %v", n, err)
	}
}

func TestCreateTokenRevokesTheActiveTokensOfTheSameActorAndHost(t *testing.T) {
	s, ctx := tokenStore(t)
	older := mustToken(t, s, "claude", "pc", "agb_older")
	otherHost := mustToken(t, s, "claude", "laptop", "agb_other-host")
	otherActor := mustToken(t, s, "codex", "pc", "agb_other-actor")

	// A token revoked earlier keeps the revocation it has.
	stale := mustToken(t, s, "claude", "phone", "agb_stale")
	revoked, err := s.RevokeToken(ctx, stale.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := s.CreateToken(ctx, TokenInput{Actor: "claude", ClientHost: "pc", Secret: "agb_fresh", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RevokedAt != nil {
		t.Errorf("the new token is revoked: %+v", fresh)
	}

	got, err := s.ListTokens(ctx, model.TokensAll)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]model.Token{}
	for _, tok := range got {
		byID[tok.ID] = tok
	}
	o := byID[older.ID]
	if o.RevokedAt == nil || o.RevokedBy == nil || *o.RevokedBy != "owner" || *o.RevokedAt != fresh.CreatedAt {
		t.Errorf("the older token of the same device = %+v, want revoked by the approver at the time of the new token (%v)", o, fresh.CreatedAt)
	}
	if byID[otherHost.ID].RevokedAt != nil || byID[otherActor.ID].RevokedAt != nil {
		t.Errorf("tokens of another device or actor were revoked: %+v %+v", byID[otherHost.ID], byID[otherActor.ID])
	}
	if !reflect.DeepEqual(byID[stale.ID], *revoked) {
		t.Errorf("a revoked token changed: %+v, was %+v", byID[stale.ID], *revoked)
	}

	// The old secret no longer authenticates; the new one does.
	if _, err := s.AuthenticateToken(ctx, "agb_older"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the revoked secret authenticated: %v", err)
	}
	if _, err := s.AuthenticateToken(ctx, "agb_fresh"); err != nil {
		t.Errorf("the new secret: %v", err)
	}
}

func TestCreateTokenIsOneTransaction(t *testing.T) {
	s, ctx := tokenStore(t)
	older := mustToken(t, s, "claude", "pc", "agb_same")
	// The same secret hashes to a row that already exists, so the insert fails
	// after the older token of the device was revoked; nothing may stay revoked.
	if _, err := s.CreateToken(ctx, TokenInput{Actor: "claude", ClientHost: "pc", Secret: "agb_same", CreatedBy: "owner"}); err == nil {
		t.Fatal("a second token with the same hash was accepted")
	}
	active, err := s.ListTokens(ctx, model.TokensActive)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tokenIDs(active), []int64{older.ID}) {
		t.Errorf("active tokens after the failed create = %v, want only %d", tokenIDs(active), older.ID)
	}
}

func TestAuthenticateToken(t *testing.T) {
	s, ctx := tokenStore(t)
	tok := mustToken(t, s, "codex", "pc", "agb_secret")
	got, err := s.AuthenticateToken(ctx, "agb_secret")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != tok.ID || got.Actor != "codex" || got.ClientHost != "pc" {
		t.Errorf("authenticated = %+v, want %+v", got, tok)
	}
	for _, secret := range []string{"", "agb_secre", "agb_secret ", "AGB_SECRET", "agb_other"} {
		if _, err := s.AuthenticateToken(ctx, secret); !errors.Is(err, ErrNotFound) {
			t.Errorf("AuthenticateToken(%q) err = %v, want ErrNotFound", secret, err)
		}
	}
	if _, err := s.RevokeToken(ctx, tok.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(ctx, "agb_secret"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked token authenticated: %v", err)
	}
}

func TestAuthenticateTokenWritesLastUsedAtOncePerMinute(t *testing.T) {
	s, ctx := tokenStore(t)
	now := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	tok := mustToken(t, s, "codex", "pc", "agb_secret")
	lastUsed := func() *model.Time {
		t.Helper()
		items, err := s.ListTokens(ctx, model.TokensAll)
		if err != nil || len(items) != 1 {
			t.Fatalf("tokens = %v, %v", items, err)
		}
		return items[0].LastUsedAt
	}
	use := func(at time.Time) {
		t.Helper()
		now = at
		if _, err := s.AuthenticateToken(ctx, "agb_secret"); err != nil {
			t.Fatal(err)
		}
	}
	if lastUsed() != nil {
		t.Fatal("a token never used has last_used_at")
	}

	start := now.Add(5 * time.Second)
	use(start)
	if got := lastUsed(); got == nil || *got != model.NewTime(start) {
		t.Fatalf("first use: last_used_at = %v, want %v", got, model.NewTime(start))
	}
	// Inside a minute of the stored value nothing is written.
	use(start.Add(30 * time.Second))
	use(start.Add(59*time.Second + 999*time.Millisecond))
	if got := lastUsed(); *got != model.NewTime(start) {
		t.Errorf("use within a minute: last_used_at = %v, want %v", got, model.NewTime(start))
	}
	// From 60 seconds on the stored value moves.
	next := start.Add(60 * time.Second)
	use(next)
	if got := lastUsed(); *got != model.NewTime(next) {
		t.Errorf("use after 60 seconds: last_used_at = %v, want %v", got, model.NewTime(next))
	}
	// A clock that went backwards writes nothing.
	use(next.Add(-time.Hour))
	if got := lastUsed(); *got != model.NewTime(next) {
		t.Errorf("use with an earlier clock: last_used_at = %v, want %v", got, model.NewTime(next))
	}
	if got, err := s.AuthenticateToken(ctx, "agb_secret"); err != nil || got.ID != tok.ID {
		t.Errorf("authenticate = %+v, %v", got, err)
	}
}

func TestListTokens(t *testing.T) {
	s, ctx := tokenStore(t)
	if items, err := s.ListTokens(ctx, model.TokensAll); err != nil || items == nil || len(items) != 0 {
		t.Errorf("an empty list must be empty, not nil: %#v, %v", items, err)
	}
	a := mustToken(t, s, "claude", "pc", "agb_a")
	b := mustToken(t, s, "codex", "pc", "agb_b")
	c := mustToken(t, s, "claude", "laptop", "agb_c")
	if _, err := s.RevokeToken(ctx, b.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	for filter, want := range map[model.TokenFilter][]int64{
		model.TokensActive:  {c.ID, a.ID},
		model.TokensRevoked: {b.ID},
		model.TokensAll:     {c.ID, b.ID, a.ID},
	} {
		items, err := s.ListTokens(ctx, filter)
		if err != nil || !reflect.DeepEqual(tokenIDs(items), want) {
			t.Errorf("ListTokens(%s) = %v, %v; want %v", filter, tokenIDs(items), err, want)
		}
	}
	if _, err := s.ListTokens(ctx, "bogus"); err == nil {
		t.Error("an unknown filter was accepted")
	}
}

func TestRevokeToken(t *testing.T) {
	s, ctx := tokenStore(t)
	tok := mustToken(t, s, "codex", "pc", "agb_secret")
	revoked, err := s.RevokeToken(ctx, tok.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.ID != tok.ID || revoked.RevokedAt == nil || revoked.RevokedBy == nil || *revoked.RevokedBy != "owner" || revoked.Actor != "codex" {
		t.Errorf("revoked = %+v", revoked)
	}

	// Revoking again changes nothing and still succeeds.
	again, err := s.RevokeToken(ctx, tok.ID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, revoked) {
		t.Errorf("a second revocation changed the token: %+v, was %+v", again, revoked)
	}

	// A token revokes itself in the name of its own actor.
	own := mustToken(t, s, "claude", "pc", "agb_own")
	self, err := s.RevokeToken(ctx, own.ID, "claude")
	if err != nil || self.RevokedBy == nil || *self.RevokedBy != "claude" {
		t.Errorf("self revocation = %+v, %v", self, err)
	}

	if _, err := s.RevokeToken(ctx, 9999, "owner"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing token err = %v", err)
	}
	if _, err := s.RevokeToken(ctx, mustToken(t, s, "claude", "other", "agb_x").ID, "ghost"); err == nil {
		t.Error("a revocation by an undeclared actor was accepted")
	}
}

func TestTokenWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := tokenStore(t)
	print := func() string {
		t.Helper()
		fp, err := s.FingerprintNow(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	before := print()
	tok := mustToken(t, s, "codex", "pc", "agb_secret")
	afterCreate := print()
	if afterCreate == before {
		t.Error("fingerprint unchanged after a token was created")
	}
	if print() != afterCreate {
		t.Error("fingerprint changed without a write")
	}

	// Recording the use of a token is not a change worth a backup.
	for range 3 {
		if _, err := s.AuthenticateToken(ctx, "agb_secret"); err != nil {
			t.Fatal(err)
		}
	}
	if got := print(); got != afterCreate {
		t.Errorf("fingerprint changed by last_used_at alone: %q -> %q", afterCreate, got)
	}

	if _, err := s.RevokeToken(ctx, tok.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	afterRevoke := print()
	if afterRevoke == afterCreate {
		t.Error("fingerprint unchanged after a token was revoked")
	}
	if _, err := s.RevokeToken(ctx, tok.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := print(); got != afterRevoke {
		t.Errorf("fingerprint changed by a repeated revocation: %q -> %q", afterRevoke, got)
	}
	mustToken(t, s, "codex", "pc", "agb_next")
	if print() == afterRevoke {
		t.Error("fingerprint unchanged after another token was created")
	}
}

// A snapshot is a complete copy of the schema version 5 data: tokens with
// their hashes, revocations and last use, and never a secret.
func TestSnapshotCarriesTokens(t *testing.T) {
	s, ctx := tokenStore(t)
	first := mustToken(t, s, "claude", "pc", "agb_first")
	mustToken(t, s, "codex", "pc", "agb_second")
	if _, err := s.AuthenticateToken(ctx, "agb_second"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeToken(ctx, first.ID, "owner"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dst))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, tokens, revoked, used, hashed int
	if err := db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select (select count(*) from token), (select count(*) from token where revoked_at is not null),
		(select count(*) from token where last_used_at is not null), (select count(*) from token where token_hash in (?, ?))`,
		hashToken("agb_first"), hashToken("agb_second")).Scan(&tokens, &revoked, &used, &hashed); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || tokens != 2 || revoked != 1 || used != 1 || hashed != 2 {
		t.Errorf("snapshot: version %d, tokens %d (revoked %d, used %d), rows with the hash of a secret %d", version, tokens, revoked, used, hashed)
	}
}
