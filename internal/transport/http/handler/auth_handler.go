package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/manoIvans/manomesh/internal/auth"
	"github.com/manoIvans/manomesh/internal/domain"
	"github.com/manoIvans/manomesh/internal/mail"
)

// userRepository: interface pequena que o AuthHandler precisa.
// Mantida no consumer pra desacoplar e facilitar mock.
type userRepository interface {
	Create(ctx context.Context, email, passwordHash, username, displayName string) (*domain.User, error)
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
	FindByID(ctx context.Context, id int64) (*domain.User, error)
	UpdatePassword(ctx context.Context, id int64, newHash string) error
	SetEmailVerified(ctx context.Context, id int64) error
}

// emailVerificationRepo: persiste tokens de verificação de email.
type emailVerificationRepo interface {
	Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error
	Consume(ctx context.Context, tokenHash string) (int64, error)
}

// passwordResetRepo: persiste tokens de reset de senha.
type passwordResetRepo interface {
	Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error
	Consume(ctx context.Context, tokenHash string) (int64, error)
}

// refreshTokenRepo: persiste refresh tokens com rotação estrita.
type refreshTokenRepo interface {
	Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (int64, error)
	Rotate(ctx context.Context, oldHash, newHash string, newExpiresAt time.Time) (userID, newID int64, err error)
	Revoke(ctx context.Context, tokenHash string) error
	RevokeAllForUser(ctx context.Context, userID int64) error
}

// usernameRegexp valida o formato de username. Mesma regra do CHECK
// no banco (migration 005).
var usernameRegexp = regexp.MustCompile(`^[a-z0-9_]{1,30}$`)

// TTLs canônicos. Centralizados aqui pra ficar óbvio o que vai mudar
// quando ajustarmos politicas (ex: refresh de 30 → 14 dias).
const (
	verifyTokenTTL  = 24 * time.Hour
	resetTokenTTL   = 1 * time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// AuthHandler agrupa registro, login, reset/verify e rotação de
// refresh tokens. Frontend base URL é necessário pra montar links
// de email — não usamos `c.Request.Host` porque o backend pode rodar
// num domínio diferente do frontend (sempre roda em dev).
type AuthHandler struct {
	users            userRepository
	verifyTokens     emailVerificationRepo
	resetTokens      passwordResetRepo
	refreshTokens    refreshTokenRepo
	tm               *auth.TokenManager
	mailer           mail.Mailer
	frontendBaseURL  string
}

func NewAuthHandler(
	users userRepository,
	verifyTokens emailVerificationRepo,
	resetTokens passwordResetRepo,
	refreshTokens refreshTokenRepo,
	tm *auth.TokenManager,
	mailer mail.Mailer,
	frontendBaseURL string,
) *AuthHandler {
	return &AuthHandler{
		users:           users,
		verifyTokens:    verifyTokens,
		resetTokens:     resetTokens,
		refreshTokens:   refreshTokens,
		tm:              tm,
		mailer:          mailer,
		frontendBaseURL: strings.TrimRight(frontendBaseURL, "/"),
	}
}

// ============================================================
// Request/response shapes
// ============================================================

type registerRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=8"`
	Username    string `json:"username" binding:"required,min=1,max=30"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=60"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type forgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type resetPasswordRequest struct {
	Token       string `json:"token"        binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

type verifyEmailRequest struct {
	Token string `json:"token" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// authResponse: shape unificado de Login, Register e Refresh.
// access_token é JWT (TTL curto); refresh_token é opaque (long TTL,
// armazenado hashed no DB). Front guarda os dois separados.
type authResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         *domain.User `json:"user,omitempty"`
}

// ============================================================
// Register
// ============================================================

// Register cria user, emite par de tokens E dispara email de
// verificação (best-effort — falha no email não bloqueia o cadastro;
// usuário pode pedir reenvio em /verify).
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if !usernameRegexp.MatchString(username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "username deve ter 1-30 caracteres usando apenas a-z, 0-9 e _",
		})
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "display_name é obrigatório"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		serverError(c, "bcrypt hash", err, "falha ao processar senha")
		return
	}

	user, err := h.users.Create(c.Request.Context(), email, string(hash), username, displayName)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "email já cadastrado"})
		case errors.Is(err, domain.ErrUsernameAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": "username já cadastrado"})
		default:
			serverError(c, "create user", err, "falha ao criar usuário")
		}
		return
	}

	// Dispara verificação best-effort. Erro só vai pro log; UX do
	// register continua o mesmo (cadastro ok, mensagem do banner
	// instrui usuário a buscar email ou pedir reenvio).
	h.sendVerificationEmail(c.Request.Context(), user)

	resp, err := h.issueTokens(c.Request.Context(), user)
	if err != nil {
		serverError(c, "issue tokens (register)", err, "falha ao gerar tokens")
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// ============================================================
// Login (mesma mensagem para "email não existe" e "senha errada")
// ============================================================

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.users.FindByEmail(c.Request.Context(), email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "credenciais inválidas"})
			return
		}
		serverError(c, "find user by email", err, "falha ao consultar usuário")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "credenciais inválidas"})
		return
	}

	resp, err := h.issueTokens(c.Request.Context(), user)
	if err != nil {
		serverError(c, "issue tokens (login)", err, "falha ao gerar tokens")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ============================================================
// Refresh (rotação estrita)
// ============================================================

// Refresh consome um refresh_token, emite par novo (access+refresh),
// revoga o antigo apontando replaced_by_id. Detecta reuso e revoga
// tudo do user nesse caso.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldHash := auth.HashToken(req.RefreshToken)
	newRaw, newHash, err := auth.GenerateToken()
	if err != nil {
		serverError(c, "generate refresh", err, "falha ao gerar token")
		return
	}

	userID, _, err := h.refreshTokens.Rotate(
		c.Request.Context(),
		oldHash, newHash,
		time.Now().Add(refreshTokenTTL),
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidToken):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh inválido"})
		case errors.Is(err, domain.ErrExpiredToken):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh expirado"})
		case errors.Is(err, domain.ErrRefreshTokenReused):
			// 401 + tudo do user já foi revogado dentro do Rotate.
			c.JSON(http.StatusUnauthorized, gin.H{"error": "sessão revogada por segurança"})
		default:
			serverError(c, "rotate refresh", err, "falha no refresh")
		}
		return
	}

	access, err := h.tm.Generate(userID)
	if err != nil {
		serverError(c, "generate access (refresh)", err, "falha ao gerar token")
		return
	}
	c.JSON(http.StatusOK, authResponse{AccessToken: access, RefreshToken: newRaw})
}

// ============================================================
// Logout (revoga refresh — access expira sozinho)
// ============================================================

func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Idempotente: se já estava revogado/inexistente, segue 204.
	if err := h.refreshTokens.Revoke(c.Request.Context(), auth.HashToken(req.RefreshToken)); err != nil {
		serverError(c, "revoke refresh", err, "falha no logout")
		return
	}
	c.Status(http.StatusNoContent)
}

// ============================================================
// ForgotPassword (sempre 202, não vaza se email existe)
// ============================================================

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req forgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Lookup silencioso — se email não existe, 202 mesmo assim
	// (anti-enumeration). Em erro de DB, também 202 — log interno
	// permite debug sem vazar info ao cliente.
	user, err := h.users.FindByEmail(c.Request.Context(), email)
	if err != nil {
		if !errors.Is(err, domain.ErrUserNotFound) {
			log.Printf("forgot-password lookup: %v", err)
		}
		c.Status(http.StatusAccepted)
		return
	}

	rawToken, tokenHash, err := auth.GenerateToken()
	if err != nil {
		log.Printf("forgot-password gen: %v", err)
		c.Status(http.StatusAccepted)
		return
	}
	if err := h.resetTokens.Create(
		c.Request.Context(),
		user.ID, tokenHash,
		time.Now().Add(resetTokenTTL),
	); err != nil {
		log.Printf("forgot-password persist: %v", err)
		c.Status(http.StatusAccepted)
		return
	}

	resetURL := h.frontendBaseURL + "/reset?token=" + rawToken
	msg := mail.FormatPasswordResetEmail(user.DisplayName, resetURL)
	msg.To = user.Email
	if err := h.mailer.Send(c.Request.Context(), msg); err != nil {
		log.Printf("forgot-password send: %v", err)
	}
	c.Status(http.StatusAccepted)
}

// ============================================================
// ResetPassword
// ============================================================

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := h.resetTokens.Consume(
		c.Request.Context(),
		auth.HashToken(req.Token),
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidToken):
			c.JSON(http.StatusBadRequest, gin.H{"error": "token inválido"})
		case errors.Is(err, domain.ErrExpiredToken):
			c.JSON(http.StatusGone, gin.H{"error": "token expirado"})
		default:
			serverError(c, "consume reset", err, "falha ao processar reset")
		}
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		serverError(c, "bcrypt hash (reset)", err, "falha ao processar senha")
		return
	}
	if err := h.users.UpdatePassword(c.Request.Context(), userID, string(hash)); err != nil {
		serverError(c, "update password (reset)", err, "falha ao atualizar senha")
		return
	}
	// Revoga todas as sessões — se atacante tinha sessão com senha
	// antiga, ela vira inválida no próximo /refresh.
	if err := h.refreshTokens.RevokeAllForUser(c.Request.Context(), userID); err != nil {
		log.Printf("revoke all on reset: %v", err)
	}
	c.Status(http.StatusNoContent)
}

// ============================================================
// VerifyEmail + ResendVerification
// ============================================================

func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req verifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := h.verifyTokens.Consume(
		c.Request.Context(),
		auth.HashToken(req.Token),
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidToken):
			c.JSON(http.StatusBadRequest, gin.H{"error": "token inválido"})
		case errors.Is(err, domain.ErrExpiredToken):
			c.JSON(http.StatusGone, gin.H{"error": "token expirado"})
		default:
			serverError(c, "consume verify", err, "falha ao verificar email")
		}
		return
	}

	if err := h.users.SetEmailVerified(c.Request.Context(), userID); err != nil {
		serverError(c, "set email verified", err, "falha ao confirmar email")
		return
	}
	c.Status(http.StatusNoContent)
}

// ResendVerification: rota AUTENTICADA (precisa do JWT do próprio user).
// Diferente do ForgotPassword (anti-enumeration), aqui o solicitante já
// se identificou — devolvemos sempre 202 mesmo assim por simplicidade.
func (h *AuthHandler) ResendVerification(c *gin.Context) {
	userID, ok := userIDFromContext(c)
	if !ok {
		return
	}
	user, err := h.users.FindByID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, "find user (resend)", err, "falha ao reenviar")
		return
	}
	// Já verificado: no-op (UX: front esconde o botão; resposta segue 202).
	if user.EmailVerifiedAt != nil {
		c.Status(http.StatusAccepted)
		return
	}
	h.sendVerificationEmail(c.Request.Context(), user)
	c.Status(http.StatusAccepted)
}

// ============================================================
// Helpers (não exportados)
// ============================================================

// issueTokens cria o par access+refresh pro user. Usado por
// Register e Login. Falha aqui é 500 — sem token o cliente não loga.
func (h *AuthHandler) issueTokens(ctx context.Context, user *domain.User) (authResponse, error) {
	access, err := h.tm.Generate(user.ID)
	if err != nil {
		return authResponse{}, err
	}
	rawRefresh, refreshHash, err := auth.GenerateToken()
	if err != nil {
		return authResponse{}, err
	}
	if _, err := h.refreshTokens.Create(ctx, user.ID, refreshHash, time.Now().Add(refreshTokenTTL)); err != nil {
		return authResponse{}, err
	}
	return authResponse{
		AccessToken:  access,
		RefreshToken: rawRefresh,
		User:         user,
	}, nil
}

// sendVerificationEmail é best-effort — falha só vai pro log. Caller
// continua o fluxo (register/resend); usuário pode pedir reenvio
// via UI se o email não chegou.
func (h *AuthHandler) sendVerificationEmail(ctx context.Context, user *domain.User) {
	rawToken, tokenHash, err := auth.GenerateToken()
	if err != nil {
		log.Printf("verify token gen: %v", err)
		return
	}
	if err := h.verifyTokens.Create(ctx, user.ID, tokenHash, time.Now().Add(verifyTokenTTL)); err != nil {
		log.Printf("verify token persist: %v", err)
		return
	}
	verifyURL := h.frontendBaseURL + "/verify?token=" + rawToken
	msg := mail.FormatVerificationEmail(user.DisplayName, verifyURL)
	msg.To = user.Email
	if err := h.mailer.Send(ctx, msg); err != nil {
		log.Printf("verify email send: %v", err)
	}
}
