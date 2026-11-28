package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

const tokenUseInterval = time.Minute

// EnsureActors upserts declared actors while keeping previously persisted rows.
func (s *Store) EnsureActors(ctx context.Context, actors []model.Actor) error {
	return s.withTx(ctx, func(tx pgx.Tx, _ *[]model.Event) error {
		now := s.now()
		for _, a := range actors {
			if _, err := tx.Exec(ctx,
				`INSERT INTO actor (id, kind, created_at)
				 VALUES ($1, $2, $3)
				 ON CONFLICT (id) DO UPDATE SET kind = EXCLUDED.kind`,
				a.ID, string(a.Kind), now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureAgentActor registers actorID as an agent if absent.
func (s *Store) EnsureAgentActor(ctx context.Context, actorID string) (bool, error) {
	if !model.ValidActorID(actorID) {
		return false, violation("actor", "invalid_value", "행위자 ID는 영문 소문자와 숫자 1~32자여야 합니다.")
	}
	var created bool
	err := s.withTx(ctx, func(tx pgx.Tx, _ *[]model.Event) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM actor WHERE id = $1`, actorID).Scan(&kind)
		switch {
		case err == nil:
			if kind == string(model.ActorUser) {
				return violation("actor", "invalid_value", "user 종류 행위자는 기기 승인 로그인을 쓸 수 없습니다.")
			}
			created = false
			return nil
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx,
				`INSERT INTO actor (id, kind, created_at) VALUES ($1, 'agent', $2)`,
				actorID, s.now(),
			); err != nil {
				return err
			}
			created = true
			return nil
		default:
			return err
		}
	})
	return created, err
}

// ListActors returns all persisted actors, users first, then agents.
func (s *Store) ListActors(ctx context.Context) ([]model.Actor, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, kind FROM actor
		 ORDER BY CASE WHEN kind = 'user' THEN 0 ELSE 1 END, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Actor{}
	for rows.Next() {
		var a model.Actor
		var kind string
		if err := rows.Scan(&a.ID, &kind); err != nil {
			return nil, err
		}
		a.Kind = model.ActorKind(kind)
		items = append(items, a)
	}
	return items, rows.Err()
}

func scanToken(row pgx.Row) (model.Token, error) {
	var t model.Token
	var createdAt time.Time
	var lastUsed, revokedAt *time.Time
	if err := row.Scan(
		&t.ID, &t.Actor, &t.ClientHost, &createdAt, &t.CreatedBy,
		&lastUsed, &revokedAt, &t.RevokedBy, &t.RevokedSession, &t.RevokedHost,
	); err != nil {
		return model.Token{}, err
	}
	t.CreatedAt = model.NewTime(createdAt)
	t.LastUsedAt = toModelTimePtr(lastUsed)
	t.RevokedAt = toModelTimePtr(revokedAt)
	return t, nil
}

const tokenCols = `id, actor_id, client_host, created_at, created_by, last_used_at, revoked_at, revoked_by, revoked_session, revoked_host`

// EnsureSeedToken idempotently registers an active seed token for local mode.
func (s *Store) EnsureSeedToken(ctx context.Context, in CreateTokenInput) (model.Token, error) {
	hash := sha256Hex(in.Secret)
	t, err := s.GetTokenByHash(ctx, hash)
	if err == nil && t.RevokedAt == nil {
		return t, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return model.Token{}, err
	}
	return s.CreateToken(ctx, Trace{Actor: in.CreatedBy}, in)
}

// CreateToken inserts a new token and revokes prior active tokens for the same actor and host.
func (s *Store) CreateToken(ctx context.Context, tr Trace, in CreateTokenInput) (model.Token, error) {
	tok, _, err := s.CreateTokenReplacing(ctx, tr, in)
	return tok, err
}

// CreateTokenReplacing inserts a new token and returns IDs of replaced active tokens.
func (s *Store) CreateTokenReplacing(ctx context.Context, tr Trace, in CreateTokenInput) (model.Token, []int64, error) {
	createdBy := in.CreatedBy
	if createdBy == "" {
		createdBy = tr.Actor
	}
	if createdBy == "" {
		createdBy = "system"
	}
	var tok model.Token
	var revokedIDs []int64
	err := s.withTx(ctx, func(tx pgx.Tx, _ *[]model.Event) error {
		now := s.now()
		rows, err := tx.Query(ctx,
			`SELECT id FROM token WHERE actor_id = $1 AND client_host = $2 AND revoked_at IS NULL`,
			in.Actor, in.ClientHost)
		if err != nil {
			return err
		}
		for rows.Next() {
			var rid int64
			if err := rows.Scan(&rid); err != nil {
				rows.Close()
				return err
			}
			revokedIDs = append(revokedIDs, rid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(revokedIDs) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE token
				 SET revoked_at = $1, revoked_by = $2, revoked_session = $3, revoked_host = $4
				 WHERE actor_id = $5 AND client_host = $6 AND revoked_at IS NULL`,
				now, createdBy, in.ApprovedSession, in.ApprovedHost, in.Actor, in.ClientHost,
			); err != nil {
				return err
			}
		}
		var err2 error
		tok, err2 = scanToken(tx.QueryRow(ctx,
			`INSERT INTO token (actor_id, client_host, token_hash, created_at, created_by)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING `+tokenCols,
			in.Actor, in.ClientHost, sha256Hex(in.Secret), now, createdBy,
		))
		return err2
	})
	return tok, revokedIDs, err
}

// GetTokenByHash looks up a token by its SHA-256 hex digest.
func (s *Store) GetTokenByHash(ctx context.Context, tokenHash string) (model.Token, error) {
	t, err := scanToken(s.pool.QueryRow(ctx,
		`SELECT `+tokenCols+` FROM token WHERE token_hash = $1`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Token{}, ErrNotFound
	}
	return t, err
}

// AuthenticateToken validates a bearer secret and throttles last_used_at updates.
func (s *Store) AuthenticateToken(ctx context.Context, secret string) (*model.Token, error) {
	t, err := scanToken(s.pool.QueryRow(ctx,
		`SELECT `+tokenCols+` FROM token WHERE token_hash = $1 AND revoked_at IS NULL`,
		sha256Hex(secret)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if t.LastUsedAt != nil && now.Sub(t.LastUsedAt.Time()) < tokenUseInterval {
		return &t, nil
	}
	stale := now.Add(-tokenUseInterval)
	tag, err := s.pool.Exec(ctx,
		`UPDATE token SET last_used_at = $1
		 WHERE id = $2 AND revoked_at IS NULL AND (last_used_at IS NULL OR last_used_at <= $3)`,
		now, t.ID, stale)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 {
		mt := model.NewTime(now)
		t.LastUsedAt = &mt
	}
	return &t, nil
}

// ListTokens returns tokens matching filter, newest first.
func (s *Store) ListTokens(ctx context.Context, filter model.TokenFilter) ([]model.Token, error) {
	cond := ""
	switch filter {
	case model.TokensActive:
		cond = ` WHERE revoked_at IS NULL`
	case model.TokensRevoked:
		cond = ` WHERE revoked_at IS NOT NULL`
	case model.TokensAll:
	default:
		return nil, fmt.Errorf("unknown token filter %q", filter)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+tokenCols+` FROM token`+cond+` ORDER BY id DESC`)
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
		items = append(items, t)
	}
	return items, rows.Err()
}

// RevokeToken marks a token revoked; revoking an already revoked token is a no-op.
func (s *Store) RevokeToken(ctx context.Context, tr Trace, tokenID int64) (model.Token, error) {
	var tok model.Token
	err := s.withTx(ctx, func(tx pgx.Tx, _ *[]model.Event) error {
		cur, err := scanToken(tx.QueryRow(ctx, `SELECT `+tokenCols+` FROM token WHERE id = $1 FOR UPDATE`, tokenID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.RevokedAt != nil {
			tok = cur
			return nil
		}
		tok, err = scanToken(tx.QueryRow(ctx,
			`UPDATE token
			 SET revoked_at = $1, revoked_by = $2, revoked_session = $3, revoked_host = $4
			 WHERE id = $5
			 RETURNING `+tokenCols,
			s.now(), tr.Actor, tr.SessionID, tr.ClientHost, tokenID,
		))
		return err
	})
	return tok, err
}
