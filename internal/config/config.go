package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config agrega todas as configurações da aplicação carregadas a partir
// de variáveis de ambiente. É a única fonte da verdade — nenhum outro
// pacote deve chamar os.Getenv diretamente.
type Config struct {
	AppEnv      string
	AppPort     int
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration
	// UploadDir é a raiz onde o LocalStorage cria thumbnails/ e models/.
	// Caminho relativo ao CWD do processo (geralmente a raiz do projeto
	// em dev, ou um volume montado em produção).
	UploadDir string
	// AllowedOrigins lista as origens permitidas pelo CORS. Tipicamente
	// só o frontend (Vite em :5173 em dev). Múltiplas origens separadas
	// por vírgula na env var.
	AllowedOrigins []string
	// MigrationsDir é a pasta com os arquivos .sql aplicados no boot.
	// Default "migrations" assume execução com CWD na raiz do projeto;
	// em Docker, montamos a pasta em /app/migrations e setamos via env.
	MigrationsDir string
	// FrontendBaseURL é o domínio onde o frontend roda — usado pra
	// montar links nos emails (verificação + reset). Em dev é o Vite
	// (5173); em produção, o domínio público.
	FrontendBaseURL string
}

// Load lê o arquivo .env (se existir) e popula a struct Config.
// Em produção, o .env normalmente não existe e as variáveis vêm do
// orquestrador (Docker, Kubernetes, etc.) — por isso o erro do
// godotenv.Load é deliberadamente ignorado.
func Load() (*Config, error) {
	_ = godotenv.Load()

	port, err := strconv.Atoi(getEnv("APP_PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("APP_PORT inválido: %w", err)
	}

	ttlHours, err := strconv.Atoi(getEnv("JWT_TTL_HOURS", "24"))
	if err != nil {
		return nil, fmt.Errorf("JWT_TTL_HOURS inválido: %w", err)
	}

	cfg := &Config{
		AppEnv:         getEnv("APP_ENV", "development"),
		AppPort:        port,
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		JWTTTL:         time.Duration(ttlHours) * time.Hour,
		UploadDir:      getEnv("UPLOAD_DIR", "uploads"),
		AllowedOrigins: parseOrigins(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),
		MigrationsDir:   getEnv("MIGRATIONS_DIR", "migrations"),
		FrontendBaseURL: getEnv("FRONTEND_BASE_URL", "http://localhost:5173"),
	}

	// DATABASE_URL não tem default por design: rodar o servidor sem
	// banco configurado é sempre um erro de operador.
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("variável de ambiente DATABASE_URL é obrigatória")
	}

	// JWT_SECRET também é obrigatório: sem ele, tokens não podem ser
	// assinados com segurança. Não tem default por design.
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("variável de ambiente JWT_SECRET é obrigatória")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// parseOrigins quebra uma string CSV de origens em slice, descartando
// vazios. Não validamos a forma da URL aqui — uma origem mal escrita
// simplesmente não vai bater com o Origin recebido e o CORS bloqueia,
// que é o comportamento desejado em caso de erro de configuração.
func parseOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
