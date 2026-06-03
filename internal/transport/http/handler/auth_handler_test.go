package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/manoIvans/manomesh/internal/auth"
	"github.com/manoIvans/manomesh/internal/domain"
	"github.com/manoIvans/manomesh/internal/mail"
)

// Testes do AuthHandler. Cobrem o caminho feliz e os erros mais
// importantes pra UX (credencial errada, conflitos, validação) +
// fluxos novos: forgot/reset, verify email, refresh (rotação +
// detecção de reuso).
//
// TokenManager usa um secret de teste — JWT real, não fake. Mailer
// é um stub no-op (não loga em test); repos de token são fakes
// que registram chamadas pra que os testes inspecionem o efeito.

func newTestTokenManager() *auth.TokenManager {
	return auth.NewTokenManager("test-secret-only-for-tests", time.Hour)
}

// authDeps agrupa as 4 deps do AuthHandler pra que cada test possa
// sobrescrever só o que precisa.
type authDeps struct {
	users   *fakeUserRepo
	verify  *fakeVerifyRepo
	reset   *fakeResetRepo
	refresh *fakeRefreshRepo
}

func newAuthDeps() authDeps {
	return authDeps{
		users:   &fakeUserRepo{},
		verify:  &fakeVerifyRepo{},
		reset:   &fakeResetRepo{},
		refresh: &fakeRefreshRepo{},
	}
}

// setupAuth devolve a engine com todas as rotas do AuthHandler. O
// mailer é stub do pacote mail (não escreve em log). Frontend base
// é uma URL fixa pra que possamos verificar os links nos testes que
// quiserem inspecionar.
func setupAuth(t *testing.T, d authDeps) *gin.Engine {
	t.Helper()
	h := NewAuthHandler(
		d.users, d.verify, d.reset, d.refresh,
		newTestTokenManager(),
		mail.NewStubMailer(),
		"http://test-frontend.example",
	)
	eng := newTestEngine(t)
	eng.POST("/register", h.Register)
	eng.POST("/login", h.Login)
	eng.POST("/refresh", h.Refresh)
	eng.POST("/logout", h.Logout)
	eng.POST("/forgot-password", h.ForgotPassword)
	eng.POST("/reset-password", h.ResetPassword)
	eng.POST("/verify-email", h.VerifyEmail)
	return eng
}

// ============================================================
// Register
// ============================================================

func TestRegister_Success(t *testing.T) {
	d := newAuthDeps()
	d.users.CreateFn = func(_ context.Context, email, hash, username, displayName string) (*domain.User, error) {
		if email != "ivan@test.com" {
			t.Errorf("email deveria estar lowercase, got %q", email)
		}
		if hash == "senha123456" {
			t.Errorf("repo recebeu senha em texto puro — bcrypt não aplicado")
		}
		if username != "ivan" {
			t.Errorf("username: want %q, got %q", "ivan", username)
		}
		return &domain.User{ID: 1, Email: email, Username: username, DisplayName: displayName}, nil
	}
	eng := setupAuth(t, d)

	w := doJSON(t, eng, http.MethodPost, "/register", map[string]string{
		"email":        "Ivan@Test.com",
		"password":     "senha123456",
		"username":     "ivan",
		"display_name": "Ivan",
	}, "")
	assertStatus(t, w, http.StatusCreated)
	body := decodeJSON(t, w)
	if _, ok := body["access_token"].(string); !ok {
		t.Fatalf("access_token ausente: %v", body)
	}
	if _, ok := body["refresh_token"].(string); !ok {
		t.Fatalf("refresh_token ausente: %v", body)
	}
}

func TestRegister_EmailConflict(t *testing.T) {
	d := newAuthDeps()
	d.users.CreateFn = func(_ context.Context, _, _, _, _ string) (*domain.User, error) {
		return nil, domain.ErrEmailAlreadyExists
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/register", map[string]string{
		"email": "ivan@test.com", "password": "senha123456",
		"username": "ivan", "display_name": "Ivan",
	}, "")
	assertStatus(t, w, http.StatusConflict)
	assertJSONString(t, w, "error", "email já cadastrado")
}

func TestRegister_UsernameConflict(t *testing.T) {
	d := newAuthDeps()
	d.users.CreateFn = func(_ context.Context, _, _, _, _ string) (*domain.User, error) {
		return nil, domain.ErrUsernameAlreadyExists
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/register", map[string]string{
		"email": "ivan@test.com", "password": "senha123456",
		"username": "ivan", "display_name": "Ivan",
	}, "")
	assertStatus(t, w, http.StatusConflict)
	assertJSONString(t, w, "error", "username já cadastrado")
}

func TestRegister_InvalidUsername(t *testing.T) {
	// CreateFn deliberadamente nil — não deve ser chamado.
	d := newAuthDeps()
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/register", map[string]string{
		"email": "ivan@test.com", "password": "senha123456",
		"username": "Ivan With Spaces", "display_name": "Ivan",
	}, "")
	assertStatus(t, w, http.StatusBadRequest)
}

func TestRegister_PasswordTooShort(t *testing.T) {
	d := newAuthDeps()
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/register", map[string]string{
		"email": "ivan@test.com", "password": "1234567",
		"username": "ivan", "display_name": "Ivan",
	}, "")
	assertStatus(t, w, http.StatusBadRequest)
}

// ============================================================
// Login
// ============================================================

func TestLogin_Success(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("setup bcrypt: %v", err)
	}
	d := newAuthDeps()
	d.users.FindByEmailFn = func(_ context.Context, _ string) (*domain.User, error) {
		return &domain.User{ID: 42, Email: "ivan@test.com", PasswordHash: string(hash)}, nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/login", map[string]string{
		"email": "ivan@test.com", "password": "secret123",
	}, "")
	assertStatus(t, w, http.StatusOK)
	body := decodeJSON(t, w)
	if _, ok := body["access_token"].(string); !ok {
		t.Fatalf("access_token ausente: %v", body)
	}
	if _, ok := body["refresh_token"].(string); !ok {
		t.Fatalf("refresh_token ausente: %v", body)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("certasenha"), bcrypt.MinCost)
	d := newAuthDeps()
	d.users.FindByEmailFn = func(_ context.Context, _ string) (*domain.User, error) {
		return &domain.User{ID: 1, Email: "ivan@test.com", PasswordHash: string(hash)}, nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/login", map[string]string{
		"email": "ivan@test.com", "password": "errado",
	}, "")
	assertStatus(t, w, http.StatusUnauthorized)
	assertJSONString(t, w, "error", "credenciais inválidas")
}

func TestLogin_UserNotFound(t *testing.T) {
	d := newAuthDeps()
	d.users.FindByEmailFn = func(_ context.Context, _ string) (*domain.User, error) {
		return nil, domain.ErrUserNotFound
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/login", map[string]string{
		"email": "naoexiste@test.com", "password": "qualquercoisa",
	}, "")
	assertStatus(t, w, http.StatusUnauthorized)
	assertJSONString(t, w, "error", "credenciais inválidas")
}

// ============================================================
// ForgotPassword (sempre 202; cria token quando email existe)
// ============================================================

func TestForgotPassword_EmailExists_CreatesToken(t *testing.T) {
	createCalls := 0
	d := newAuthDeps()
	d.users.FindByEmailFn = func(_ context.Context, _ string) (*domain.User, error) {
		return &domain.User{ID: 1, Email: "ivan@test.com", DisplayName: "Ivan"}, nil
	}
	d.reset.CreateFn = func(_ context.Context, userID int64, _ string, _ time.Time) error {
		if userID != 1 {
			t.Errorf("create reset com user errado: %d", userID)
		}
		createCalls++
		return nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/forgot-password", map[string]string{
		"email": "ivan@test.com",
	}, "")
	assertStatus(t, w, http.StatusAccepted)
	if createCalls != 1 {
		t.Errorf("reset token deveria ter sido criado 1x, got %d", createCalls)
	}
}

func TestForgotPassword_EmailNotFound_StillReturns202(t *testing.T) {
	// Anti-enumeration: mesmo 202 quando email não existe. Reset
	// repo NÃO é chamado.
	d := newAuthDeps()
	d.users.FindByEmailFn = func(_ context.Context, _ string) (*domain.User, error) {
		return nil, domain.ErrUserNotFound
	}
	// CreateFn deliberadamente nil — não deve ser invocado.
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/forgot-password", map[string]string{
		"email": "ninguem@test.com",
	}, "")
	assertStatus(t, w, http.StatusAccepted)
}

// ============================================================
// ResetPassword
// ============================================================

func TestResetPassword_Success_UpdatesAndRevokes(t *testing.T) {
	revokedUser := int64(0)
	d := newAuthDeps()
	d.reset.ConsumeFn = func(_ context.Context, _ string) (int64, error) {
		return 42, nil
	}
	d.users.UpdatePasswordFn = func(_ context.Context, id int64, newHash string) error {
		if id != 42 {
			t.Errorf("update password id: %d", id)
		}
		if newHash == "novasenha123" {
			t.Errorf("repo recebeu senha em texto puro — bcrypt não aplicado")
		}
		return nil
	}
	d.refresh.RevokeAllForUserFn = func(_ context.Context, userID int64) error {
		revokedUser = userID
		return nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/reset-password", map[string]string{
		"token":        "qualquer-token-raw",
		"new_password": "novasenha123",
	}, "")
	assertStatus(t, w, http.StatusNoContent)
	if revokedUser != 42 {
		t.Errorf("revoke all NÃO foi chamado pro user 42, got %d", revokedUser)
	}
}

func TestResetPassword_InvalidToken(t *testing.T) {
	d := newAuthDeps()
	d.reset.ConsumeFn = func(_ context.Context, _ string) (int64, error) {
		return 0, domain.ErrInvalidToken
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/reset-password", map[string]string{
		"token": "abc", "new_password": "novasenha123",
	}, "")
	assertStatus(t, w, http.StatusBadRequest)
}

func TestResetPassword_ExpiredToken(t *testing.T) {
	d := newAuthDeps()
	d.reset.ConsumeFn = func(_ context.Context, _ string) (int64, error) {
		return 0, domain.ErrExpiredToken
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/reset-password", map[string]string{
		"token": "abc", "new_password": "novasenha123",
	}, "")
	assertStatus(t, w, http.StatusGone)
}

// ============================================================
// VerifyEmail
// ============================================================

func TestVerifyEmail_Success(t *testing.T) {
	verifiedUser := int64(0)
	d := newAuthDeps()
	d.verify.ConsumeFn = func(_ context.Context, _ string) (int64, error) {
		return 7, nil
	}
	d.users.SetEmailVerifiedFn = func(_ context.Context, id int64) error {
		verifiedUser = id
		return nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/verify-email", map[string]string{
		"token": "qualquer",
	}, "")
	assertStatus(t, w, http.StatusNoContent)
	if verifiedUser != 7 {
		t.Errorf("SetEmailVerified NÃO foi chamado pro user 7, got %d", verifiedUser)
	}
}

func TestVerifyEmail_InvalidToken(t *testing.T) {
	d := newAuthDeps()
	d.verify.ConsumeFn = func(_ context.Context, _ string) (int64, error) {
		return 0, domain.ErrInvalidToken
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/verify-email", map[string]string{
		"token": "abc",
	}, "")
	assertStatus(t, w, http.StatusBadRequest)
}

// ============================================================
// Refresh + Logout
// ============================================================

func TestRefresh_Success_RotatesPair(t *testing.T) {
	d := newAuthDeps()
	d.refresh.RotateFn = func(_ context.Context, _, _ string, _ time.Time) (int64, int64, error) {
		return 99, 123, nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/refresh", map[string]string{
		"refresh_token": "qualquer-token-cru",
	}, "")
	assertStatus(t, w, http.StatusOK)
	body := decodeJSON(t, w)
	if _, ok := body["access_token"].(string); !ok {
		t.Fatalf("access_token ausente: %v", body)
	}
	rt, ok := body["refresh_token"].(string)
	if !ok || rt == "qualquer-token-cru" {
		t.Errorf("refresh_token deveria ser NOVO, got %q", rt)
	}
}

func TestRefresh_Invalid(t *testing.T) {
	d := newAuthDeps()
	d.refresh.RotateFn = func(_ context.Context, _, _ string, _ time.Time) (int64, int64, error) {
		return 0, 0, domain.ErrInvalidToken
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/refresh", map[string]string{
		"refresh_token": "lixo",
	}, "")
	assertStatus(t, w, http.StatusUnauthorized)
}

func TestRefresh_Expired(t *testing.T) {
	d := newAuthDeps()
	d.refresh.RotateFn = func(_ context.Context, _, _ string, _ time.Time) (int64, int64, error) {
		return 0, 0, domain.ErrExpiredToken
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/refresh", map[string]string{
		"refresh_token": "antigo",
	}, "")
	assertStatus(t, w, http.StatusUnauthorized)
}

func TestRefresh_Reused_SignalsRevoke(t *testing.T) {
	// Detecção de reuso — repo já fez a revogação total dentro do
	// Rotate; handler só mapeia pra 401 com mensagem distinta. Frontend
	// vai ver 401 e deslogar.
	d := newAuthDeps()
	d.refresh.RotateFn = func(_ context.Context, _, _ string, _ time.Time) (int64, int64, error) {
		return 0, 0, domain.ErrRefreshTokenReused
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/refresh", map[string]string{
		"refresh_token": "roubado",
	}, "")
	assertStatus(t, w, http.StatusUnauthorized)
	assertJSONString(t, w, "error", "sessão revogada por segurança")
}

func TestLogout_Idempotent(t *testing.T) {
	revoked := 0
	d := newAuthDeps()
	d.refresh.RevokeFn = func(_ context.Context, _ string) error {
		revoked++
		return nil
	}
	eng := setupAuth(t, d)
	w := doJSON(t, eng, http.MethodPost, "/logout", map[string]string{
		"refresh_token": "qualquer",
	}, "")
	assertStatus(t, w, http.StatusNoContent)
	if revoked != 1 {
		t.Errorf("Revoke deveria ter sido chamado 1x, got %d", revoked)
	}
}
