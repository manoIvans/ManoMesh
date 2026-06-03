# ManoMesh

Marketplace de assets 3D com estética pixel-art / RPG retrô. Catálogo público, perfis com avatar, favoritos, carrinho + checkout em duas etapas (sessão `pending` → confirm `paid`, idempotente; pronto pra plugar Stripe/MercadoPago no lugar do stub), packs (vendedor agrupa N assets próprios num bundle com preço próprio), biblioteca de compras, avaliações com estrelas, notificações in-app, dashboard analítico do vendedor, viewer 3D interativo e filtros multi-facet.

> Repo no GitHub: `manoIvans/ManoMesh`. Go module: `github.com/manoIvans/manomesh`.

---

## Stack

- **Backend**: Go 1.25 · Gin · pgx/v5 · golang-jwt/jwt/v5 · bcrypt · `crypto/rand` + SHA-256 pra tokens de email/reset/refresh
- **Banco**: PostgreSQL 16
- **Frontend**: React 18 · Vite 6 · TypeScript · Tailwind v4
- **3D**: three.js · @react-three/fiber · @react-three/drei
- **Infra**: Docker Compose (Postgres + API), frontend roda local via Vite

---

## Features

### Usuário
- Cadastro com `username` único + `display_name` + email + senha
- **Verificação de email**: register dispara email com link (stub do mailer loga no stdout em dev); confirmar libera POST /assets. Banner persistente no header até confirmar, com botão "Reenviar"
- **Reset de senha por email**: `/forgot` → POST /forgot-password (sempre 202, anti-enumeration) → email com link → `/reset?token=...` → atualiza senha + revoga todas as sessões
- **Refresh tokens com rotação estrita**: access token JWT (1h) + refresh opaque (30d); cada `POST /refresh` revoga o token antigo e emite par novo; detecção de reuso revoga todas as sessões do user
- Perfil editável (`/perfil/me`): display name, bio, avatar (upload PNG/JPG/WEBP até 2 MiB)
- Perfil público em `/u/:username` listando os assets do usuário
- Auto-refresh em 401: cliente HTTP tenta rotacionar o token antes de logar fora; só dispara logout global se o refresh também falha

### Catálogo
- Galeria pública com cards pixel-art
- **FilterBar** (barra horizontal de dropdowns): tags multi-select, faixa de preço (Min/Max), ordenação (6 opções)
- Busca por título com debounce 200ms
- URL state pra todo filtro (`?tag=...&q=...&sort=...&min=...&max=...`) — tudo linkável
- Sessões "Em alta" (top vendidos) e "Top criadores" na home quando sem filtro
- Recomendações "Você também pode gostar" no detalhe (tag overlap via PostgreSQL)
- Diretório `/criadores` com todos os usuários

### Vendedor
- Publicar asset (`/dashboard`): título, descrição, tags, preço, thumbnail, modelo 3D (.glb/.gltf)
- Editar/deletar pelo `OwnerPanel` no `/asset/:id`
- Trocar thumbnail/modelo independentemente dos metadados (rotas multipart separadas)
- "Minha Loja" (`/my-store`): grid dos próprios assets
- **Packs** (bundles): agrupar 2-50 assets próprios num único item à venda com preço próprio (desconto vs soma individual). Thumbnail opcional (fallback pro 1º item). Items são SEMPRE assets do mesmo dono — validado no repo via `SELECT FOR UPDATE` que previne race com mudança de ownership. Carrinho misto (assets + packs), checkout expande pack em N purchases com **dedupe** dos assets que o comprador já tem 'paid' (cobra preço cheio mesmo assim — política "compra só os faltantes, preço cheio"). Cada purchase recebe `from_pack_id` pra rastrear origem; biblioteca agrupa por pack. AssetDetail tem badge "também faz parte do pack X" via lookup inverso. Vendedor edita/exclui packs direto na `Minha Loja`.
- **Dashboard analítico** em `/my-store`: total de vendas, receita, compradores únicos, asset mais vendido e tabela de últimas vendas (com link pro perfil do comprador)

### Comércio
- Favoritos (`/favoritos`) — coração no card e detalhe, optimistic update
- Carrinho misto (`/carrinho`) — assets soltos + packs na mesma lista. Total/checkout em duas etapas: clica "Finalizar" → cria sessão `pending` + redirect pra `/checkout/:id` (página stub que simula Stripe/MP) → "Pagar" confirma idempotente e leva pra `/library`
- Catálogo de packs em `/packs` (paginado, 24/página) + detalhe em `/pack/:id` com grid dos assets inclusos
- Vendedor cria pack em `/dashboard/packs/new`: form multi-select dos próprios assets + título/preço + capa opcional
- Biblioteca (`/library`) — histórico de compras com link de download do modelo

### Avaliações
- Estrelas 1-5 + comentário (somente quem comprou pode avaliar; um review por usuário por asset)
- Média + total visíveis no `AssetCard` (galeria/listas) e no `AssetDetail` (header)
- Sort "Melhor avaliados" na galeria
- Form de criar/editar/excluir o próprio review no `AssetDetail`

### Notificações
- Bell no header com badge de não-lidas (polling 60s + refetch quando volta à aba)
- Página `/notificacoes` com lista das últimas 50 + botão "marcar todas como lidas"
- Tipos: `asset_sold` (vendedor), `asset_reviewed` (vendedor) e `purchase_confirmation` (comprador recebe um aviso por asset comprado)
- Disparadas no `ConfirmCheckoutSession` (não no Checkout — só na confirmação do pagamento) e no `ReviewCreate`. Best-effort: falha não bloqueia fluxo principal; idempotência do confirm garante que retries de webhook não duplicam

### Viewer 3D
- `/asset/:id` mostra o modelo via three.js/R3F
- Controles: fullscreen (F), wireframe (W), reset câmera (R) — overlay + atalhos de teclado

---

## Estrutura

```
├─ cmd/api/                  # main.go do servidor Go
├─ internal/
│  ├─ auth/                  # JWT + token manager
│  ├─ domain/                # entities + sentinel errors (User, Asset, Purchase, TagCount…)
│  ├─ migrate/               # runner que aplica migrations no boot da API
│  ├─ repository/postgres/   # 1 repo por agregado (User, Asset, Favorite, Cart, Purchase, Review, Notification)
│  ├─ storage/               # LocalStorage pra uploads (thumbnail/model/avatar)
│  └─ transport/http/
│     ├─ handler/            # Asset, User, Cart, Favorite, Auth, Review, Notification, Health
│     ├─ middleware/         # CORS, RequireAuth
│     ├─ server.go           # router + DI
│     └─ static.go           # /uploads/* com Cache-Control immutable
├─ frontend/
│  └─ src/
│     ├─ api/client.ts       # fetch wrapper + tipos
│     ├─ auth/               # AuthContext, AuthInterceptor (401 global), ProtectedRoute
│     ├─ cart/               # CartContext (cart + purchased ids)
│     ├─ favorites/          # FavoritesContext
│     ├─ notifications/      # NotificationsContext (polling 60s + visibility)
│     ├─ components/         # AssetCard, Avatar, FavoriteButton, CartButton, Toast, ModelViewer, StarRating, LineSkeleton…
│     ├─ lib/                # format.ts, money.ts, tags.ts (helpers compartilhados)
│     ├─ styles/pixel.ts     # PIXEL_BTN, PIXEL_INPUT, ASSET_GRID_CLASSES
│     ├─ pages/              # 1 arquivo por rota
│     └─ App.tsx             # Routes
├─ migrations/               # 016 arquivos SQL, ordem importante
├─ uploads/                  # bind mount: thumbnails/, models/, avatars/
├─ Dockerfile                # multi-stage build da API
├─ docker-compose.yml        # Postgres + API
└─ .env.example
```

---

## Como rodar

### Pré-requisitos
- Docker Desktop
- Node 20+ (pro frontend)

### 1. Subir backend + Postgres

```bash
cp .env.example .env
# edita .env e define JWT_SECRET (obrigatório)
docker compose up --build -d
```

A API tem um **migrator embutido** (`internal/migrate`) que roda no boot, lê `migrations/*.sql` (bind mount em `/app/migrations`) e aplica o que não está em `schema_migrations`. Adicionar uma migration nova é só dropar o arquivo na pasta e `docker compose restart api` — sem `docker exec ... psql` manual. Idempotente com o initdb do Postgres (que roda só na 1ª init do volume).

Pra resetar do ZERO (apaga DB e uploads):
```bash
docker compose down -v
docker compose up --build -d
```

### 2. Subir frontend

```bash
cd frontend
cp .env.example .env  # define VITE_API_BASE_URL=http://localhost:8080
npm install
npm run dev
```

Abre em `http://localhost:5173`.

### 3. Validações

```bash
# Backend — build + tests
go build ./...
go test ./...

# Frontend
cd frontend && npx tsc --noEmit
cd frontend && npm run build  # produção
```

---

## Testes

**Backend**: 120 testes de handler + 20 nos pacotes `auth` e `migrate` = **140 testes Go**. Handler tests em [internal/transport/http/handler/*_test.go](internal/transport/http/handler/) cobrem caminhos felizes + mapeamento de erro sentinel (404/403/409/410/413/415) + side-effects importantes (cleanup de arquivos no delete/rollback, hooks de notificação no confirm de pagamento, idempotência do confirm). Não tocam no banco — usam mocks das interfaces (`fakeUserRepo`, `fakeAssetRepo`, `fakeNotificationSink`, `fakePurchaseRepo`, `fakeFileStorage`, etc.) definidos em [testhelpers_test.go](internal/transport/http/handler/testhelpers_test.go).

Padrão dos mocks: cada interface tem um struct fake com campos `XxxFn func(...)`. Se o teste **não configura** uma função e ela é chamada, panic — esquecimento de mock vira falha óbvia em vez de nil pointer no fundo. Notification sink captura chamadas (`SoldAssetsCalls`, `BuyerPurchasesCalls`, `ForReviewCalls`) pra que testes verifiquem hooks dispararam (ou não) com args corretos. Tests de multipart usam helpers `doMultipart` / `doMultipartWithRepeats` (último suporta campos repetidos como `tags=a&tags=b`).

Cobertura por handler:
- **Auth** (19+): register sucesso + conflitos email/username + validação; login com bcrypt real + mensagem anti-enumeration. Forgot/Reset (anti-enumeration 202 sem revelar emails; revoga sessões no reset). Verify email (success + invalid + expired). Refresh (sucesso devolve par novo, inválido/expirado → 401, **reuso → revoga TODAS as sessões + 401 com mensagem distinta**). Logout idempotente.
- **Asset** (24+): GetByID, Update, Delete (com cleanup), Similar (cap), Trending, Tags, MyAssets; List dual-mode (legado vs `?page=`); **Create multipart** (sucesso + faltando thumb + rollback quando model falha + tipo inválido + tags vazias + DB falha → ambos arquivos limpos); **ReplaceThumbnail/ReplaceModel** (sucesso + remove antigo, DB falha → rollback do novo, 403/404, campo faltando)
- **User** (17+): GetMe, GetByUsername (regression check: PublicUser não vaza email), UpdateMe, List (legado + `?page=` + cap de `page_size`); **UploadAvatar** (sucesso + remove antigo, primeira vez sem remoção, campo faltando, tipo inválido, DB falha → rollback); **DeleteAvatar** (remove antigo, idempotente sem avatar prévio)
- **Cart + Checkout** (15+): Add asset (self-purchase), AddPack/RemovePack (404/409), Checkout cria sessão `pending` (não dispara notifs), GetCheckoutSession, ConfirmSession dispara notifs, **confirm idempotente NÃO refire notifs**, sessão expirada (410), List devolve shape misto `{assets, packs}`
- **Pack** (20): Create multipart (com/sem thumb, < 2 items, asset_ids inválido, ErrPackInvalidItems faz cleanup), GetByID, List paginado, MyPacks (filtra por JWT), ByAssetID (lookup inverso), Update (200/403/404/binding), Delete (cleanup do thumb, 403), ReplaceThumbnail (sucesso + rollback do novo se DB falha)
- **Favorite** (5): Add/Remove idempotentes, List/ListIDs
- **Review** (4 cases + 3 subtestes): Create exige compra, conflito UNIQUE, rating fora de 1-5
- **Notification** (3): List, UnreadCount (formato `{count}`), MarkAllRead

Pra rodar verboso: `go test ./internal/transport/http/handler/ -v`.

**Frontend**: 51 testes vitest em [frontend/src/**/__tests__/](frontend/src/) — helpers de `lib/`, `auth/tokenStorage`, validações. Roda com `npm test -- --run`.

Não cobertos por ora (escopo maior):
- Repository tests (precisam Postgres via testcontainers-go ou DSN de teste) — testes de handler usam mocks das interfaces, então o SQL real fica não-coberto

---

## Variáveis de ambiente

| Variável | Onde | Default | Notas |
|---|---|---|---|
| `POSTGRES_USER` | docker-compose | `postgres` | |
| `POSTGRES_PASSWORD` | docker-compose | `postgres` | |
| `POSTGRES_DB` | docker-compose | `lojinha_assets` | |
| `DATABASE_URL` | API | (computado) | DSN do pgx |
| `JWT_SECRET` | API | — | **obrigatório**; compose aborta se vazio |
| `JWT_TTL_HOURS` | API | `24` | TTL do access token (recomendado 1 em produção; refresh tokens cobrem o resto) |
| `FRONTEND_BASE_URL` | API | `http://localhost:5173` | usado pra montar links nos emails de verificação e reset |
| `APP_PORT` | API | `8080` | porta interna do container |
| `GIN_MODE` | API | `release` | `debug` pra logs verbosos |
| `UPLOAD_DIR` | API | `/app/uploads` | bind mount no host |
| `CORS_ALLOWED_ORIGINS` | API | `http://localhost:5173` | CSV |
| `VITE_API_BASE_URL` | frontend | — | **obrigatório**; ex: `http://localhost:8080` |

---

## API endpoints

Todas as rotas em `/api/v1/*`. Health check em `/ping`. Uploads servidos em `/uploads/{thumbnails,models,avatars}/{uuid}.{ext}`.

### Auth (público)
- `POST /register` — body `{email, password, username, display_name}` → `{access_token, refresh_token, user}`. Dispara email de verificação best-effort.
- `POST /login` — body `{email, password}` → `{access_token, refresh_token}`. Mesmo `401 credenciais inválidas` pra email inexistente e senha errada (anti-enumeration).
- `POST /refresh` — body `{refresh_token}` → `{access_token, refresh_token}`. **Rotação estrita**: revoga o antigo + insere novo na mesma transação. Reuso (mesmo refresh apresentado 2x após rotação) → revoga TODAS as sessões do user.
- `POST /logout` — body `{refresh_token}` → 204. Idempotente.
- `POST /forgot-password` — body `{email}` → **sempre 202** (não revela se o email existe). Se existir, envia link de reset.
- `POST /reset-password` — body `{token, new_password}`. Em sucesso, revoga todas as sessões do user.
- `POST /verify-email` — body `{token}`. Marca `users.email_verified_at`. Idempotente.

### Auth (protegido)
- `POST /resend-verification` — usa o JWT pra identificar; reenvia email de verificação. Sempre 202.

### Assets (público)
- `GET /assets` — lista catálogo (inclui `average_rating` + `review_count`). **Dual-mode**: sem query devolve array bare (compat com a Galeria atual); com `?page=N&page_size=M` (default 20, cap 100) devolve `{items, page, page_size, total}`.
- `GET /assets/:id` — detalhe
- `GET /assets/:id/similar?limit=N` — recomendações por tag overlap (default 4, cap 20)
- `GET /trending?limit=N` — top vendidos (default 8, cap 50)
- `GET /tags` — `[{tag, count}]`
- `GET /assets/:id/reviews` — lista de reviews do asset
- `GET /assets/:id/reviews/summary` — `{average, count}`

### Assets (protegido — `Authorization: Bearer <jwt>`)
- `POST /assets` — multipart: title, description, tags[], price_cents, thumbnail, model
- `PUT /assets/:id` — JSON metadados
- `PUT /assets/:id/thumbnail` — multipart com `thumbnail`
- `PUT /assets/:id/model` — multipart com `model`
- `DELETE /assets/:id`

### Users
- `GET /users` (público) — diretório, retorna `PublicUser[]` com `asset_count`. **Três modos** (prioridade): `?page=N` → envelope paginado `{items, page, page_size, total}` (default `page_size=20`, cap 100); senão `?limit=N` → array bare com até N (compat home "Top criadores", cap 100); senão array bare com todos. A página `/criadores` usa o modo paginado (`page_size=24`)
- `GET /users/:username` (público) — perfil público (sem email)
- `GET /users/me` (protegido) — perfil completo
- `PATCH /users/me` (protegido) — body `{display_name, bio}`
- `POST /users/me/avatar` (protegido) — multipart `avatar`
- `DELETE /users/me/avatar` (protegido)

### Favoritos (protegido)
- `POST /assets/:id/favorite` · `DELETE /assets/:id/favorite`
- `GET /my/favorites` — `Asset[]`
- `GET /my/favorite-ids` — `{ids: int[]}`

### Carrinho + compras (protegido)
- `POST /assets/:id/cart` · `DELETE /assets/:id/cart` — asset solto
- `POST /packs/:id/cart` · `DELETE /packs/:id/cart` — pack inteiro (vira N purchases no checkout)
- `GET /my/cart` — **shape misto**: `{assets: Asset[], packs: Pack[]}`
- `DELETE /my/cart` — esvazia tudo (assets+packs)
- `GET /my/cart-ids` — `{asset_ids: int[], pack_ids: int[]}` (hidrata `isInCart`/`isPackInCart` em N cards)
- `POST /my/cart/checkout` — abre uma **CheckoutSession** `pending` (provider stub), expande packs em N purchases (dedupe contra `status='paid'` do user; pack inteiro cobrado mesmo se user já tem alguns assets do pack, snapshot proporcional entre items efetivamente comprados), cria as `purchases` em `status='pending'` e esvazia o carrinho. Retorna a sessão completa (`{id, status, total_cents, expires_at, purchase_ids[]}`). NÃO dispara notificações ainda.
- `GET /my/checkout/sessions/:id` — detalhe da sessão (usado pelo stub do gateway no frontend).
- `POST /my/checkout/sessions/:id/confirm` — marca a sessão e suas purchases como `paid`. **Idempotente** (webhooks reais podem retry). Aqui dispara `asset_sold` + `purchase_confirmation`. Erros: `404` (não encontrada), `410` (expirada, >30min), `409` (estado inválido / asset comprado em outra sessão).
- `GET /my/library` — `Purchase[]` apenas `status='paid'` (pending/failed não aparecem; asset null quando vendedor deletou). Inclui `from_pack_id` + `from_pack_title` (via LEFT JOIN packs) pra que o frontend agrupe compras por pack
- `GET /my/library-ids` — `{ids: int[]}`
- `GET /my/store/stats` — dashboard: `{total_sales, revenue_cents, unique_buyers, top_asset, recent_sales}`

### Packs (bundles de assets)
- `GET /packs` (público) — listagem paginada `{items, page, page_size, total}` (default `page_size=20`, cap 100). Cada item inclui `items_count` (subquery), sem aninhar os assets pra evitar N+1
- `GET /packs/:id` (público) — detalhe com `items: Asset[]` aninhados (JOIN em pack_items+assets, ordenado por `position`)
- `GET /assets/:id/packs` (público) — lookup inverso: lista os packs que **contêm** este asset. Alimenta o badge "também faz parte do pack X" no AssetDetail
- `POST /packs` (protegido) — multipart: `title`, `description`, `price_cents`, `asset_ids[]` (2-50 únicos, todos do mesmo dono), `thumbnail` (opcional)
- `PUT /packs/:id` (protegido) — JSON `{title, description, price_cents, asset_ids[]}` (substitui items por completo)
- `PUT /packs/:id/thumbnail` (protegido) — multipart `thumbnail` (troca arquivo, faz rollback se DB falha)
- `DELETE /packs/:id` (protegido) — cleanup do thumb no disco; items CASCADE pelo schema
- `GET /my/packs` (protegido) — packs do JWT, sem paginação

### Reviews (protegido)
- `POST /assets/:id/reviews` — body `{rating 1-5, comment}`. Requer compra prévia (403 sem). Hook notifica dono.
- `PUT /reviews/:id` — edita próprio review
- `DELETE /reviews/:id` — exclui próprio

### Notificações (protegido)
- `GET /my/notifications` — últimas 50 com `asset_title` + dados do actor (LEFT JOIN, podem vir null)
- `GET /my/notifications/unread-count` — `{count}` (endpoint leve pra polling)
- `POST /my/notifications/read-all` — marca tudo como lido

### Códigos comuns
- `400` payload inválido
- `401` sem token ou token expirado (frontend faz auto-logout)
- `403` operação proibida (asset/pack alheio; email não verificado em POST /assets)
- `404` recurso inexistente
- `409` conflito (email/username já existe, auto-compra, asset já comprado, sessão em estado inválido)
- `410` sessão de checkout expirada / token de reset ou verify expirado
- `413` arquivo > limite
- `415` tipo de arquivo não suportado

---

## Migrations

Sequenciais, idempotentes (`IF NOT EXISTS`). Schema atual:

| # | Tabela / mudança |
|---|---|
| 001 | `users(id, email, password_hash, timestamps)` |
| 002 | `assets(id, owner_id→users, title, description, category, price_cents, timestamps)` |
| 003 | `assets` ganha `thumbnail_path`, `model_path` |
| 004 | `assets.category (str)` → `tags (text[])` + GIN index |
| 005 | `users` ganha `display_name`, `username` (unique, `^[a-z0-9_]{1,30}$`), `bio`, `avatar_path` |
| 006 | `favorites(user_id, asset_id, created_at)` PK composta + index inverso |
| 007 | `cart_items` + `purchases` (com `price_cents_snapshot` imutável; asset_id SET NULL no delete pra preservar histórico) |
| 008 | Índices em `created_at`/`added_at`/`purchased_at` pros ORDER BY DESC |
| 009 | `reviews(asset_id, user_id, rating 1-5, comment)` UNIQUE(asset_id, user_id), CHECK rating, index por (asset_id, created_at DESC) |
| 010 | `notifications(user_id, type, asset_id, actor_user_id, read_at)` com CHECK enum (`asset_sold`/`asset_reviewed`) + index parcial WHERE read_at IS NULL pra UnreadCount rápido |
| 011 | Amplia CHECK do `notifications.type` pra incluir `purchase_confirmation` (notifica o comprador além do vendedor) |
| 012 | `checkout_sessions(id UUID, user_id, status pending/paid/failed/expired, provider, total_cents, expires_at, paid_at)` + `purchases.status` + `purchases.checkout_session_id`; UNIQUE parcial em purchases passa a filtrar `status='paid'` (pendings concorrentes pro mesmo asset coexistem; só pagas bloqueiam) |
| 013 | `packs(id, owner_id, title, description, price_cents, thumbnail_path?, timestamps)` + `pack_items(pack_id, asset_id, position)` PK composta com CASCADE em ambas FKs. Validações de "min 2 items" e "todos do mesmo owner" ficam em app (PackRepository com `SELECT FOR UPDATE`) |
| 014 | `cart_items` aceita asset_id OU pack_id (XOR via CHECK), surrogate `id BIGSERIAL` substitui PK composta, UNIQUEs parciais por target. `purchases.from_pack_id` (FK SET NULL pra preservar histórico se pack for deletado) |
| 015 | Verificação de email + reset de senha. `users.email_verified_at` + tabelas `email_verification_tokens` e `password_reset_tokens` (token armazenado como SHA-256 hex; `used_at` em vez de DELETE pra trilha de auditoria) |
| 016 | `refresh_tokens` com rotação estrita: chain via `replaced_by_id` (auto-FK SET NULL) permite detectar reuso de token revogado; index parcial em `WHERE revoked_at IS NULL` acelera "revogar tudo do user" |

---

## Decisões técnicas

- **JWT por header `Authorization: Bearer`** (não cookie) → sem CSRF; CORS sem `Allow-Credentials`. Access token TTL curto; refresh tokens são opaque (não-JWT) armazenados como SHA-256 hex no DB — vazamento do DB não expõe tokens utilizáveis.
- **Refresh com rotação estrita + detecção de reuso**: cada `POST /refresh` gera par novo, revoga o antigo apontando `replaced_by_id`. Tentativa de reuso (token revogado COM sucessor) revoga toda a árvore de sessões do user e devolve 401 com mensagem distinta — frontend faz logout. Inspiração: OWASP refresh token best practices.
- **Mailer behind interface** (`mail.Mailer`): `StubMailer` em dev loga no stdout — copia/cola o link de verificação/reset sem precisar configurar provider. Trocar por Resend/SMTP em produção é só DI no boot.
- **Money em `int64` (centavos)** — float em dinheiro é receita pra bug.
- **Single-source-of-truth de filtros = URL** (search params) — back/forward funcionam, compartilhável.
- **Optimistic update** em favoritos e carrinho via Contexts dedicados (`FavoritesContext`, `CartContext`); fallback de rollback se backend rejeitar.
- **Multi-tag = OR; entre facets = AND** (padrão de marketplace).
- **Cache-Control immutable** em `/uploads` — arquivos UUID nunca mudam de conteúdo (troca gera UUID novo).
- **Gzip nas respostas JSON** (exceto `/uploads` que já é binário comprimido).
- **Bundle splitting** (react/router/three vendor chunks) — atualizar nosso código não invalida `three` (876 kB) no cache do browser.
- **Lazy load** das rotas via `React.lazy` + `Suspense`. `three` filtrado do `modulePreload` pra que home não baixe sem precisar.
- **Migrator embutido no boot da API** (`internal/migrate`): tracking em `schema_migrations`, idempotente com o initdb do Postgres. Adicionar migration nova é só dropar `.sql` em `migrations/` e restartar o container.
- **Notificações best-effort**: hooks pós-ConfirmSession e pós-Review.Create. Falha do INSERT em `notifications` é só logada — UX principal (compra/review) sempre completa. Quando virar feature crítica, mover pra dentro da mesma transação.
- **Checkout em duas etapas (provider-ready)**: `POST /cart/checkout` cria `CheckoutSession` `pending` + purchases `pending` e esvazia o carrinho; `POST /sessions/:id/confirm` é **idempotente** (CAS via `WHERE status='pending'` + lock `FOR UPDATE`) e dispara notificações apenas na primeira confirmação. Stripe/MercadoPago plugam aqui sem mexer em lógica de negócio: o stub do Checkout vira `redirect` externo, e o Confirm vira webhook do provedor.
- **Paginação dual-mode opt-in** nos listings públicos (`/assets`, `/users`): sem `?page=` retorna array bare (compat com clientes legados — a Galeria faz filtro client-side); com `?page=N&page_size=M` retorna envelope `{items, page, page_size, total}`. `page_size` cap em 100 contra abuse.

---

## Comandos úteis

```bash
# Logs da API
docker compose logs -f api

# Shell do Postgres
docker exec -it lojinha-postgres psql -U postgres -d lojinha_assets

# Listar tabelas
docker exec lojinha-postgres psql -U postgres -d lojinha_assets -c "\dt"

# Reset total
docker compose down -v && docker compose up --build -d

# Smoke test da API
curl -s http://localhost:8080/ping
curl -s http://localhost:8080/api/v1/assets | head
```

---

## Roadmap (não implementado)

- Tests de repository com Postgres real (testcontainers-go) — handler tests cobrem multipart (incluindo cleanup/rollback de arquivos) e sentinel mapping, mas SQL real fica no escopo de integration tests
- Integração de gateway real (Stripe/MercadoPago). O fluxo já tem a estrutura de sessão + confirm idempotente; basta trocar o stub do `Checkout.tsx` pelo redirect externo e o `ConfirmCheckoutSession` pelo webhook do provedor.
- Paginação no `Gallery` (hoje só os endpoints e o `Creators` usam; a galeria mantém filtro client-side)
- Rename do diretório do repo `Lojinha-dos-meus-assets/` (cosmético — o módulo Go já é `manomesh`)
