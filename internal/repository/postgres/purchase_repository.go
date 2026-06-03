package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manoIvans/manomesh/internal/domain"
)

// PurchaseRepository encapsula a tabela `purchases` e o fluxo de
// checkout. Checkout não é INSERT simples — envolve múltiplas linhas
// em transação + clear do carrinho — daí o repo dedicado.
type PurchaseRepository struct {
	db *pgxpool.Pool
}

func NewPurchaseRepository(db *pgxpool.Pool) *PurchaseRepository {
	return &PurchaseRepository{db: db}
}

// purchaseIntent é o "que vai virar purchase" depois da expansão do
// carrinho. assets soltos viram 1 intent direto; packs viram N intents
// (1 por item, exceto os que o user já tem 'paid').
//
// fromPackID é nil pra compras diretas, e o ID do pack pra items
// originados de pack — frontend usa esse campo pra mostrar "comprado
// via pack X" na biblioteca.
type purchaseIntent struct {
	assetID     int64
	priceCents  int64 // snapshot já calculado (asset.price OU pack_price/N)
	fromPackID  *int64
}

// Checkout abre uma checkout_session com base no carrinho do user.
// Suporta carrinho misto (assets soltos + packs). Pra cada entrada:
//
//	asset solto → 1 purchase pending, snapshot = preço atual do asset
//	pack        → N purchases (1 por item) MINUS items que o user já tem
//	              'paid' em qualquer compra anterior; preço total = preço
//	              do pack (sempre), distribuído proporcionalmente entre
//	              os items efetivamente comprados (último absorve o resto
//	              da divisão pra somar exato).
//
// Tudo em transação:
//  1. SELECT FOR UPDATE cart_items + JOINs pra travar assets/packs.
//  2. Valida não-vazio + self-purchase em assets e packs.
//  3. Expande packs pra purchaseIntent[] descartando items já 'paid'.
//  4. Se TODOS os items de TODOS os packs já foram comprados E não há
//     assets soltos, ErrCartEmpty (nada a comprar) — refunda o user
//     mentalmente sem criar sessão fantasma.
//  5. INSERT session com TotalCents = soma dos prices reais cobrados
//     (assets soltos + pack_price cheio dos packs que ainda têm pelo
//     menos 1 item a comprar).
//  6. INSERT N purchases (status='pending', with from_pack_id quando
//     aplicável).
//  7. DELETE cart_items do user.
//
// ErrSelfPurchase / ErrAlreadyPurchased mantidos do flow original.
func (r *PurchaseRepository) Checkout(ctx context.Context, userID int64) (*domain.CheckoutSession, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx (checkout): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	intents, total, err := buildCheckoutIntents(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if len(intents) == 0 {
		return nil, domain.ErrCartEmpty
	}

	session := &domain.CheckoutSession{}
	const insertSessionQ = `
		INSERT INTO checkout_sessions (user_id, total_cents)
		VALUES ($1, $2)
		RETURNING id, user_id, status, provider, total_cents, created_at, expires_at`
	if err := tx.QueryRow(ctx, insertSessionQ, userID, total).Scan(
		&session.ID, &session.UserID, &session.Status,
		&session.Provider, &session.TotalCents,
		&session.CreatedAt, &session.ExpiresAt,
	); err != nil {
		return nil, fmt.Errorf("insert checkout session: %w", err)
	}

	const insertPurchaseQ = `
		INSERT INTO purchases (user_id, asset_id, price_cents_snapshot, status, checkout_session_id, from_pack_id)
		VALUES ($1, $2, $3, 'pending', $4, $5)
		RETURNING id`
	purchaseIDs := make([]int64, 0, len(intents))
	for _, in := range intents {
		var pid int64
		if err := tx.QueryRow(
			ctx, insertPurchaseQ,
			userID, in.assetID, in.priceCents, session.ID, in.fromPackID,
		).Scan(&pid); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
				return nil, domain.ErrAlreadyPurchased
			}
			return nil, fmt.Errorf("insert purchase: %w", err)
		}
		purchaseIDs = append(purchaseIDs, pid)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM cart_items WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("clear cart in checkout: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit checkout: %w", err)
	}

	session.PurchaseIDs = purchaseIDs
	return session, nil
}

// buildCheckoutIntents enumera o carrinho (assets soltos + packs),
// expande packs, dedupe contra purchases pagas e devolve as intents +
// total a cobrar. Faz todos os SELECTs FOR UPDATE pra serializar com
// outros checkouts concorrentes do mesmo user.
func buildCheckoutIntents(ctx context.Context, tx pgx.Tx, userID int64) ([]purchaseIntent, int64, error) {
	// 1) assets soltos no carrinho — lock pelo lado do asset.
	const assetQ = `
		SELECT a.id, a.price_cents, a.owner_id
		  FROM cart_items c
		  JOIN assets a ON a.id = c.asset_id
		 WHERE c.user_id = $1 AND c.asset_id IS NOT NULL
		 FOR UPDATE OF a`
	type cartAsset struct {
		id, price, owner int64
	}
	assetRows, err := tx.Query(ctx, assetQ, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("select cart assets for checkout: %w", err)
	}
	cartAssets := make([]cartAsset, 0)
	for assetRows.Next() {
		var ca cartAsset
		if err := assetRows.Scan(&ca.id, &ca.price, &ca.owner); err != nil {
			assetRows.Close()
			return nil, 0, fmt.Errorf("scan cart asset: %w", err)
		}
		if ca.owner == userID {
			assetRows.Close()
			return nil, 0, domain.ErrSelfPurchase
		}
		cartAssets = append(cartAssets, ca)
	}
	assetRows.Close()
	if err := assetRows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate cart assets: %w", err)
	}

	// 2) packs no carrinho — lock pelo lado do pack.
	const packQ = `
		SELECT p.id, p.price_cents, p.owner_id
		  FROM cart_items c
		  JOIN packs p ON p.id = c.pack_id
		 WHERE c.user_id = $1 AND c.pack_id IS NOT NULL
		 FOR UPDATE OF p`
	type cartPack struct {
		id, price, owner int64
	}
	packRows, err := tx.Query(ctx, packQ, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("select cart packs for checkout: %w", err)
	}
	cartPacks := make([]cartPack, 0)
	for packRows.Next() {
		var cp cartPack
		if err := packRows.Scan(&cp.id, &cp.price, &cp.owner); err != nil {
			packRows.Close()
			return nil, 0, fmt.Errorf("scan cart pack: %w", err)
		}
		if cp.owner == userID {
			packRows.Close()
			return nil, 0, domain.ErrSelfPurchase
		}
		cartPacks = append(cartPacks, cp)
	}
	packRows.Close()
	if err := packRows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate cart packs: %w", err)
	}

	// 3) Compras pagas anteriormente — usado pra dedupe (assets que
	// vêm via pack mas já são do user).
	owned, err := loadOwnedAssetIDs(ctx, tx, userID)
	if err != nil {
		return nil, 0, err
	}

	intents := make([]purchaseIntent, 0)
	var total int64

	for _, ca := range cartAssets {
		if _, has := owned[ca.id]; has {
			// Asset solto que já foi comprado — rejeita. Defesa: a UNIQUE
			// parcial em purchases pegaria depois (ErrAlreadyPurchased),
			// mas erramos cedo pra retornar antes de criar a sessão.
			return nil, 0, domain.ErrAlreadyPurchased
		}
		intents = append(intents, purchaseIntent{
			assetID:    ca.id,
			priceCents: ca.price,
		})
		total += ca.price
	}

	for _, cp := range cartPacks {
		items, err := loadPackItemIDs(ctx, tx, cp.id)
		if err != nil {
			return nil, 0, err
		}
		// Filtra items que o user já tem 'paid'.
		toBuy := make([]int64, 0, len(items))
		for _, aid := range items {
			if _, has := owned[aid]; !has {
				toBuy = append(toBuy, aid)
			}
		}
		if len(toBuy) == 0 {
			// Todos do pack já são do user — pack não gera nenhuma
			// purchase nova; também NÃO cobramos pelo pack (faria o
			// user pagar por nada).
			continue
		}
		// Preço cheio do pack, distribuído entre os items efetivamente
		// comprados. Último item absorve o resto da divisão pra que a
		// soma dos snapshots == pack price exato.
		base := cp.price / int64(len(toBuy))
		remainder := cp.price - base*int64(len(toBuy))
		for i, aid := range toBuy {
			snap := base
			if i == len(toBuy)-1 {
				snap += remainder
			}
			// Captura cp.id em variável local pra que &packID aponte pra
			// memória estável (não pro loop var compartilhada).
			packID := cp.id
			intents = append(intents, purchaseIntent{
				assetID:    aid,
				priceCents: snap,
				fromPackID: &packID,
			})
		}
		total += cp.price
	}

	return intents, total, nil
}

// loadOwnedAssetIDs devolve o set de asset IDs que o user já tem em
// purchases 'paid'. Usado pra dedupe na expansão dos packs.
func loadOwnedAssetIDs(ctx context.Context, tx pgx.Tx, userID int64) (map[int64]struct{}, error) {
	const q = `
		SELECT asset_id FROM purchases
		 WHERE user_id = $1 AND status = 'paid' AND asset_id IS NOT NULL`
	rows, err := tx.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("load owned assets: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan owned asset: %w", err)
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// loadPackItemIDs devolve os asset IDs que compõem o pack, ordenados
// por position. Usado na expansão do pack durante checkout.
func loadPackItemIDs(ctx context.Context, tx pgx.Tx, packID int64) ([]int64, error) {
	const q = `SELECT asset_id FROM pack_items WHERE pack_id = $1 ORDER BY position, asset_id`
	rows, err := tx.Query(ctx, q, packID)
	if err != nil {
		return nil, fmt.Errorf("load pack item ids: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan pack item id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FindSession devolve uma sessão pelo ID, conferindo ownership. Se a
// sessão é de outro usuário, devolvemos ErrSessionNotFound (não vazamos
// info). PurchaseIDs vêm via subquery.
func (r *PurchaseRepository) FindSession(ctx context.Context, sessionID string, userID int64) (*domain.CheckoutSession, error) {
	const q = `
		SELECT id, user_id, status, provider, provider_session_id,
		       total_cents, created_at, expires_at, paid_at
		  FROM checkout_sessions
		 WHERE id = $1 AND user_id = $2`

	s := &domain.CheckoutSession{}
	if err := r.db.QueryRow(ctx, q, sessionID, userID).Scan(
		&s.ID, &s.UserID, &s.Status, &s.Provider, &s.ProviderSessionID,
		&s.TotalCents, &s.CreatedAt, &s.ExpiresAt, &s.PaidAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrSessionNotFound
		}
		return nil, fmt.Errorf("select checkout session: %w", err)
	}

	ids, err := r.purchaseIDsForSession(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	s.PurchaseIDs = ids
	return s, nil
}

func (r *PurchaseRepository) purchaseIDsForSession(ctx context.Context, sessionID string) ([]int64, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM purchases WHERE checkout_session_id = $1 ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("select session purchases: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session purchase id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ConfirmSession marca a sessão como paga (mais as purchases vinculadas)
// e devolve o resultado. Idempotente: se a sessão já está 'paid', devolve
// (session, true=alreadyPaid, nil) sem disparar efeitos colaterais —
// importante porque webhooks reais podem disparar 2x.
//
// Erros:
//   - ErrSessionNotFound: id não existe ou não é do user
//   - ErrSessionExpired:  passou de expires_at
//   - ErrSessionInvalidState: estado != pending|paid (failed/expired no DB)
//
// O UPDATE de status usa WHERE status='pending' pra prevenir race
// (dois webhooks confirmando em paralelo): a primeira query move pra
// 'paid', a segunda devolve 0 rows e cai no branch idempotente.
func (r *PurchaseRepository) ConfirmSession(ctx context.Context, sessionID string, userID int64) (session *domain.CheckoutSession, alreadyPaid bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin tx (confirm): %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lê sessão sob lock pra serializar concorrência. FOR UPDATE evita
	// que dois confirms simultâneos vejam status='pending' antes de
	// nenhum dar UPDATE.
	const lockQ = `
		SELECT id, status, expires_at
		  FROM checkout_sessions
		 WHERE id = $1 AND user_id = $2
		 FOR UPDATE`
	var (
		id        string
		status    domain.CheckoutSessionStatus
		expiresAt time.Time
	)
	if err := tx.QueryRow(ctx, lockQ, sessionID, userID).Scan(&id, &status, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, domain.ErrSessionNotFound
		}
		return nil, false, fmt.Errorf("lock checkout session: %w", err)
	}

	switch status {
	case domain.SessionPaid:
		// Idempotente: sessão já estava paga. Devolve estado atual sem
		// disparar update. Caller NÃO deve refazer notificações.
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit idempotent confirm: %w", err)
		}
		s, ferr := r.FindSession(ctx, sessionID, userID)
		if ferr != nil {
			return nil, false, ferr
		}
		return s, true, nil
	case domain.SessionPending:
		// Caminho feliz — segue.
	default:
		// 'failed' ou 'expired' no DB — não confirma.
		return nil, false, domain.ErrSessionInvalidState
	}

	// Janela de tempo: depois do lock, ainda checa expiração contra
	// agora. Sessão pendente mas vencida vira ErrSessionExpired.
	if time.Now().After(expiresAt) {
		// Marca como expired pra que próximos GETs reflitam — best-effort
		// (não falha o request se o UPDATE falhar).
		if _, uerr := tx.Exec(ctx,
			`UPDATE checkout_sessions SET status = 'expired' WHERE id = $1 AND status = 'pending'`,
			sessionID,
		); uerr == nil {
			_ = tx.Commit(ctx)
		}
		return nil, false, domain.ErrSessionExpired
	}

	// Move sessão → paid (CAS via WHERE status='pending').
	const updateSessionQ = `
		UPDATE checkout_sessions
		   SET status = 'paid', paid_at = NOW()
		 WHERE id = $1 AND status = 'pending'`
	tag, err := tx.Exec(ctx, updateSessionQ, sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("update session to paid: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Outro caller bateu no UPDATE entre o SELECT FOR UPDATE e este
		// Exec. Loja-mente impossível com FOR UPDATE, mas defensivo.
		return nil, false, domain.ErrSessionInvalidState
	}

	// Move purchases → paid. UNIQUE parcial agora pode disparar: se a
	// purchase 'pending' vira 'paid' mas já existe outra 'paid' do mesmo
	// (user, asset) — significa que comprou via outra sessão entre Add
	// e Confirm. Mapeia pra ErrAlreadyPurchased.
	const updatePurchasesQ = `
		UPDATE purchases
		   SET status = 'paid'
		 WHERE checkout_session_id = $1 AND status = 'pending'`
	if _, err := tx.Exec(ctx, updatePurchasesQ, sessionID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return nil, false, domain.ErrAlreadyPurchased
		}
		return nil, false, fmt.Errorf("update purchases to paid: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit confirm: %w", err)
	}

	// Re-lê fora da tx pra devolver shape final consistente.
	s, ferr := r.FindSession(ctx, sessionID, userID)
	if ferr != nil {
		return nil, false, ferr
	}
	return s, false, nil
}

// ListByUser devolve as compras CONFIRMADAS do usuário (status='paid'),
// ordenadas da mais recente pra mais antiga. Pending/failed NÃO aparecem
// na library — só compra confirmada conta como "tenho esse asset".
//
// LEFT JOIN porque purchases.asset_id pode ser NULL após delete.
// As colunas do asset/user vêm como pointers nullable; quando
// asset_id da purchase for NULL, todas as colunas de a/u vêm nil
// e não montamos o Asset aninhado.
func (r *PurchaseRepository) ListByUser(ctx context.Context, userID int64) ([]*domain.Purchase, error) {
	// LEFT JOIN packs pra trazer FromPackTitle quando a compra veio
	// de um pack. Pack deletado depois = pk.id null + título null.
	const q = `
		SELECT p.id, p.user_id, p.status, p.price_cents_snapshot, p.purchased_at,
		       a.id, a.owner_id, a.title, a.description, a.tags,
		       a.price_cents, a.thumbnail_path, a.model_path,
		       a.created_at, a.updated_at,
		       u.display_name, u.username, u.avatar_path,
		       p.from_pack_id, pk.title
		  FROM purchases p
		  LEFT JOIN assets a  ON a.id  = p.asset_id
		  LEFT JOIN users  u  ON u.id  = a.owner_id
		  LEFT JOIN packs  pk ON pk.id = p.from_pack_id
		 WHERE p.user_id = $1 AND p.status = 'paid'
		 ORDER BY p.purchased_at DESC`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("select purchases: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Purchase, 0)
	for rows.Next() {
		p := &domain.Purchase{}
		// Asset/user columns nullable porque LEFT JOIN. *time.Time
		// e *string aceitam NULL via pgx scan diretamente.
		var (
			aID          *int64
			aOwnerID     *int64
			aTitle       *string
			aDescription *string
			aTags        []string
			aPriceCents  *int64
			aThumbPath   *string
			aModelPath   *string
			aCreatedAt   *time.Time
			aUpdatedAt   *time.Time
			uDisplayName *string
			uUsername    *string
			uAvatarPath  *string
		)
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.Status, &p.PriceCentsSnapshot, &p.PurchasedAt,
			&aID, &aOwnerID, &aTitle, &aDescription, &aTags,
			&aPriceCents, &aThumbPath, &aModelPath,
			&aCreatedAt, &aUpdatedAt,
			&uDisplayName, &uUsername, &uAvatarPath,
			&p.FromPackID, &p.FromPackTitle,
		); err != nil {
			return nil, fmt.Errorf("scan purchase row: %w", err)
		}

		if aID != nil {
			a := &domain.Asset{
				ID:               *aID,
				OwnerID:          ptrInt64(aOwnerID),
				Title:            ptrString(aTitle),
				Description:      ptrString(aDescription),
				Tags:             aTags,
				PriceCents:       ptrInt64(aPriceCents),
				ThumbnailPath:    ptrString(aThumbPath),
				ModelPath:        ptrString(aModelPath),
				AuthorName:       ptrString(uDisplayName),
				AuthorUsername:   ptrString(uUsername),
				AuthorAvatarPath: uAvatarPath,
			}
			if aCreatedAt != nil {
				a.CreatedAt = *aCreatedAt
			}
			if aUpdatedAt != nil {
				a.UpdatedAt = *aUpdatedAt
			}
			p.Asset = a
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate purchases: %w", err)
	}
	return out, nil
}

// SellerStats agrega métricas de vendas dos assets que pertencem
// ao `sellerID`. Tudo via JOIN purchases ↔ assets ↔ users(buyer).
//
// Múltiplas queries (totais, top asset, recent sales) em vez de
// uma única consolidada — Postgres lida bem com vários SELECTs
// independentes, e separar mantém cada SQL fácil de revisar/index.
//
// Ignoramos linhas com asset_id IS NULL (vendedor deletou o asset
// depois da compra): elas continuam no histórico do COMPRADOR mas
// não fazem sentido aqui — o vendedor já não tem o produto.
func (r *PurchaseRepository) SellerStats(ctx context.Context, sellerID int64, recentLimit int) (*domain.SellerStats, error) {
	stats := &domain.SellerStats{
		RecentSales: make([]*domain.SaleSummary, 0),
	}

	// 1) Totais agregados — count, sum, buyers distintos numa query só.
	// Filtra 'paid': vendas pendentes não contam pro dashboard.
	const aggQ = `
		SELECT
			COUNT(*) AS total_sales,
			COALESCE(SUM(p.price_cents_snapshot), 0) AS revenue,
			COUNT(DISTINCT p.user_id) AS unique_buyers
		  FROM purchases p
		  JOIN assets a ON a.id = p.asset_id
		 WHERE a.owner_id = $1 AND p.status = 'paid'`
	if err := r.db.QueryRow(ctx, aggQ, sellerID).Scan(
		&stats.TotalSales, &stats.RevenueCents, &stats.UniqueBuyers,
	); err != nil {
		return nil, fmt.Errorf("seller stats aggregate: %w", err)
	}

	// 2) Top asset (asset mais vendido). GROUP BY a.id + ORDER BY count
	// DESC + LIMIT 1. Se vendedor ainda não vendeu nada, query retorna
	// 0 linhas — tratamos com pgx.ErrNoRows e mantemos TopAsset = nil.
	const topQ = `
		SELECT a.id, a.title, COUNT(*) AS sales
		  FROM purchases p
		  JOIN assets a ON a.id = p.asset_id
		 WHERE a.owner_id = $1 AND p.status = 'paid'
		 GROUP BY a.id, a.title
		 ORDER BY sales DESC, a.id ASC
		 LIMIT 1`
	top := &domain.TopAsset{}
	if err := r.db.QueryRow(ctx, topQ, sellerID).Scan(
		&top.AssetID, &top.Title, &top.Sales,
	); err == nil {
		stats.TopAsset = top
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("seller stats top asset: %w", err)
	}

	// 3) Recent sales — últimas N. JOIN com users pra trazer o
	// nome do comprador (LEFT JOIN não precisa: user_id NOT NULL na
	// FK do schema).
	const recentQ = `
		SELECT p.id, a.id, a.title, u.username, u.display_name,
		       p.price_cents_snapshot, p.purchased_at
		  FROM purchases p
		  JOIN assets a ON a.id = p.asset_id
		  JOIN users u ON u.id = p.user_id
		 WHERE a.owner_id = $1 AND p.status = 'paid'
		 ORDER BY p.purchased_at DESC
		 LIMIT $2`
	rows, err := r.db.Query(ctx, recentQ, sellerID, recentLimit)
	if err != nil {
		return nil, fmt.Errorf("seller stats recent: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s := &domain.SaleSummary{}
		if err := rows.Scan(
			&s.PurchaseID, &s.AssetID, &s.AssetTitle,
			&s.BuyerUsername, &s.BuyerDisplayName,
			&s.PriceCentsSnapshot, &s.PurchasedAt,
		); err != nil {
			return nil, fmt.Errorf("seller stats scan recent: %w", err)
		}
		stats.RecentSales = append(stats.RecentSales, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seller stats iterate recent: %w", err)
	}

	return stats, nil
}

// IsPurchased: o user já comprou (e PAGOU) este asset? Usado pelo
// frontend pra esconder o botão "Adicionar ao carrinho" quando já é
// do usuário. Pending NÃO conta — usuário ainda pode tentar de novo
// se o pagamento falhou.
func (r *PurchaseRepository) IsPurchased(ctx context.Context, userID, assetID int64) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM purchases
			 WHERE user_id = $1 AND asset_id = $2 AND status = 'paid'
		)`
	var ok bool
	if err := r.db.QueryRow(ctx, q, userID, assetID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check purchase: %w", err)
	}
	return ok, nil
}

// ListPurchasedIDsByUser devolve o set de asset IDs comprados pelo
// usuário. Frontend hidrata em uma round-trip pra trocar "Adicionar
// ao carrinho" por "Já comprado" nos cards onde aplicável.
func (r *PurchaseRepository) ListPurchasedIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	// Filtramos asset_id IS NOT NULL: se o asset foi deletado pelo
	// vendedor, o ID não nos serve no front (não há card pra atualizar).
	// status='paid': pending não bloqueia botão "comprar" — pode ter
	// falhado, usuário precisa poder tentar de novo.
	const q = `
		SELECT asset_id FROM purchases
		 WHERE user_id = $1 AND asset_id IS NOT NULL AND status = 'paid'`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("select purchased ids: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan purchased id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate purchased ids: %w", err)
	}
	return ids, nil
}

// Helpers pra deref nullable do scan. Renomeados com prefixo `ptr`
// pra não conflitar com possíveis usos futuros do mesmo helper em
// outros repos.
func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
