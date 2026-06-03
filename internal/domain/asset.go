package domain

import (
	"errors"
	"time"
)

// ErrAssetNotFound é retornado pelo repositório quando o asset
// procurado não existe. O handler mapeia para 404.
var ErrAssetNotFound = errors.New("asset não encontrado")

// ErrAssetForbidden é retornado quando o usuário autenticado tenta
// editar/excluir um asset que não é dele. Mapeado para 403.
//
// Importante: é diferente de "não encontrado" — alguns produtos
// preferem devolver 404 nesse caso para não revelar que o ID existe.
// Para a Lojinha, optei por 403 explícito porque os IDs são públicos
// (a listagem retorna todos), então não há informação a esconder.
var ErrAssetForbidden = errors.New("operação não permitida neste asset")

// ErrSelfPurchase é retornado quando o usuário tenta adicionar um
// asset PRÓPRIO ao carrinho ou comprá-lo. Conceitualmente sem sentido
// (já é dele) e ainda cria registro de "compra" ruidoso. Mapeado
// pra 409 Conflict no handler.
var ErrSelfPurchase = errors.New("não pode comprar o próprio asset")

// ErrAlreadyPurchased é retornado quando o usuário tenta comprar um
// asset que já comprou antes. Bens digitais são únicos — comprar 2x
// não faz sentido. Mapeado pra 409 Conflict.
var ErrAlreadyPurchased = errors.New("asset já comprado anteriormente")

// ErrCartEmpty é retornado quando o checkout é chamado mas o
// carrinho não tem nada. Mapeado pra 400 Bad Request — não há nada
// a comprar.
var ErrCartEmpty = errors.New("carrinho vazio")

// ErrSessionNotFound: checkout session com ID inexistente (ou não
// pertence ao usuário). Mapeado pra 404 — não diferenciamos "outro
// dono" de "não existe" pra não vazar info.
var ErrSessionNotFound = errors.New("sessão de checkout não encontrada")

// ErrSessionExpired: sessão passou do prazo (30min). Mapeado pra 410
// Gone — o recurso existiu, mas não está mais utilizável; cliente
// precisa criar novo checkout.
var ErrSessionExpired = errors.New("sessão de checkout expirada")

// ErrSessionInvalidState: tentativa de confirmar uma sessão que não
// está pending (ex: já 'failed' ou 'expired'). 'paid' NÃO cai aqui —
// confirmar paid é idempotente e devolve sucesso. Mapeado pra 409.
var ErrSessionInvalidState = errors.New("sessão em estado inválido para esta operação")

// ErrPackNotFound: pack com ID inexistente. 404.
var ErrPackNotFound = errors.New("pack não encontrado")

// ErrPackForbidden: usuário tentou editar/deletar pack alheio. 403.
var ErrPackForbidden = errors.New("operação não permitida neste pack")

// ErrPackInvalidItems: cobre dois cenários:
//   - asset_ids fora dos limites (min 2, max 50)
//   - algum asset_id não pertence ao owner do pack (defense-in-depth)
//
// Mensagem genérica pra não revelar quais IDs falharam. 400.
var ErrPackInvalidItems = errors.New("items do pack inválidos")

// ErrReviewExists é retornado quando o usuário tenta criar um
// review pro asset que já avaliou. UI deveria oferecer EDITAR, não
// POST duplicado — mas o backend protege via UNIQUE constraint.
// Mapeado pra 409 Conflict.
var ErrReviewExists = errors.New("usuário já avaliou este asset")

// ErrReviewRequiresPurchase: só quem comprou pode avaliar. Regra
// aplicada no handler/repo via JOIN com purchases. Mapeado pra
// 403 Forbidden.
var ErrReviewRequiresPurchase = errors.New("é preciso comprar o asset para avaliar")

// ErrReviewNotFound: review com id inexistente. Mapeado pra 404.
var ErrReviewNotFound = errors.New("review não encontrado")

// ErrReviewForbidden: tentativa de editar/excluir review de outro
// usuário. Mapeado pra 403.
var ErrReviewForbidden = errors.New("este review não é seu")

// Asset representa um item à venda na Lojinha. Metadados + ponteiros
// para os arquivos físicos (thumbnail e modelo 3D). Os arquivos em si
// vivem em disco (ou eventualmente em object storage); aqui guardamos
// só o caminho relativo, devolvido pelo storage no momento do upload.
//
// PriceCents é inteiro (centavos) para evitar a clássica armadilha
// de float em dinheiro. Conversão para "R$ 12,34" é responsabilidade
// da camada de apresentação.
type Asset struct {
	ID          int64  `json:"id"`
	OwnerID     int64  `json:"owner_id"`
	Title       string `json:"title"`
	Description string `json:"description"`

	Tags          []string `json:"tags"`
	PriceCents    int64    `json:"price_cents"`
	ThumbnailPath string   `json:"thumbnail_path"`
	ModelPath     string   `json:"model_path"`

	// Campos de autor desnormalizados (vêm via JOIN no List/FindByID).
	// AuthorName é o display_name; AuthorUsername alimenta o link pra
	// /u/:username; AuthorAvatarPath é o caminho relativo do avatar
	// (pode ser nil — usuário sem avatar). omitempty pra que Create
	// (sem JOIN) não vaze campos zerados.
	AuthorName       string  `json:"author_name,omitempty"`
	AuthorUsername   string  `json:"author_username,omitempty"`
	AuthorAvatarPath *string `json:"author_avatar_path,omitempty"`

	// Agregado de reviews (subquery na listagem). AverageRating é
	// pointer pra que JSON distinga "sem reviews" (null) de "média 0".
	// ReviewCount = 0 também indica sem reviews — pode usar qualquer
	// um dos dois no front.
	AverageRating *float64 `json:"average_rating,omitempty"`
	ReviewCount   int64    `json:"review_count,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SellerStats agrega métricas do vendedor pro dashboard do
// /my-store. Tudo derivado de `purchases` + `assets`:
//   - TotalSales: número de compras dos assets do vendedor
//   - RevenueCents: soma de price_cents_snapshot (preserva preços
//     pagos historicamente, NÃO o preço atual do asset)
//   - UniqueBuyers: clientes distintos que compraram dele
//   - TopAssetID/Title/Sales: asset mais comprado (nil se ainda sem
//     nenhuma venda)
//   - RecentSales: últimas N compras com asset + comprador
type SellerStats struct {
	TotalSales   int64           `json:"total_sales"`
	RevenueCents int64           `json:"revenue_cents"`
	UniqueBuyers int64           `json:"unique_buyers"`
	TopAsset     *TopAsset       `json:"top_asset,omitempty"`
	RecentSales  []*SaleSummary  `json:"recent_sales"`
}

// TopAsset: asset mais vendido do vendedor. Separado pra que JSON
// fique nil quando vendedor ainda não vendeu nada.
type TopAsset struct {
	AssetID int64  `json:"asset_id"`
	Title   string `json:"title"`
	Sales   int64  `json:"sales"`
}

// SaleSummary: linha simplificada do histórico recente. Não usa
// Purchase porque queremos achatado em uma row (sem aninhar Asset
// e User) — mais leve no JSON e mais simples no SQL.
type SaleSummary struct {
	PurchaseID         int64     `json:"purchase_id"`
	AssetID            int64     `json:"asset_id"`
	AssetTitle         string    `json:"asset_title"`
	BuyerUsername      string    `json:"buyer_username"`
	BuyerDisplayName   string    `json:"buyer_display_name"`
	PriceCentsSnapshot int64     `json:"price_cents_snapshot"`
	PurchasedAt        time.Time `json:"purchased_at"`
}

// PurchaseStatus rastreia o estado do pagamento de uma compra.
// Estado vive em sincronia com o status da CheckoutSession-pai:
//
//	'pending'  — sessão criada, aguardando confirmação do provedor
//	'paid'     — provedor confirmou pagamento; compra é "real"
//	'failed'   — pagamento recusado (cartão, fraude, etc.)
//	'refunded' — futuro: estorno após paid (não implementado ainda)
//
// Library/SellerStats filtram só 'paid' — pending NÃO conta como
// compra do ponto de vista do comprador OU do vendedor.
type PurchaseStatus string

const (
	PurchasePending  PurchaseStatus = "pending"
	PurchasePaid     PurchaseStatus = "paid"
	PurchaseFailed   PurchaseStatus = "failed"
	PurchaseRefunded PurchaseStatus = "refunded"
)

// Purchase é o registro IMUTÁVEL de uma compra. price_cents_snapshot
// preserva o preço pago, ignorando reajustes futuros do dono. Asset
// pode ser nil (campo aninhado opcional preenchido via JOIN) se o
// vendedor deletou o asset depois da compra — o registro permanece
// mas perde o conteúdo associado.
//
// Status: 'pending' enquanto a sessão de pagamento não é confirmada;
// 'paid' depois que o webhook (ou stub de confirmação) marca como
// pago. Só purchases 'paid' aparecem na Library do comprador.
//
// Devolvido pelo GET /api/v1/my/library; o asset aninhado tem os
// mesmos campos do Asset normal pra que o frontend reuse o card.
type Purchase struct {
	ID                 int64          `json:"id"`
	UserID             int64          `json:"user_id"`
	Status             PurchaseStatus `json:"status"`
	PriceCentsSnapshot int64          `json:"price_cents_snapshot"`
	PurchasedAt        time.Time      `json:"purchased_at"`
	// Asset é nil se o vendedor deletou o asset depois da compra.
	// O front mostra "asset removido" quando isso acontece.
	Asset *Asset `json:"asset,omitempty"`
	// FromPackID/Title: marcado quando a purchase nasceu de um pack
	// no checkout. Title vem via LEFT JOIN — se o pack foi deletado
	// depois, FK SET NULL apaga FromPackID mas a purchase persiste.
	// Frontend usa pra agrupar a library: "via pack Medieval".
	FromPackID    *int64  `json:"from_pack_id,omitempty"`
	FromPackTitle *string `json:"from_pack_title,omitempty"`
}

// CheckoutSessionStatus rastreia o estado da tentativa de pagamento.
// Espelha o que provedores reais (Stripe, MercadoPago) usam: pending
// até o cliente confirmar, paid quando o webhook chega, failed/expired
// nos casos de rejeição/timeout.
type CheckoutSessionStatus string

const (
	SessionPending CheckoutSessionStatus = "pending"
	SessionPaid    CheckoutSessionStatus = "paid"
	SessionFailed  CheckoutSessionStatus = "failed"
	SessionExpired CheckoutSessionStatus = "expired"
)

// CheckoutSession é a "tentativa de pagamento" — em provedor real
// corresponderia a um Stripe PaymentIntent ou MercadoPago Preference.
// No modo stub, é só uma row interna que o frontend "redireciona" pra
// uma página de simulação e depois confirma manualmente.
//
// ID é UUID pra não vazar contagem; Provider/ProviderSessionID guardam
// referência ao provedor externo (stub agora, real depois). ExpiresAt
// é 30min após criação — depois disso a sessão não pode ser confirmada.
type CheckoutSession struct {
	ID                string                `json:"id"`
	UserID            int64                 `json:"user_id"`
	Status            CheckoutSessionStatus `json:"status"`
	Provider          string                `json:"provider"`
	ProviderSessionID *string               `json:"provider_session_id,omitempty"`
	TotalCents        int64                 `json:"total_cents"`
	CreatedAt         time.Time             `json:"created_at"`
	ExpiresAt         time.Time             `json:"expires_at"`
	PaidAt            *time.Time            `json:"paid_at,omitempty"`
	// PurchaseIDs aninhados pra que o frontend descubra o que foi
	// comprado sem GET extra. Populado em Create e Confirm.
	PurchaseIDs []int64 `json:"purchase_ids,omitempty"`
}

// Review representa uma avaliação de asset feita por um usuário
// que comprou. Rating 1-5 + comment opcional (string vazia OK).
// Campos de autor (Username, DisplayName, AvatarPath) são populados
// via JOIN quando devolvido em listagens — em respostas de POST/PUT
// vêm omitempty pra evitar payload poluído.
type Review struct {
	ID        int64     `json:"id"`
	AssetID   int64     `json:"asset_id"`
	UserID    int64     `json:"user_id"`
	Rating    int       `json:"rating"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Autor (vem via JOIN quando aplicável).
	AuthorUsername    string  `json:"author_username,omitempty"`
	AuthorDisplayName string  `json:"author_display_name,omitempty"`
	AuthorAvatarPath  *string `json:"author_avatar_path,omitempty"`
}

// ReviewSummary é o agregado mostrado próximo ao título do asset:
// média + total. Average é float (média de SMALLINT), Count é total
// de reviews. Quando Count = 0, Average vem 0 (frontend esconde).
type ReviewSummary struct {
	Average float64 `json:"average"`
	Count   int64   `json:"count"`
}

// NotificationType enumera os tipos suportados. Backend valida via
// CHECK no schema; aqui é só pra type-safety no Go.
type NotificationType string

const (
	NotificationAssetSold            NotificationType = "asset_sold"
	NotificationAssetReviewed        NotificationType = "asset_reviewed"
	NotificationPurchaseConfirmation NotificationType = "purchase_confirmation"
)

// Notification representa uma linha da tabela notifications.
// AssetID e ActorUserID são pointers porque a FK é ON DELETE SET NULL.
// Campos `_*` (AssetTitle, ActorUsername, etc.) vêm via JOIN quando
// devolvido em listagens — omitempty pra não vazar campos zerados.
type Notification struct {
	ID          int64            `json:"id"`
	UserID      int64            `json:"user_id"`
	Type        NotificationType `json:"type"`
	AssetID     *int64           `json:"asset_id,omitempty"`
	ActorUserID *int64           `json:"actor_user_id,omitempty"`
	ReadAt      *time.Time       `json:"read_at,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`

	// Campos do JOIN. Optional porque na Create não populamos.
	AssetTitle       *string `json:"asset_title,omitempty"`
	ActorUsername    *string `json:"actor_username,omitempty"`
	ActorDisplayName *string `json:"actor_display_name,omitempty"`
	ActorAvatarPath  *string `json:"actor_avatar_path,omitempty"`
}

// TagCount é o par tag→quantidade-de-assets usado pela tela de
// filtros: a galeria mostra "fantasia (12)" no chip. Devolvido pelo
// endpoint GET /api/v1/tags. Computado via unnest(tags) + GROUP BY
// no Postgres — não é só derivar de Asset.Tags em Go.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

// Pack agrupa N assets do MESMO vendedor num único item à venda. Tem
// preço próprio (geralmente desconto vs soma individual) e thumbnail
// opcional (quando nil, frontend cai pra thumb do 1º item).
//
// `Items` vem populado por FindByID/listing com JOIN em pack_items +
// assets. Pode vir vazio quando o caller só precisa do metadados (ex:
// listagens minimalistas que evitam N+1).
//
// Constraints de negócio (validadas no PackRepository, não no schema):
//   - Min 2 items por pack (1 item = só vende o asset direto)
//   - Max 50 items por pack (defense vs payloads ridículos)
//   - Todos os items pertencem ao mesmo owner do pack
type Pack struct {
	ID            int64    `json:"id"`
	OwnerID       int64    `json:"owner_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	PriceCents    int64    `json:"price_cents"`
	ThumbnailPath *string  `json:"thumbnail_path,omitempty"`
	// Items aninhados (Asset[] na ordem definida pelo dono via `position`).
	// Omitido em respostas de Create/Update (caller usa GetByID se quiser).
	Items []*Asset `json:"items,omitempty"`

	// Author desnormalizado via JOIN (similar a Asset). Omitido em respostas
	// que não fazem o JOIN.
	AuthorName       string  `json:"author_name,omitempty"`
	AuthorUsername   string  `json:"author_username,omitempty"`
	AuthorAvatarPath *string `json:"author_avatar_path,omitempty"`

	// ItemsCount: quantidade total de assets no pack, populada em
	// listagens que NÃO trazem Items aninhado. Listagem pode mostrar
	// "10 assets" sem carregar todos.
	ItemsCount int64 `json:"items_count,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Limites do pack — replicados no handler pra validação JSON binding
// e no repository pra defense-in-depth.
const (
	MinPackItems = 2
	MaxPackItems = 50
)
