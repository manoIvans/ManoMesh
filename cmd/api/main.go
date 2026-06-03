package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/manoIvans/manomesh/internal/auth"
	"github.com/manoIvans/manomesh/internal/config"
	"github.com/manoIvans/manomesh/internal/mail"
	"github.com/manoIvans/manomesh/internal/migrate"
	"github.com/manoIvans/manomesh/internal/repository/postgres"
	"github.com/manoIvans/manomesh/internal/storage"
	httptransport "github.com/manoIvans/manomesh/internal/transport/http"
)

func main() {
	// 1) Configuração
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// 2) Context global que captura SIGINT/SIGTERM. É o que dispara
	// o graceful shutdown quando você dá Ctrl+C ou o orquestrador
	// pede para o container parar.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 3) Banco — o programa morre cedo se não conseguir conectar.
	// Melhor falhar no boot do que servir requests com 500.
	db, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
	log.Println("postgres conectado")

	// 3.5) Migrations. Roda ANTES de qualquer handler tocar no banco,
	// pra que o schema esteja sempre atualizado. Idempotente: se tudo
	// já está aplicado, é só uma query de leitura na schema_migrations.
	// Falha hard se algo der errado — melhor crashar no boot que servir
	// requests com schema parcialmente migrado.
	if err := migrate.Run(ctx, db, cfg.MigrationsDir); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// 4) Gerenciador de tokens JWT. Criado uma vez no boot e
	// reaproveitado em todas as requests — o segredo nunca sai
	// daqui.
	tokenManager := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)

	// 5) Storage local para os arquivos físicos dos assets. Criar
	// no boot garante que falhas de permissão/disco apareçam aqui
	// e não na primeira request de upload em produção.
	fileStorage, err := storage.NewLocalStorage(cfg.UploadDir)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	log.Printf("uploads em %q", cfg.UploadDir)

	// 5.5) Mailer transacional. Default é StubMailer (loga no stdout)
	// pra que dev tenha o link de verificação/reset disponível sem
	// configurar provider externo. Plugar Resend/SMTP é trocar a impl
	// — interface mail.Mailer.
	mailer := mail.NewStubMailer()
	log.Println("mailer: stub (logs no stdout)")

	// 6) Roteador + servidor HTTP. Os timeouts são DEFENSIVOS, mas
	// precisam acomodar uploads grandes (.glb pode chegar a ~100 MiB).
	// Por isso usamos ReadHeaderTimeout curto (protege contra slowloris
	// no handshake) e ReadTimeout longo (cobre upload em conexão lenta).
	router := httptransport.NewRouter(
		db, tokenManager, fileStorage,
		mailer, cfg.FrontendBaseURL,
		cfg.AllowedOrigins,
	)
	srv := &http.Server{
		Addr:              httptransport.Addr(cfg.AppPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	// 5) Servidor sobe em goroutine separada para que a main thread
	// possa ficar escutando o sinal de shutdown.
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("servidor escutando em %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// 6) Espera o que acontecer primeiro: erro do servidor ou sinal.
	select {
	case err := <-serverErr:
		log.Fatalf("server: %v", err)
	case <-ctx.Done():
		log.Println("sinal recebido, desligando servidor...")
	}

	// 7) Graceful shutdown: dá até 10s para terminar requests em
	// andamento antes de matar conexões à força.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown forçado: %v", err)
	}
	log.Println("encerrado")
}
