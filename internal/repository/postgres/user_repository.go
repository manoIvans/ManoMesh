package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manoIvans/manomesh/internal/domain"
)

// pgUniqueViolation é o SQLSTATE retornado pelo Postgres quando uma
// constraint UNIQUE é violada. Detectamos isso para distinguir
// "email já existe" / "username já existe" de erro genérico (500).
const pgUniqueViolation = "23505"

// userColumns centraliza a lista de colunas usadas nos SELECT/RETURNING
// pra que Scan e schema fiquem sincronizados. Adicionou coluna nova?
// Atualizar aqui e o compilador te avisa nos Scans.
const userColumns = "id, email, password_hash, username, display_name, bio, avatar_path, email_verified_at, created_at, updated_at"

// UserRepository encapsula o acesso à tabela `users`. O handler nunca
// fala com pgxpool diretamente — sempre via essa interface mental.
type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

// scanUser preenche um domain.User a partir de qualquer Row pgx.
// Mantém a ordem de colunas consistente com userColumns.
func scanUser(row pgx.Row, u *domain.User) error {
	return row.Scan(
		&u.ID, &u.Email, &u.PasswordHash,
		&u.Username, &u.DisplayName, &u.Bio, &u.AvatarPath,
		&u.EmailVerifiedAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
}

// Create insere um novo usuário. Retorna sentinels distintos para
// email/username já em uso pra que o handler possa apontar pro campo
// certo no form (UX).
func (r *UserRepository) Create(ctx context.Context, email, passwordHash, username, displayName string) (*domain.User, error) {
	const q = `
		INSERT INTO users (email, password_hash, username, display_name)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + userColumns

	u := &domain.User{}
	err := scanUser(r.db.QueryRow(ctx, q, email, passwordHash, username, displayName), u)
	if err != nil {
		return nil, mapUniqueViolation(err)
	}
	return u, nil
}

// mapUniqueViolation traduz violações de UNIQUE em sentinels
// específicos baseado no nome da constraint. Centralizar aqui
// evita repetir o type-assert + switch em cada caller.
func mapUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		switch pgErr.ConstraintName {
		case "users_username_key":
			return domain.ErrUsernameAlreadyExists
		case "users_email_key", "":
			// users_email_key é o nome auto-gerado pelo UNIQUE da coluna
			// email (Postgres usa "<table>_<column>_key" por padrão).
			// Empty string cobre versões antigas do pgx que não preenchem
			// ConstraintName em todos os casos.
			return domain.ErrEmailAlreadyExists
		default:
			// Constraint nova e desconhecida — propaga genérico pra que
			// vire 500 e a gente descubra no log em vez de mapear errado.
			return fmt.Errorf("unknown unique violation %q: %w", pgErr.ConstraintName, err)
		}
	}
	return fmt.Errorf("insert user: %w", err)
}

// FindByEmail é o caminho do login. ErrUserNotFound é traduzido para
// 401 (não 404) pra não enumerar emails cadastrados.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = $1`

	u := &domain.User{}
	if err := scanUser(r.db.QueryRow(ctx, q, email), u); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("select user by email: %w", err)
	}
	return u, nil
}

// FindByID é usado pelo GET /users/me (com o ID do JWT). Não revela
// existência do usuário a terceiros — só o próprio dono chama.
func (r *UserRepository) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	u := &domain.User{}
	if err := scanUser(r.db.QueryRow(ctx, q, id), u); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("select user by id: %w", err)
	}
	return u, nil
}

// ListWithAssetCount devolve TODOS os usuários (PublicUser) com o
// total de assets de cada um. Usado pelo diretório /criadores e
// pela seção "Top criadores" da home.
//
// LEFT JOIN com assets pra que usuários sem nenhum publicação ainda
// apareçam (com count=0). Os campos sensíveis (email, password_hash)
// não vêm — devolvemos só os campos de PublicUser + AssetCount.
//
// Ordenação: assets DESC, depois display_name pra desempate. Aplica
// LIMIT só quando `limit > 0`; limit <= 0 devolve todos.
func (r *UserRepository) ListWithAssetCount(ctx context.Context, limit int) ([]*domain.PublicUser, error) {
	// Query base. Quando limit > 0, anexamos LIMIT no final. Mais
	// simples que CASE no SQL — duas queries pequenas vs uma com
	// fmt.Sprintf que invita SQL injection se algum dia limit virar
	// string vinda de usuário.
	const baseQ = `
		SELECT u.id, u.username, u.display_name, u.bio, u.avatar_path,
		       u.created_at,
		       COUNT(a.id) AS asset_count
		  FROM users u
		  LEFT JOIN assets a ON a.owner_id = u.id
		 GROUP BY u.id
		 ORDER BY asset_count DESC, u.display_name ASC`

	var rows pgx.Rows
	var err error
	if limit > 0 {
		rows, err = r.db.Query(ctx, baseQ+" LIMIT $1", limit)
	} else {
		rows, err = r.db.Query(ctx, baseQ)
	}
	if err != nil {
		return nil, fmt.Errorf("select users with asset count: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.PublicUser, 0)
	for rows.Next() {
		u := &domain.PublicUser{}
		var count int64
		if err := rows.Scan(
			&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.AvatarPath,
			&u.CreatedAt, &count,
		); err != nil {
			return nil, fmt.Errorf("scan user with count: %w", err)
		}
		// Preenche AssetCount sempre (até quando 0) — diferenciar
		// "não populado" de "tem 0" não vale a complexidade aqui.
		u.AssetCount = &count
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return out, nil
}

// ListWithAssetCountPaginated devolve uma página da listagem do
// diretório /criadores. Mesma ordenação do ListWithAssetCount (assets
// DESC, depois display_name) + LIMIT/OFFSET. COUNT(*) sobre users
// devolve o total geral.
//
// page/pageSize já validados pelo handler. Offset = (page-1)*pageSize.
func (r *UserRepository) ListWithAssetCountPaginated(ctx context.Context, page, pageSize int) ([]*domain.PublicUser, int64, error) {
	offset := (page - 1) * pageSize

	const listQ = `
		SELECT u.id, u.username, u.display_name, u.bio, u.avatar_path,
		       u.created_at,
		       COUNT(a.id) AS asset_count
		  FROM users u
		  LEFT JOIN assets a ON a.owner_id = u.id
		 GROUP BY u.id
		 ORDER BY asset_count DESC, u.display_name ASC
		 LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(ctx, listQ, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select users page: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.PublicUser, 0, pageSize)
	for rows.Next() {
		u := &domain.PublicUser{}
		var count int64
		if err := rows.Scan(
			&u.ID, &u.Username, &u.DisplayName, &u.Bio, &u.AvatarPath,
			&u.CreatedAt, &count,
		); err != nil {
			return nil, 0, fmt.Errorf("scan user page row: %w", err)
		}
		u.AssetCount = &count
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users page: %w", err)
	}

	var total int64
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	return out, total, nil
}

// FindByUsername alimenta a página pública /u/:username. Username
// vem do path param já normalizado (lowercase) pelo handler.
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE username = $1`

	u := &domain.User{}
	if err := scanUser(r.db.QueryRow(ctx, q, username), u); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("select user by username: %w", err)
	}
	return u, nil
}

// UpdateProfile aplica edição de display_name e bio. Não toca em
// username, email, avatar nem password — cada um tem seu fluxo
// dedicado (UX e segurança diferentes).
func (r *UserRepository) UpdateProfile(ctx context.Context, id int64, displayName, bio string) (*domain.User, error) {
	const q = `
		UPDATE users
		   SET display_name = $1,
		       bio = $2,
		       updated_at = NOW()
		 WHERE id = $3
		RETURNING ` + userColumns

	u := &domain.User{}
	if err := scanUser(r.db.QueryRow(ctx, q, displayName, bio, id), u); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("update user profile: %w", err)
	}
	return u, nil
}

// IsEmailVerified: lookup barato pro middleware RequireVerifiedEmail.
// Single column scan; ErrUserNotFound se o id não existe (não deveria
// acontecer dentro do middleware, mas defensivo).
func (r *UserRepository) IsEmailVerified(ctx context.Context, id int64) (bool, error) {
	const q = `SELECT email_verified_at FROM users WHERE id = $1`
	var verifiedAt *string
	if err := r.db.QueryRow(ctx, q, id).Scan(&verifiedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrUserNotFound
		}
		return false, fmt.Errorf("check email verified: %w", err)
	}
	return verifiedAt != nil, nil
}

// UpdatePassword troca a senha do user. Usado pelo fluxo de reset
// (após o token ser consumido). NÃO toca em updated_at do user — a
// trilha de mudança fica em password_reset_tokens.used_at. Caller já
// validou que o token é válido antes de chamar.
func (r *UserRepository) UpdatePassword(ctx context.Context, id int64, newHash string) error {
	const q = `UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`
	tag, err := r.db.Exec(ctx, q, newHash, id)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// SetEmailVerified marca o user como verificado. Idempotente: marcar
// um user já verificado faz UPDATE redundante mas não falha.
func (r *UserRepository) SetEmailVerified(ctx context.Context, id int64) error {
	const q = `UPDATE users SET email_verified_at = COALESCE(email_verified_at, NOW()), updated_at = NOW() WHERE id = $1`
	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("set email verified: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// SetAvatar grava o novo caminho e devolve o ANTERIOR (pra que o
// handler remova o arquivo antigo do disco). Em uma única transação
// pra evitar race: dois POSTs concorrentes do mesmo usuário não vão
// deixar avatar órfão no banco.
//
// Se o usuário não tinha avatar antes, oldPath sai como string vazia.
func (r *UserRepository) SetAvatar(ctx context.Context, id int64, newPath string) (oldPath string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx (set avatar): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback após Commit é no-op

	var existing *string
	if err := tx.QueryRow(ctx, `SELECT avatar_path FROM users WHERE id = $1`, id).Scan(&existing); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrUserNotFound
		}
		return "", fmt.Errorf("select existing avatar: %w", err)
	}

	if _, err := tx.Exec(
		ctx,
		`UPDATE users SET avatar_path = $1, updated_at = NOW() WHERE id = $2`,
		newPath, id,
	); err != nil {
		return "", fmt.Errorf("update avatar: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit set avatar: %w", err)
	}

	if existing != nil {
		return *existing, nil
	}
	return "", nil
}

// ClearAvatar zera o avatar_path e devolve o anterior pra cleanup
// no disco. Mesmo padrão transacional do SetAvatar pra evitar drift.
func (r *UserRepository) ClearAvatar(ctx context.Context, id int64) (oldPath string, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx (clear avatar): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var existing *string
	if err := tx.QueryRow(ctx, `SELECT avatar_path FROM users WHERE id = $1`, id).Scan(&existing); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrUserNotFound
		}
		return "", fmt.Errorf("select existing avatar: %w", err)
	}

	if _, err := tx.Exec(
		ctx,
		`UPDATE users SET avatar_path = NULL, updated_at = NOW() WHERE id = $1`,
		id,
	); err != nil {
		return "", fmt.Errorf("clear avatar: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit clear avatar: %w", err)
	}

	if existing != nil {
		return *existing, nil
	}
	return "", nil
}
