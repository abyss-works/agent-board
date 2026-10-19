package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// tokenUseInterval is how far last_used_at must lag behind the clock before a
// request writes it again. It keeps a busy token from writing on every request.
const tokenUseInterval = time.Minute

// TokenInput is a new actor token. Secret is the bearer string; only its
// SHA-256 is stored. CreatedBy is the user who approved the token.
type TokenInput struct {
	Actor      string
	ClientHost string
	Secret     string
	CreatedBy  string
}

// hashToken is the SHA-256 of a bearer string in hex, which the token table
// keeps in place of the string.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

const tokenCols = `t.id, t.actor_id, t.client_host, t.created_at, t.created_by, t.last_used_at, t.revoked_at, t.revoked_by`

func scanToken(sc rowScanner) (*model.Token, error) {
	var t model.Token
	var created string
	var used, revoked, revokedBy sql.NullString
	if err := sc.Scan(&t.ID, &t.Actor, &t.ClientHost, &created, &t.CreatedBy, &used, &revoked, &revokedBy); err != nil {
		return nil, err
	}
	var err error
	if t.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		src sql.NullString
		dst **model.Time
	}{{used, &t.LastUsedAt}, {revoked, &t.RevokedAt}} {
		if !f.src.Valid {
			continue
		}
		at, err := model.ParseTime(f.src.String)
		if err != nil {
			return nil, err
		}
		*f.dst = &at
	}
	if revokedBy.Valid {
		t.RevokedBy = &revokedBy.String
	}
	return &t, nil
}

func getToken(ctx context.Context, q querier, id int64) (*model.Token, error) {
	t, err := scanToken(q.QueryRowContext(ctx, `select `+tokenCols+` from token t where t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// CreateToken stores a token for an actor on a device and, in the same
// transaction, revokes the tokens the actor still has active on that device,
// in the name of the approver. A token has no activity record.
func (s *Store) CreateToken(ctx context.Context, in TokenInput) (*model.Token, error) {
	now := s.stamp().String()
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update token set revoked_at = ?, revoked_by = ? where actor_id = ? and client_host = ? and revoked_at is null`,
			now, in.CreatedBy, in.Actor, in.ClientHost); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `insert into token(actor_id, client_host, token_hash, created_at, created_by) values (?, ?, ?, ?, ?)`,
			in.Actor, in.ClientHost, hashToken(in.Secret), now, in.CreatedBy)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return getToken(ctx, s.db, id)
}

// AuthenticateToken finds the active token whose secret is given, or returns
// ErrNotFound for an unknown or revoked one. It records the use: last_used_at
// moves to the clock only when it is empty or at least a minute old. The
// token comes back as it was found, with LastUsedAt set to the clock when this
// call wrote it.
func (s *Store) AuthenticateToken(ctx context.Context, secret string) (*model.Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `select `+tokenCols+` from token t where t.token_hash = ? and t.revoked_at is null`, hashToken(secret)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := s.stamp()
	if t.LastUsedAt != nil && now.Time().Sub(t.LastUsedAt.Time()) < tokenUseInterval {
		return t, nil
	}
	stale := model.NewTime(now.Time().Add(-tokenUseInterval)).String()
	wrote := false
	err = s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `update token set last_used_at = ? where id = ? and revoked_at is null and (last_used_at is null or last_used_at <= ?)`,
			now.String(), t.ID, stale)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		wrote = n == 1
		return err
	})
	if err != nil {
		return nil, err
	}
	if wrote {
		t.LastUsedAt = &now
	}
	return t, nil
}

// ListTokens returns the tokens that match the filter, newest first.
func (s *Store) ListTokens(ctx context.Context, filter model.TokenFilter) ([]model.Token, error) {
	cond := ""
	switch filter {
	case model.TokensActive:
		cond = ` where t.revoked_at is null`
	case model.TokensRevoked:
		cond = ` where t.revoked_at is not null`
	case model.TokensAll:
	default:
		return nil, fmt.Errorf("unknown token filter %q", filter)
	}
	rows, err := s.db.QueryContext(ctx, `select `+tokenCols+` from token t`+cond+` order by t.id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *t)
	}
	return items, rows.Err()
}

// RevokeToken revokes a token in the name of an actor. A token that is already
// revoked is returned as it is, so repeating the call is harmless.
func (s *Store) RevokeToken(ctx context.Context, id int64, by string) (*model.Token, error) {
	now := s.stamp().String()
	err := s.write(ctx, func(tx *sql.Tx) error {
		t, err := getToken(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.RevokedAt != nil {
			return nil
		}
		_, err = tx.ExecContext(ctx, `update token set revoked_at = ?, revoked_by = ? where id = ?`, now, by, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return getToken(ctx, s.db, id)
}
