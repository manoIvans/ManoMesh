package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manoIvans/manomesh/internal/domain"
)

// PackRepository encapsula tabelas `packs` + `pack_items`. Mantém a
// invariante "todos os items pertencem ao owner do pack" — validada na
// mesma transação do Create/Update pra evitar TOCTOU (asset trocou de
// dono entre a checagem e o INSERT).
type PackRepository struct {
	db *pgxpool.Pool
}

func NewPackRepository(db *pgxpool.Pool) *PackRepository {
	return &PackRepository{db: db}
}

// packColumns: lista canônica das colunas da tabela `packs` usada nos
// SELECT/RETURNING. Mantém Scan + schema sincronizados.
const packColumns = "id, owner_id, title, description, price_cents, thumbnail_path, created_at, updated_at"

func scanPack(row pgx.Row, p *domain.Pack) error {
	return row.Scan(
		&p.ID, &p.OwnerID, &p.Title, &p.Description,
		&p.PriceCents, &p.ThumbnailPath,
		&p.CreatedAt, &p.UpdatedAt,
	)
}

// Create insere um novo pack com a lista inicial de assetIDs. Tudo em
// transação:
//  1. Valida quantidade (2..50) — defense-in-depth, handler também checa.
//  2. SELECT FOR UPDATE dos assets pra confirmar que TODOS pertencem
//     ao ownerID. Lock evita race com mudança de ownership.
//  3. INSERT pack.
//  4. INSERT pack_items batch.
//
// thumbnailPath é optional (string vazia → null no DB).
func (r *PackRepository) Create(
	ctx context.Context,
	ownerID int64,
	title, description string,
	priceCents int64,
	thumbnailPath string,
	assetIDs []int64,
) (*domain.Pack, error) {
	if err := validateItemsCount(assetIDs); err != nil {
		return nil, err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx (create pack): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := assertAllOwnedBy(ctx, tx, ownerID, assetIDs); err != nil {
		return nil, err
	}

	var thumbArg any = nil
	if thumbnailPath != "" {
		thumbArg = thumbnailPath
	}

	const insertQ = `
		INSERT INTO packs (owner_id, title, description, price_cents, thumbnail_path)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + packColumns

	p := &domain.Pack{}
	if err := scanPack(tx.QueryRow(ctx, insertQ, ownerID, title, description, priceCents, thumbArg), p); err != nil {
		return nil, fmt.Errorf("insert pack: %w", err)
	}

	if err := insertPackItems(ctx, tx, p.ID, assetIDs); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create pack: %w", err)
	}
	p.ItemsCount = int64(len(assetIDs))
	return p, nil
}

// FindByID devolve o pack + items aninhados (Asset[] com JOIN em assets +
// users pra trazer author). Items vêm ordenados por pack_items.position
// (ASC) com asset_id como desempate.
//
// ErrPackNotFound se id não existe.
func (r *PackRepository) FindByID(ctx context.Context, id int64) (*domain.Pack, error) {
	const headerQ = `
		SELECT p.id, p.owner_id, p.title, p.description,
		       p.price_cents, p.thumbnail_path,
		       p.created_at, p.updated_at,
		       u.display_name, u.username, u.avatar_path
		  FROM packs p
		  JOIN users u ON u.id = p.owner_id
		 WHERE p.id = $1`

	p := &domain.Pack{}
	err := r.db.QueryRow(ctx, headerQ, id).Scan(
		&p.ID, &p.OwnerID, &p.Title, &p.Description,
		&p.PriceCents, &p.ThumbnailPath,
		&p.CreatedAt, &p.UpdatedAt,
		&p.AuthorName, &p.AuthorUsername, &p.AuthorAvatarPath,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPackNotFound
		}
		return nil, fmt.Errorf("select pack header: %w", err)
	}

	items, err := r.loadItems(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	p.Items = items
	p.ItemsCount = int64(len(items))
	return p, nil
}

// List devolve TODOS os packs (paginado), ordenados do mais recente
// pro mais antigo. Items NÃO vêm aninhados — só count via subquery,
// pra que listagem com 50 packs não dispare 50 JOINs em pack_items.
func (r *PackRepository) List(ctx context.Context, page, pageSize int) ([]*domain.Pack, int64, error) {
	offset := (page - 1) * pageSize

	const listQ = `
		SELECT p.id, p.owner_id, p.title, p.description,
		       p.price_cents, p.thumbnail_path,
		       p.created_at, p.updated_at,
		       u.display_name, u.username, u.avatar_path,
		       (SELECT COUNT(*) FROM pack_items WHERE pack_id = p.id) AS items_count
		  FROM packs p
		  JOIN users u ON u.id = p.owner_id
		 ORDER BY p.created_at DESC
		 LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(ctx, listQ, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select packs: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Pack, 0, pageSize)
	for rows.Next() {
		p := &domain.Pack{}
		if err := rows.Scan(
			&p.ID, &p.OwnerID, &p.Title, &p.Description,
			&p.PriceCents, &p.ThumbnailPath,
			&p.CreatedAt, &p.UpdatedAt,
			&p.AuthorName, &p.AuthorUsername, &p.AuthorAvatarPath,
			&p.ItemsCount,
		); err != nil {
			return nil, 0, fmt.Errorf("scan pack row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate packs: %w", err)
	}

	var total int64
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM packs`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count packs: %w", err)
	}
	return out, total, nil
}

// ListByOwner: packs do vendedor logado. Mesmo shape (sem items
// aninhados, items_count via subquery). Ordenação por created_at DESC.
func (r *PackRepository) ListByOwner(ctx context.Context, ownerID int64) ([]*domain.Pack, error) {
	const q = `
		SELECT p.id, p.owner_id, p.title, p.description,
		       p.price_cents, p.thumbnail_path,
		       p.created_at, p.updated_at,
		       u.display_name, u.username, u.avatar_path,
		       (SELECT COUNT(*) FROM pack_items WHERE pack_id = p.id) AS items_count
		  FROM packs p
		  JOIN users u ON u.id = p.owner_id
		 WHERE p.owner_id = $1
		 ORDER BY p.created_at DESC`

	rows, err := r.db.Query(ctx, q, ownerID)
	if err != nil {
		return nil, fmt.Errorf("select packs by owner: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Pack, 0)
	for rows.Next() {
		p := &domain.Pack{}
		if err := rows.Scan(
			&p.ID, &p.OwnerID, &p.Title, &p.Description,
			&p.PriceCents, &p.ThumbnailPath,
			&p.CreatedAt, &p.UpdatedAt,
			&p.AuthorName, &p.AuthorUsername, &p.AuthorAvatarPath,
			&p.ItemsCount,
		); err != nil {
			return nil, fmt.Errorf("scan owner pack row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update edita metadados E items do pack (PUT semântico — substitui
// items inteiros). Ownership conferida ANTES do UPDATE pra diferenciar
// 404 (não existe) de 403 (não é seu).
//
// Em transação:
//  1. SELECT FOR UPDATE pack — lock + ownership check.
//  2. Valida itemsCount + ownership de todos os assets novos.
//  3. UPDATE pack (metadados).
//  4. DELETE pack_items + reinsert batch.
//
// thumbnailPath segue regra: string vazia mantém o existente; pra trocar
// ou limpar, usar PUT /packs/:id/thumbnail dedicado (igual asset).
func (r *PackRepository) Update(
	ctx context.Context,
	id, ownerID int64,
	title, description string,
	priceCents int64,
	assetIDs []int64,
) (*domain.Pack, error) {
	if err := validateItemsCount(assetIDs); err != nil {
		return nil, err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx (update pack): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var actualOwner int64
	err = tx.QueryRow(ctx, `SELECT owner_id FROM packs WHERE id = $1 FOR UPDATE`, id).Scan(&actualOwner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPackNotFound
		}
		return nil, fmt.Errorf("lock pack for update: %w", err)
	}
	if actualOwner != ownerID {
		return nil, domain.ErrPackForbidden
	}

	if err := assertAllOwnedBy(ctx, tx, ownerID, assetIDs); err != nil {
		return nil, err
	}

	const updateQ = `
		UPDATE packs
		   SET title = $1, description = $2, price_cents = $3, updated_at = NOW()
		 WHERE id = $4
		RETURNING ` + packColumns
	p := &domain.Pack{}
	if err := scanPack(tx.QueryRow(ctx, updateQ, title, description, priceCents, id), p); err != nil {
		return nil, fmt.Errorf("update pack: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM pack_items WHERE pack_id = $1`, id); err != nil {
		return nil, fmt.Errorf("delete old pack items: %w", err)
	}
	if err := insertPackItems(ctx, tx, id, assetIDs); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update pack: %w", err)
	}
	p.ItemsCount = int64(len(assetIDs))
	return p, nil
}

// Delete remove o pack (CASCADE limpa pack_items). Devolve thumbnail
// path pra cleanup no disco (igual padrão de asset). Distingue 404/403.
func (r *PackRepository) Delete(ctx context.Context, id, ownerID int64) (thumbnailPath string, err error) {
	var actualOwner int64
	var thumb *string
	const checkQ = `SELECT owner_id, thumbnail_path FROM packs WHERE id = $1`
	if err := r.db.QueryRow(ctx, checkQ, id).Scan(&actualOwner, &thumb); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrPackNotFound
		}
		return "", fmt.Errorf("check pack for delete: %w", err)
	}
	if actualOwner != ownerID {
		return "", domain.ErrPackForbidden
	}

	if _, err := r.db.Exec(ctx, `DELETE FROM packs WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("delete pack: %w", err)
	}
	if thumb != nil {
		return *thumb, nil
	}
	return "", nil
}

// UpdateThumbnail troca o arquivo físico em transação. Mesmo padrão dos
// avatares e thumbnails de asset: devolve path antigo pra cleanup no
// disco (ou "" quando não havia).
func (r *PackRepository) UpdateThumbnail(ctx context.Context, id, ownerID int64, newPath string) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx (pack thumb): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var actualOwner int64
	var existing *string
	const checkQ = `SELECT owner_id, thumbnail_path FROM packs WHERE id = $1 FOR UPDATE`
	if err := tx.QueryRow(ctx, checkQ, id).Scan(&actualOwner, &existing); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrPackNotFound
		}
		return "", fmt.Errorf("check pack for thumb update: %w", err)
	}
	if actualOwner != ownerID {
		return "", domain.ErrPackForbidden
	}

	if _, err := tx.Exec(ctx,
		`UPDATE packs SET thumbnail_path = $1, updated_at = NOW() WHERE id = $2`,
		newPath, id,
	); err != nil {
		return "", fmt.Errorf("update pack thumbnail: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit pack thumb update: %w", err)
	}
	if existing != nil {
		return *existing, nil
	}
	return "", nil
}

// ListByAssetID devolve os packs que CONTÊM o asset. Lookup inverso
// via pack_items — AssetDetail usa pra mostrar badge "também faz
// parte do pack X". Sem items aninhados (caller já está olhando
// o asset; só precisa do header + items_count).
//
// Ordenado por pack.created_at DESC (mais recente primeiro). Sem
// paginação porque um asset raramente está em mais de poucos packs.
func (r *PackRepository) ListByAssetID(ctx context.Context, assetID int64) ([]*domain.Pack, error) {
	const q = `
		SELECT p.id, p.owner_id, p.title, p.description,
		       p.price_cents, p.thumbnail_path,
		       p.created_at, p.updated_at,
		       u.display_name, u.username, u.avatar_path,
		       (SELECT COUNT(*) FROM pack_items WHERE pack_id = p.id) AS items_count
		  FROM pack_items pi
		  JOIN packs p ON p.id = pi.pack_id
		  JOIN users u ON u.id = p.owner_id
		 WHERE pi.asset_id = $1
		 ORDER BY p.created_at DESC`

	rows, err := r.db.Query(ctx, q, assetID)
	if err != nil {
		return nil, fmt.Errorf("select packs by asset: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Pack, 0)
	for rows.Next() {
		p := &domain.Pack{}
		if err := rows.Scan(
			&p.ID, &p.OwnerID, &p.Title, &p.Description,
			&p.PriceCents, &p.ThumbnailPath,
			&p.CreatedAt, &p.UpdatedAt,
			&p.AuthorName, &p.AuthorUsername, &p.AuthorAvatarPath,
			&p.ItemsCount,
		); err != nil {
			return nil, fmt.Errorf("scan asset-pack row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// loadItems busca os Asset[] que compõem um pack, ordenados por
// pack_items.position ASC, com author via JOIN em users.
func (r *PackRepository) loadItems(ctx context.Context, packID int64) ([]*domain.Asset, error) {
	const q = `
		SELECT a.id, a.owner_id, a.title, a.description, a.tags,
		       a.price_cents, a.thumbnail_path, a.model_path,
		       a.created_at, a.updated_at,
		       u.display_name, u.username, u.avatar_path
		  FROM pack_items pi
		  JOIN assets a ON a.id = pi.asset_id
		  JOIN users u ON u.id = a.owner_id
		 WHERE pi.pack_id = $1
		 ORDER BY pi.position ASC, pi.asset_id ASC`

	rows, err := r.db.Query(ctx, q, packID)
	if err != nil {
		return nil, fmt.Errorf("select pack items: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Asset, 0)
	for rows.Next() {
		a := &domain.Asset{}
		if err := rows.Scan(
			&a.ID, &a.OwnerID, &a.Title, &a.Description, &a.Tags,
			&a.PriceCents, &a.ThumbnailPath, &a.ModelPath,
			&a.CreatedAt, &a.UpdatedAt,
			&a.AuthorName, &a.AuthorUsername, &a.AuthorAvatarPath,
		); err != nil {
			return nil, fmt.Errorf("scan pack item: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// validateItemsCount confirma 2..50. Replicado no handler pra resposta
// 400 com mensagem amigável; aqui é defense-in-depth pra que repository
// rejeite mesmo se chamado direto (sem passar pelo handler).
func validateItemsCount(assetIDs []int64) error {
	n := len(assetIDs)
	if n < domain.MinPackItems || n > domain.MaxPackItems {
		return domain.ErrPackInvalidItems
	}
	// Defense: rejeita duplicatas (mesmo asset_id 2x no array). UNIQUE
	// constraint da PK pack_items pegaria, mas erro mais limpo aqui.
	seen := make(map[int64]struct{}, n)
	for _, id := range assetIDs {
		if _, dup := seen[id]; dup {
			return domain.ErrPackInvalidItems
		}
		seen[id] = struct{}{}
	}
	return nil
}

// assertAllOwnedBy confere que TODOS os assetIDs existem E pertencem
// ao ownerID. SELECT FOR UPDATE lockou as rows pra que ownership não
// mude entre check e INSERT. Erro genérico (ErrPackInvalidItems) pra
// não revelar quais IDs falharam.
func assertAllOwnedBy(ctx context.Context, tx pgx.Tx, ownerID int64, assetIDs []int64) error {
	const q = `
		SELECT COUNT(*) FROM assets
		 WHERE id = ANY($1) AND owner_id = $2
		 FOR UPDATE`
	var n int64
	if err := tx.QueryRow(ctx, q, assetIDs, ownerID).Scan(&n); err != nil {
		return fmt.Errorf("assert assets ownership: %w", err)
	}
	if int(n) != len(assetIDs) {
		return domain.ErrPackInvalidItems
	}
	return nil
}

// insertPackItems faz o batch INSERT mantendo position = índice da
// slice (0..N-1). pgx aceita batch via SendBatch — pra simplicidade
// usamos loop com 1 query por item; volumes baixos (max 50) tornam
// o overhead desprezível.
func insertPackItems(ctx context.Context, tx pgx.Tx, packID int64, assetIDs []int64) error {
	const q = `INSERT INTO pack_items (pack_id, asset_id, position) VALUES ($1, $2, $3)`
	for i, aid := range assetIDs {
		if _, err := tx.Exec(ctx, q, packID, aid, i); err != nil {
			return fmt.Errorf("insert pack item %d: %w", aid, err)
		}
	}
	return nil
}
