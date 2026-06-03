package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manoIvans/manomesh/internal/domain"
)

// Repos pros 3 tipos de token de auth (verificação de email, reset
// de senha, refresh). Mantidos no mesmo arquivo porque compartilham
// schema (token_hash + expires_at + used_at) e o número de métodos por
// repo é pequeno.

// ============================================================
// EmailVerificationTokenRepository
// ============================================================

type EmailVerificationTokenRepository struct {
	db *pgxpool.Pool
}

func NewEmailVerificationTokenRepository(db *pgxpool.Pool) *EmailVerificationTokenRepository {
	return &EmailVerificationTokenRepository{db: db}
}

// Create insere um novo token de verificação. Caller é responsável
// por gerar o token cru (em auth/tokens.go) e passar o hash.
// expiresAt define quanto tempo o token vale (default 24h).
func (r *EmailVerificationTokenRepository) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`
	if _, err := r.db.Exec(ctx, q, userID, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("insert verification token: %w", err)
	}
	return nil
}

// Consume valida o token e marca como usado (atomicamente). Devolve
// o userID se sucesso. Sentinels:
//
//	ErrInvalidToken — hash não existe OU já foi consumido
//	ErrExpiredToken — existe mas expires_at < NOW
//
// UPDATE com WHERE used_at IS NULL faz a transição "não usado → usado"
// atômica (sem race entre o SELECT e o UPDATE de outra request).
func (r *EmailVerificationTokenRepository) Consume(ctx context.Context, tokenHash string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx (consume verify): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		userID    int64
		expiresAt time.Time
		usedAt    *time.Time
	)
	const lookupQ = `
		SELECT user_id, expires_at, used_at
		  FROM email_verification_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`
	if err := tx.QueryRow(ctx, lookupQ, tokenHash).Scan(&userID, &expiresAt, &usedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.ErrInvalidToken
		}
		return 0, fmt.Errorf("lookup verify token: %w", err)
	}
	if usedAt != nil {
		return 0, domain.ErrInvalidToken
	}
	if time.Now().After(expiresAt) {
		return 0, domain.ErrExpiredToken
	}

	if _, err := tx.Exec(ctx,
		`UPDATE email_verification_tokens SET used_at = NOW() WHERE token_hash = $1`,
		tokenHash,
	); err != nil {
		return 0, fmt.Errorf("mark verify used: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit consume verify: %w", err)
	}
	return userID, nil
}

// ============================================================
// PasswordResetTokenRepository
// ============================================================

type PasswordResetTokenRepository struct {
	db *pgxpool.Pool
}

func NewPasswordResetTokenRepository(db *pgxpool.Pool) *PasswordResetTokenRepository {
	return &PasswordResetTokenRepository{db: db}
}

func (r *PasswordResetTokenRepository) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`
	if _, err := r.db.Exec(ctx, q, userID, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("insert reset token: %w", err)
	}
	return nil
}

// Consume: mesma semântica do verify. Devolve userID + erros sentinel.
func (r *PasswordResetTokenRepository) Consume(ctx context.Context, tokenHash string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx (consume reset): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		userID    int64
		expiresAt time.Time
		usedAt    *time.Time
	)
	const lookupQ = `
		SELECT user_id, expires_at, used_at
		  FROM password_reset_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`
	if err := tx.QueryRow(ctx, lookupQ, tokenHash).Scan(&userID, &expiresAt, &usedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.ErrInvalidToken
		}
		return 0, fmt.Errorf("lookup reset token: %w", err)
	}
	if usedAt != nil {
		return 0, domain.ErrInvalidToken
	}
	if time.Now().After(expiresAt) {
		return 0, domain.ErrExpiredToken
	}

	if _, err := tx.Exec(ctx,
		`UPDATE password_reset_tokens SET used_at = NOW() WHERE token_hash = $1`,
		tokenHash,
	); err != nil {
		return 0, fmt.Errorf("mark reset used: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit consume reset: %w", err)
	}
	return userID, nil
}

// ============================================================
// RefreshTokenRepository (rotação estrita)
// ============================================================

type RefreshTokenRepository struct {
	db *pgxpool.Pool
}

func NewRefreshTokenRepository(db *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

// Create insere um refresh token novo (sem chain). Caller passa hash;
// devolve o ID inserido pra que rotações posteriores possam apontar
// `replaced_by_id` pra ele.
func (r *RefreshTokenRepository) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (int64, error) {
	const q = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id`
	var id int64
	if err := r.db.QueryRow(ctx, q, userID, tokenHash, expiresAt).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert refresh token: %w", err)
	}
	return id, nil
}

// Rotate aplica rotação estrita:
//  1. SELECT FOR UPDATE pelo hash; ErrInvalidToken se não existe.
//  2. Se já revogado E replaced_by_id != NULL → REUSO! Revoga TODOS
//     os tokens ativos do user e devolve ErrRefreshTokenReused.
//  3. Se expirado → revoga sozinho + ErrExpiredToken.
//  4. INSERT novo token; UPDATE antigo (revoked_at + replaced_by_id).
//
// Devolve (userID, newTokenID) pra que caller insira o JWT par.
//
// Tudo em transação — ROLLBACK em qualquer falha mantém o token antigo
// utilizável (preferível a deixar metade da rotação aplicada).
func (r *RefreshTokenRepository) Rotate(ctx context.Context, oldHash, newHash string, newExpiresAt time.Time) (userID, newID int64, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx (rotate): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		oldID            int64
		ownerID          int64
		oldExpiresAt     time.Time
		oldRevokedAt     *time.Time
		oldReplacedByID  *int64
	)
	const lookupQ = `
		SELECT id, user_id, expires_at, revoked_at, replaced_by_id
		  FROM refresh_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`
	if err := tx.QueryRow(ctx, lookupQ, oldHash).Scan(
		&oldID, &ownerID, &oldExpiresAt, &oldRevokedAt, &oldReplacedByID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, domain.ErrInvalidToken
		}
		return 0, 0, fmt.Errorf("lookup refresh token: %w", err)
	}

	// Detecta reuso: revogado E com sucessor já registrado.
	if oldRevokedAt != nil && oldReplacedByID != nil {
		// Revoga TUDO do user. Atomicamente, na mesma tx.
		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens
			    SET revoked_at = NOW()
			  WHERE user_id = $1 AND revoked_at IS NULL`,
			ownerID,
		); err != nil {
			return 0, 0, fmt.Errorf("revoke all on reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, 0, fmt.Errorf("commit reuse revoke: %w", err)
		}
		return 0, 0, domain.ErrRefreshTokenReused
	}

	// Revogado sem replaced_by_id = logout explícito anterior. Trata
	// como inválido (sem disparar pânico de reuso).
	if oldRevokedAt != nil {
		return 0, 0, domain.ErrInvalidToken
	}

	if time.Now().After(oldExpiresAt) {
		// Marca como revogado pra que o índice parcial idx_rt_active
		// reflita o estado real — best-effort, falha não bloqueia.
		_, _ = tx.Exec(ctx,
			`UPDATE refresh_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`,
			oldID,
		)
		_ = tx.Commit(ctx)
		return 0, 0, domain.ErrExpiredToken
	}

	// Insere o novo token PRIMEIRO — replaced_by_id do antigo aponta
	// pra ele e é FK NOT NULL no instante do UPDATE.
	const insertNewQ = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id`
	if err := tx.QueryRow(ctx, insertNewQ, ownerID, newHash, newExpiresAt).Scan(&newID); err != nil {
		return 0, 0, fmt.Errorf("insert new refresh: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens
		    SET revoked_at = NOW(), replaced_by_id = $1
		  WHERE id = $2`,
		newID, oldID,
	); err != nil {
		return 0, 0, fmt.Errorf("mark old rotated: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit rotate: %w", err)
	}
	return ownerID, newID, nil
}

// Revoke marca um refresh token como revogado (logout explícito).
// Idempotente: revogar 2x não é erro. Não vaza se o token existe
// (sempre retorna nil se a Exec roda; "0 rows affected" é OK).
func (r *RefreshTokenRepository) Revoke(ctx context.Context, tokenHash string) error {
	const q = `
		UPDATE refresh_tokens
		   SET revoked_at = NOW()
		 WHERE token_hash = $1 AND revoked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, tokenHash); err != nil {
		return fmt.Errorf("revoke refresh: %w", err)
	}
	return nil
}

// RevokeAllForUser apaga todas as sessões do user (used by reset
// password — se mudou a senha, derruba sessões antigas).
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID int64) error {
	const q = `
		UPDATE refresh_tokens
		   SET revoked_at = NOW()
		 WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := r.db.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("revoke all refresh: %w", err)
	}
	return nil
}
