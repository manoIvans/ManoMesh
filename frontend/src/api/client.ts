import { tokenStorage } from '../auth/tokenStorage'

// BASE_URL vem da env (VITE_API_BASE_URL). Falha alta e cedo se a env
// não estiver configurada — melhor um erro óbvio no boot do que
// requests indo pra `undefined/api/v1/...` com mensagens confusas.
const BASE_URL = import.meta.env.VITE_API_BASE_URL
if (!BASE_URL) {
  throw new Error(
    'VITE_API_BASE_URL não definida. Crie um .env baseado no .env.example.',
  )
}

// ApiError carrega o status HTTP e o corpo da resposta para que o
// componente decida como reagir (ex: 401 → logout, 422 → mostrar
// mensagem de validação). Manter como classe permite `instanceof`
// no try/catch sem virar genérico.
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
  ) {
    super(`HTTP ${status}`)
  }
}

// Shape do retorno do backend Go. Mantidos em snake_case porque é
// como o JSON chega — converter pra camelCase aqui exigiria mapeamento
// em toda chamada. Conviver com snake_case é mais barato.
//
// author_name é opcional porque o backend só popula no List (com
// JOIN em users). Outros endpoints omitem o campo via omitempty.
//
// tags substituiu category (migration 004) — agora multi-valor.
// Sempre array não-nulo (default '{}' no schema).
// TagCount é o par tag→quantidade-de-assets devolvido por GET /tags.
// Usado pela galeria pra mostrar "fantasia (12)" no chip do filtro.
export type TagCount = {
  tag: string
  count: number
}

// User é o shape COMPLETO (com email) devolvido em GET /users/me e
// no objeto `user` da resposta de register. NUNCA chega pra terceiros
// — o backend só responde isso pra rota autenticada do próprio dono.
//
// avatar_path é opcional + nullable: o backend devolve `null` quando
// o usuário não tem avatar (e omite o campo se nem o User foi feito
// JOIN). Tratar `null` e `undefined` no front como "sem avatar".
export type User = {
  id: number
  email: string
  username: string
  display_name: string
  bio: string
  avatar_path?: string | null
  // email_verified_at vem populado quando o user confirma o email
  // via /verify?token=... — frontend usa pra esconder o banner e
  // o backend pra liberar POST /assets.
  email_verified_at?: string | null
  created_at: string
  updated_at: string
}

// PublicUser é o shape devolvido por GET /users/:username e
// GET /users. Sem email, sem updated_at — alimenta /u/:username
// e o diretório /criadores.
//
// asset_count vem populado em GET /users (lista) mas omitido em
// GET /users/:username (perfil individual) — o backend só calcula
// quando faz sentido pro caller.
export type PublicUser = {
  id: number
  username: string
  display_name: string
  bio: string
  avatar_path?: string | null
  asset_count?: number
  created_at: string
}

// Notification: shape devolvido por /api/v1/my/notifications.
// Campos *_user / asset_* vêm via LEFT JOIN — null quando o asset
// ou actor foi deletado depois.
export type Notification = {
  id: number
  user_id: number
  type: 'asset_sold' | 'asset_reviewed' | 'purchase_confirmation'
  asset_id?: number | null
  actor_user_id?: number | null
  read_at?: string | null
  created_at: string
  // Joined
  asset_title?: string | null
  actor_username?: string | null
  actor_display_name?: string | null
  actor_avatar_path?: string | null
}

// SellerStats: shape devolvido por GET /api/v1/my/store/stats.
// Alimenta o dashboard analítico no /my-store.
//
// top_asset é omitido quando vendedor ainda não vendeu nada (todos
// os agregados são 0 e top_asset = null/undefined).
export type SellerStats = {
  total_sales: number
  revenue_cents: number
  unique_buyers: number
  top_asset?: {
    asset_id: number
    title: string
    sales: number
  } | null
  recent_sales: SaleSummary[]
}

// SaleSummary: linha "achatada" de venda recente — JOIN purchase ↔
// asset ↔ buyer em uma row só, mais leve que aninhar Purchase + Asset.
export type SaleSummary = {
  purchase_id: number
  asset_id: number
  asset_title: string
  buyer_username: string
  buyer_display_name: string
  price_cents_snapshot: number
  purchased_at: string
}

// Review: avaliação de um asset por um comprador. rating 1-5,
// comment opcional. Quando devolvido em listagens, os campos
// author_* vêm populados via JOIN; nas respostas de POST/PUT
// (rota de escrita) podem vir undefined.
export type Review = {
  id: number
  asset_id: number
  user_id: number
  rating: number
  comment: string
  created_at: string
  updated_at: string
  author_username?: string
  author_display_name?: string
  author_avatar_path?: string | null
}

// ReviewSummary: agregado mostrado próximo ao título (estrelas +
// total). average = 0 quando count = 0 (frontend esconde).
export type ReviewSummary = {
  average: number
  count: number
}

// PurchaseStatus rastreia o estado do pagamento. 'pending' durante a
// sessão de checkout, 'paid' depois que o provedor confirma. Library
// só mostra 'paid' — pending/failed não contam.
export type PurchaseStatus = 'pending' | 'paid' | 'failed' | 'refunded'

// Purchase é o registro IMUTÁVEL de uma compra. price_cents_snapshot
// preserva o preço pago — não muda mesmo que o vendedor reajuste o
// preço do asset depois. `asset` é nullable: se o vendedor deletou,
// permanece o registro (sem o conteúdo).
export type Purchase = {
  id: number
  user_id: number
  status: PurchaseStatus
  price_cents_snapshot: number
  purchased_at: string
  asset?: Asset | null
  // from_pack_id + from_pack_title: marcados quando a purchase nasceu
  // de um pack. Title vem via LEFT JOIN no backend — null se o pack
  // foi deletado depois (FK SET NULL apaga from_pack_id também). Front
  // agrupa library por from_pack_id.
  from_pack_id?: number | null
  from_pack_title?: string | null
}

// CheckoutSessionStatus espelha o que o backend grava em
// checkout_sessions.status. 'pending' até confirmação, 'paid' após.
export type CheckoutSessionStatus = 'pending' | 'paid' | 'failed' | 'expired'

// CheckoutSession é a "tentativa de pagamento" — em provedor real
// equivale a um Stripe PaymentIntent ou MercadoPago Preference. No
// stub atual, é uma row interna; o frontend "redireciona" pra uma
// página simulando o provedor e chama o endpoint de confirm.
export type CheckoutSession = {
  id: string
  user_id: number
  status: CheckoutSessionStatus
  provider: string
  provider_session_id?: string | null
  total_cents: number
  created_at: string
  expires_at: string
  paid_at?: string | null
  purchase_ids?: number[]
}

// Pack agrupa N assets do mesmo vendedor num bundle com preço próprio.
// Shape devolvido por /packs (listagem) e /packs/:id (detalhe). Em
// listagem vem com `items_count` mas SEM `items` aninhado; em detalhe
// vem com `items: Asset[]`. Adapta no consumidor via opcionais.
export type Pack = {
  id: number
  owner_id: number
  title: string
  description: string
  price_cents: number
  thumbnail_path?: string | null
  // Items aninhados — populado em GetByID, ausente em List.
  items?: Asset[]
  // Author desnormalizado via JOIN (similar a Asset).
  author_name?: string
  author_username?: string
  author_avatar_path?: string | null
  items_count?: number
  created_at: string
  updated_at: string
}

// CartResponse: shape de `GET /my/cart`. Desde o carrinho misto
// (assets + packs), front sempre recebe os dois.
export type CartResponse = {
  assets: Asset[]
  packs: Pack[]
}

// CartIDsResponse: shape de `GET /my/cart-ids`. Sets separados pra
// hidratar `isInCart` e `isPackInCart` no CartContext.
export type CartIDsResponse = {
  asset_ids: number[]
  pack_ids: number[]
}

export type Asset = {
  id: number
  owner_id: number
  title: string
  description: string
  tags: string[]
  price_cents: number
  thumbnail_path: string
  model_path: string
  // Campos desnormalizados do autor (via JOIN no backend).
  // author_name = display_name; author_username permite link pra
  // /u/:username; author_avatar_path null quando o autor não tem
  // foto de perfil. Os 3 podem vir ausentes em respostas que não
  // fazem JOIN (ex: Create).
  author_name?: string
  author_username?: string
  author_avatar_path?: string | null
  // Agregado de reviews via subquery no backend. average_rating é
  // null quando o asset não tem nenhum review (SQL AVG → NULL), e
  // review_count é 0. Frontend trata `count > 0` como "tem reviews".
  average_rating?: number | null
  review_count?: number
  created_at: string
  updated_at: string
}

type RequestBody = FormData | Record<string, unknown> | undefined

// onUnauthorized é um callback global disparado QUANDO uma request
// recebe 401. Usado pelo AuthInterceptor pra forçar logout +
// redirect quando o JWT expira ou é revogado.
//
// Por que callback em vez de jogar o erro pro caller:
//   - 401 não-tratado em cada handler vira boilerplate massivo
//   - Logout é um efeito colateral global que TODA tela precisa
//     reagir igual (limpar contextos, redirecionar)
//   - Registrar uma vez no boot do App é a forma DRY
//
// Tipos exportados pra que o componente registrar o callback fique
// type-safe.
type UnauthorizedHandler = () => void
let onUnauthorized: UnauthorizedHandler | null = null

export function setOnUnauthorized(handler: UnauthorizedHandler | null) {
  onUnauthorized = handler
}

// Rotas onde 401 NÃO deve disparar logout global — são endpoints de
// login/register cuja falha (credencial errada) é responsabilidade
// da própria tela tratar. Sem este filtro, errar a senha derrubaria
// o usuário do login pro login com mensagem de "sessão expirada".
// /refresh entra aqui porque um 401 dele = refresh inválido, e o
// fluxo de fallback é o próprio refresh tentar (loop infinito).
const AUTH_PATHS_NO_LOGOUT = [
  '/api/v1/login',
  '/api/v1/register',
  '/api/v1/refresh',
  '/api/v1/forgot-password',
  '/api/v1/reset-password',
  '/api/v1/verify-email',
]

// refreshInflight: enquanto uma rotação está acontecendo, requests
// que receberem 401 vão ESPERAR essa Promise terminar antes de tentar
// de novo. Garante que múltiplas requests paralelas com access expirado
// disparem APENAS UM /refresh e reapliquem o mesmo token novo.
let refreshInflight: Promise<boolean> | null = null

// performRefresh: chama POST /refresh com o refresh atual. Em sucesso
// grava o novo par no tokenStorage e devolve true. Em falha, limpa
// os dois tokens (forçando logout no próximo `onUnauthorized`).
async function performRefresh(): Promise<boolean> {
  const refresh = tokenStorage.getRefresh()
  if (!refresh) return false
  try {
    const res = await fetch(`${BASE_URL}/api/v1/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refresh }),
    })
    if (!res.ok) {
      tokenStorage.clear()
      return false
    }
    const data = (await res.json()) as {
      access_token: string
      refresh_token: string
    }
    tokenStorage.setPair(data.access_token, data.refresh_token)
    return true
  } catch {
    tokenStorage.clear()
    return false
  }
}

// request é a única função que faz fetch. Toda chamada da app passa
// por aqui — é o lugar para auth, base URL, parsing e tratamento de
// erro. Mantém centralizado o fluxo de refresh-on-401: a primeira
// 401 dispara uma rotação (compartilhada entre todas as requests
// concorrentes) e tenta a request de novo UMA vez com o novo token.
async function request<T>(
  method: string,
  path: string,
  body?: RequestBody,
  _retry?: boolean,
): Promise<T> {
  const headers: Record<string, string> = {}

  // Injeta Authorization automaticamente quando há token. Lê do
  // localStorage a cada call — assim, logout durante uma sessão
  // não vaza o token para requests subsequentes.
  const token = tokenStorage.get()
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }

  let fetchBody: BodyInit | undefined
  if (body instanceof FormData) {
    // NUNCA setar Content-Type manualmente em multipart: o fetch
    // precisa escrever o boundary correto, e o navegador faz isso
    // sozinho quando o body é um FormData. Forçar quebra o upload.
    fetchBody = body
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    fetchBody = JSON.stringify(body)
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    body: fetchBody,
  })

  // 204 No Content (ex: DELETE) — não tem corpo, devolve undefined.
  if (res.status === 204) {
    return undefined as T
  }

  // Refresh-on-401: tenta rotacionar o token ANTES de dar logout.
  // Só tenta uma vez (_retry guard) e nunca em rotas auth (que não
  // têm Authorization de qualquer jeito).
  if (
    res.status === 401 &&
    !_retry &&
    tokenStorage.getRefresh() &&
    !AUTH_PATHS_NO_LOGOUT.includes(path)
  ) {
    if (!refreshInflight) refreshInflight = performRefresh()
    const ok = await refreshInflight
    refreshInflight = null
    if (ok) {
      return request<T>(method, path, body, true)
    }
  }

  const responseBody = await parseBody(res)

  if (!res.ok) {
    // 401 em endpoints autenticados → dispara o handler global ANTES
    // de jogar o erro. O handler faz logout + redirect; o caller
    // ainda recebe o erro pra que toasts/loaders limpem o estado.
    if (
      res.status === 401 &&
      onUnauthorized &&
      !AUTH_PATHS_NO_LOGOUT.includes(path)
    ) {
      onUnauthorized()
    }
    throw new ApiError(res.status, responseBody)
  }
  return responseBody as T
}

// parseBody decide entre JSON e texto pelo Content-Type. Defensivo:
// um 500 do servidor pode vir como HTML, e tentar res.json() nele
// crasha em vez de devolver a mensagem útil.
async function parseBody(res: Response): Promise<unknown> {
  const contentType = res.headers.get('Content-Type') ?? ''
  if (contentType.includes('application/json')) {
    return res.json()
  }
  return res.text()
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: RequestBody) => request<T>('POST', path, body),
  put: <T>(path: string, body?: RequestBody) => request<T>('PUT', path, body),
  patch: <T>(path: string, body?: RequestBody) => request<T>('PATCH', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
}

// fileUrl monta a URL absoluta do arquivo servido pela rota estática
// /uploads/* da API. Tag <img> e loaders do three.js usam isso direto.
export function fileUrl(relPath: string): string {
  return `${BASE_URL}/uploads/${relPath}`
}
