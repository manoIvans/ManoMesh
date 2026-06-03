package http

import (
	"fmt"
	"net/http"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/manoIvans/manomesh/internal/auth"
	"github.com/manoIvans/manomesh/internal/mail"
	"github.com/manoIvans/manomesh/internal/repository/postgres"
	"github.com/manoIvans/manomesh/internal/storage"
	"github.com/manoIvans/manomesh/internal/transport/http/handler"
	"github.com/manoIvans/manomesh/internal/transport/http/middleware"
)

// NewRouter monta o roteador Gin com todas as rotas e middlewares
// da aplicação. As dependências (db, token manager, storage de
// arquivos, mailer, origens CORS, URL do frontend) entram por parâmetro
// — nada de singletons globais.
func NewRouter(
	db *pgxpool.Pool,
	tm *auth.TokenManager,
	files *storage.LocalStorage,
	mailer mail.Mailer,
	frontendBaseURL string,
	allowedOrigins []string,
) *gin.Engine {
	r := gin.Default() // logger + recovery já inclusos

	// CORS aplicado no engine (não em um grupo) para cobrir TODAS as
	// respostas — inclusive a rota estática /uploads, que o frontend
	// vai consumir via <img src> e fetch() do three.js. Precisa vir
	// antes de qualquer rota para que o middleware seja registrado
	// no topo da cadeia e responda preflight antes de RequireAuth.
	r.Use(middleware.CORS(allowedOrigins))

	// Gzip nas respostas JSON. /uploads é excluído porque PNG/JPG/WEBP/
	// GLB já vêm comprimidos pelo formato; recomprimir queima CPU sem
	// ganho. JSON do catálogo, ao contrário, comprime ~80% — vira
	// transferência muito menor pra galeria/biblioteca/etc.
	//
	// DefaultCompression equilibra CPU vs ratio. Acima disso (Best...)
	// o ganho marginal não vale o custo por request.
	r.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPaths([]string{"/uploads"}),
	))

	// MaxMultipartMemory controla quanto do upload fica em RAM antes
	// de transbordar para tempfile. 32 MiB é confortável para os
	// limites desta API (5 MiB thumb + 100 MiB modelo).
	r.MaxMultipartMemory = 32 << 20

	// Rota estática para os arquivos enviados. O frontend usa
	// `${API_BASE}/uploads/${asset.thumbnail_path}` direto na tag <img>
	// ou no loader do three.js. noDirFS bloqueia listagem de pasta —
	// só URLs com filename completo (UUID) funcionam.
	//
	// Cache-Control agressivo: como cada arquivo tem nome UUID e nunca
	// muda de conteúdo (qualquer "troca de thumbnail" gera UUID novo
	// + apaga o antigo), são efetivamente IMUTÁVEIS. 1 ano + immutable
	// permite que o browser sirva do disk-cache sem nem fazer 304.
	// Browsers respeitam `immutable` ignorando reload-shift-clicks.
	uploadsHandler := withImmutableCache(
		http.FileServer(noDirFS{fs: http.Dir(files.RootDir())}),
	)
	r.GET("/uploads/*filepath", func(c *gin.Context) {
		// StripPrefix porque o FileServer espera o path sem o /uploads.
		http.StripPrefix("/uploads", uploadsHandler).ServeHTTP(c.Writer, c.Request)
	})
	r.HEAD("/uploads/*filepath", func(c *gin.Context) {
		http.StripPrefix("/uploads", uploadsHandler).ServeHTTP(c.Writer, c.Request)
	})

	healthHandler := handler.NewHealthHandler(db)
	r.GET("/ping", healthHandler.Ping)

	userRepo := postgres.NewUserRepository(db)
	verifyTokenRepo := postgres.NewEmailVerificationTokenRepository(db)
	resetTokenRepo := postgres.NewPasswordResetTokenRepository(db)
	refreshTokenRepo := postgres.NewRefreshTokenRepository(db)
	authHandler := handler.NewAuthHandler(
		userRepo,
		verifyTokenRepo,
		resetTokenRepo,
		refreshTokenRepo,
		tm,
		mailer,
		frontendBaseURL,
	)
	userHandler := handler.NewUserHandler(userRepo, files)

	assetRepo := postgres.NewAssetRepository(db)
	assetHandler := handler.NewAssetHandler(assetRepo, files)

	favoriteRepo := postgres.NewFavoriteRepository(db)
	favoriteHandler := handler.NewFavoriteHandler(favoriteRepo)

	notificationRepo := postgres.NewNotificationRepository(db)
	notificationHandler := handler.NewNotificationHandler(notificationRepo)

	cartRepo := postgres.NewCartRepository(db)
	purchaseRepo := postgres.NewPurchaseRepository(db)
	cartHandler := handler.NewCartHandler(cartRepo, purchaseRepo, notificationRepo)

	packRepo := postgres.NewPackRepository(db)
	packHandler := handler.NewPackHandler(packRepo, files)

	reviewRepo := postgres.NewReviewRepository(db)
	reviewHandler := handler.NewReviewHandler(reviewRepo, purchaseRepo, notificationRepo)

	api := r.Group("/api/v1")
	{
		api.POST("/register", authHandler.Register)
		api.POST("/login", authHandler.Login)
		// Refresh tokens: rotação estrita (revoga ao usar). Sem auth
		// — o próprio refresh_token é a credencial.
		api.POST("/refresh", authHandler.Refresh)
		// Logout único: revoga UM refresh_token específico. Idempotente.
		api.POST("/logout", authHandler.Logout)
		// Recuperação de senha: forgot é 202 sempre (anti-enumeration);
		// reset consome token + atualiza senha + revoga sessões.
		api.POST("/forgot-password", authHandler.ForgotPassword)
		api.POST("/reset-password", authHandler.ResetPassword)
		// Verificação de email: público (token é a credencial).
		api.POST("/verify-email", authHandler.VerifyEmail)

		// Catálogo de assets é público — qualquer um pode listar e
		// ver detalhes. Mantemos FORA do grupo protegido de propósito.
		api.GET("/assets", assetHandler.List)
		api.GET("/assets/:id", assetHandler.GetByID)
		// /assets/:id/similar devolve até N assets com tags em comum.
		// Pública porque o catálogo é público; ajuda descoberta no
		// AssetDetail.
		api.GET("/assets/:id/similar", assetHandler.Similar)
		// /trending devolve os mais comprados, ordenados por contagem
		// de purchases. Pública porque alimenta a sessão "Em alta"
		// da home.
		api.GET("/trending", assetHandler.Trending)
		// /tags devolve [{tag, count}] do catálogo inteiro. Público
		// porque o catálogo é público — não vaza nada novo.
		api.GET("/tags", assetHandler.Tags)

		// Reviews públicos: qualquer um vê a lista e o resumo (média
		// + count). Criar/editar/deletar fica no grupo protegido
		// abaixo, com checagem de compra.
		api.GET("/assets/:id/reviews", reviewHandler.List)
		api.GET("/assets/:id/reviews/summary", reviewHandler.Summary)

		// Packs públicos (listagem + detalhe). Editorial agrupando
		// N assets do mesmo vendedor num único item à venda.
		api.GET("/packs", packHandler.List)
		api.GET("/packs/:id", packHandler.GetByID)
		// Lookup inverso: packs que contêm este asset. Pública —
		// AssetDetail mostra badge "também faz parte do pack X".
		api.GET("/assets/:id/packs", packHandler.ByAssetID)

		// Diretório de criadores. Pública porque o catálogo já
		// expõe autores. Aceita ?limit= pra alimentar a sessão
		// "Top criadores" da home sem precisar baixar todos.
		api.GET("/users", userHandler.List)
		// Perfil público por username. Devolve PublicUser (sem email).
		api.GET("/users/:username", userHandler.GetByUsername)

		// Rotas protegidas: tudo dentro deste grupo exige um JWT
		// válido. O middleware popula o userID no contexto, que os
		// handlers leem para saber quem é o autor da request.
		protected := api.Group("")
		protected.Use(middleware.RequireAuth(tm))
		{
			// POST /assets exige email verificado — é a única ação
			// "criativa" gateada por enquanto. Login/browse/library
			// continuam liberados pra que o user complete o flow.
			protected.POST("/assets",
				middleware.RequireVerifiedEmail(userRepo),
				assetHandler.Create,
			)
			protected.PUT("/assets/:id", assetHandler.Update)
			protected.DELETE("/assets/:id", assetHandler.Delete)
			// ResendVerification fica autenticado (front pede com JWT
			// do próprio user) — não precisa proteção extra.
			protected.POST("/resend-verification", authHandler.ResendVerification)
			// Trocar só o arquivo físico (thumbnail OU modelo). Multipart.
			// Mantemos separado do PUT JSON pra não misturar dois mundos
			// no mesmo endpoint e pra que o cliente possa trocar arquivos
			// independentemente dos metadados.
			protected.PUT("/assets/:id/thumbnail", assetHandler.ReplaceThumbnail)
			protected.PUT("/assets/:id/model", assetHandler.ReplaceModel)

			// "Minha loja": lista os assets do usuário logado.
			// Namespace /my/* deixa claro que tudo aqui é filtrado
			// pela identidade do JWT — quando vier /my/library,
			// /my/orders, etc, ficam todos juntos sob o mesmo prefixo.
			protected.GET("/my/assets", assetHandler.MyAssets)

			// Favoritos do usuário. POST/DELETE no asset específico
			// pra que a UX (clicar no coração de um card) mapeie 1:1
			// num verbo HTTP. /my/favorites devolve a lista completa
			// (Asset[]); /my/favorite-ids devolve só os IDs pra
			// hidratar os corações na Gallery em uma round-trip.
			protected.POST("/assets/:id/favorite", favoriteHandler.Add)
			protected.DELETE("/assets/:id/favorite", favoriteHandler.Remove)
			protected.GET("/my/favorites", favoriteHandler.List)
			protected.GET("/my/favorite-ids", favoriteHandler.ListIDs)

			// Carrinho. Add/Remove no asset específico (UX 1:1).
			// Checkout em /my/cart/checkout porque atua sobre o
			// carrinho inteiro, não num asset. Library = histórico
			// de compras (Purchase[]); library-ids hidrata UI sem N+1.
			protected.POST("/assets/:id/cart", cartHandler.Add)
			protected.DELETE("/assets/:id/cart", cartHandler.Remove)
			// Packs entram no carrinho como linha própria (carrinho misto
			// asset/pack desde a migration 014). Checkout expande.
			protected.POST("/packs/:id/cart", cartHandler.AddPack)
			protected.DELETE("/packs/:id/cart", cartHandler.RemovePack)
			protected.GET("/my/cart", cartHandler.List)
			protected.GET("/my/cart-ids", cartHandler.ListIDs)
			protected.DELETE("/my/cart", cartHandler.Clear)
			protected.POST("/my/cart/checkout", cartHandler.Checkout)
			// Checkout abre uma session 'pending'. Stub do provedor de
			// pagamento (Stripe/MercadoPago) é simulado pelo frontend:
			// GET pega o detalhe da sessão, POST /confirm marca como
			// pago + dispara notificações. Idempotente — webhook real
			// pode retry o request sem duplicar nada.
			protected.GET("/my/checkout/sessions/:id", cartHandler.GetCheckoutSession)
			protected.POST("/my/checkout/sessions/:id/confirm", cartHandler.ConfirmCheckoutSession)
			protected.GET("/my/library", cartHandler.Library)
			protected.GET("/my/library-ids", cartHandler.LibraryIDs)

			// Dashboard analítico do vendedor: totais + top asset +
			// últimas vendas. Agrega `purchases` JOIN `assets` filtrando
			// pelo dono — cada usuário só vê suas próprias métricas.
			protected.GET("/my/store/stats", cartHandler.StoreStats)

			// Reviews — escrita protegida. POST exige que o usuário
			// tenha comprado o asset (validado no handler). PUT/DELETE
			// só pelo autor. Listar/summary continua público acima.
			protected.POST("/assets/:id/reviews", reviewHandler.Create)
			protected.PUT("/reviews/:id", reviewHandler.Update)
			protected.DELETE("/reviews/:id", reviewHandler.Delete)

			// Packs — CRUD do vendedor. Create é multipart (texto +
			// thumbnail opcional + asset_ids[]); Update é JSON puro.
			// ReplaceThumbnail dedicado (mesmo padrão do asset).
			protected.POST("/packs", packHandler.Create)
			protected.PUT("/packs/:id", packHandler.Update)
			protected.DELETE("/packs/:id", packHandler.Delete)
			protected.PUT("/packs/:id/thumbnail", packHandler.ReplaceThumbnail)
			protected.GET("/my/packs", packHandler.MyPacks)

			// Notificações in-app do usuário. Geradas em hooks
			// (CartHandler.Checkout, ReviewHandler.Create); aqui só
			// listamos/marcamos como lidas.
			protected.GET("/my/notifications", notificationHandler.List)
			protected.GET("/my/notifications/unread-count", notificationHandler.UnreadCount)
			protected.POST("/my/notifications/read-all", notificationHandler.MarkAllRead)

			// Perfil próprio: GET retorna a versão completa (com email),
			// PATCH edita display_name/bio, POST/DELETE /avatar trocam
			// ou removem a foto. Username e email NÃO entram aqui —
			// mudar exige fluxos próprios (re-verificação de email,
			// redirect dos /u/:username antigos).
			protected.GET("/users/me", userHandler.GetMe)
			protected.PATCH("/users/me", userHandler.UpdateMe)
			protected.POST("/users/me/avatar", userHandler.UploadAvatar)
			protected.DELETE("/users/me/avatar", userHandler.DeleteAvatar)
		}
	}

	return r
}

// Addr formata a porta no padrão esperado pelo net/http (":8080").
func Addr(port int) string {
	return fmt.Sprintf(":%d", port)
}
