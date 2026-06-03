package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/manoIvans/manomesh/internal/domain"
	"github.com/manoIvans/manomesh/internal/transport/http/middleware"
)

// Setup helpers + mocks de TODAS as interfaces que os handlers
// declaram. Mantemos num único arquivo `_test.go` no mesmo package
// pra acesso aos types não-exportados (ex: userRepository) e pra
// que os testes individuais fiquem curtos: só a tabela + asserts.
//
// Convenção: cada fake struct exporta campos `XxxFn func(...)` que
// o teste configura. Se o teste não setar uma função e ela for
// chamada, panic. Isso transforma "esquecimento de mock" em falha
// óbvia em vez de nil pointer no fundo do código.

// ============================================================
// HTTP helpers
// ============================================================

func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return gin.New()
}

// doJSON faz uma request com body JSON (ou nil) e devolve o
// ResponseRecorder pra inspeção.
func doJSON(t *testing.T, eng *gin.Engine, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	return w
}

// multipartFile descreve um arquivo a anexar no doMultipart. Mantemos
// `content` como []byte (em vez de io.Reader) pra que o teste possa
// reusar fácil e validar tamanho — uploads são pequenos nos tests.
type multipartFile struct {
	field    string
	filename string
	content  []byte
}

// doMultipart monta uma request multipart/form-data com campos texto e
// arquivos. Reusado pelos tests de Create asset, avatar upload e
// replaceThumbnail/replaceModel — todos têm o mesmo padrão (alguns
// fields + um ou dois files). Sem token quando string vazia.
func doMultipart(
	t *testing.T,
	eng *gin.Engine,
	method, path string,
	fields map[string]string,
	files []multipartFile,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %q: %v", k, err)
		}
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.field, f.filename)
		if err != nil {
			t.Fatalf("create form file %q: %v", f.field, err)
		}
		if _, err := w.Write(f.content); err != nil {
			t.Fatalf("write form file %q: %v", f.field, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	return w
}

// doMultipartWithRepeats é o doMultipart com suporte a múltiplos
// valores por chave — necessário pra campos como `tags` que o handler
// lê com PostFormArray. Cada valor da slice vira um WriteField separado
// com a mesma chave.
func doMultipartWithRepeats(
	t *testing.T,
	eng *gin.Engine,
	method, path string,
	fields map[string][]string,
	files []multipartFile,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	for k, vs := range fields {
		for _, v := range vs {
			if err := mw.WriteField(k, v); err != nil {
				t.Fatalf("write field %q: %v", k, err)
			}
		}
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.field, f.filename)
		if err != nil {
			t.Fatalf("create form file %q: %v", f.field, err)
		}
		if _, err := w.Write(f.content); err != nil {
			t.Fatalf("write form file %q: %v", f.field, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	return w
}

// decodeJSON é açúcar pra ler o body da response como map.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response (%s): %v", w.Body.String(), err)
	}
	return out
}

// withAuthUser injeta o userID no contexto Gin antes do handler
// rodar. Substitui o middleware RequireAuth nos testes — não
// queremos depender de JWT real em todos os tests de handler.
func withAuthUser(userID int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.ContextUserIDKey, userID)
		c.Next()
	}
}

// ============================================================
// Mock: userRepository (AuthHandler)
// ============================================================

type fakeUserRepo struct {
	CreateFn            func(ctx context.Context, email, hash, username, displayName string) (*domain.User, error)
	FindByEmailFn       func(ctx context.Context, email string) (*domain.User, error)
	FindByIDFn          func(ctx context.Context, id int64) (*domain.User, error)
	UpdatePasswordFn    func(ctx context.Context, id int64, newHash string) error
	SetEmailVerifiedFn  func(ctx context.Context, id int64) error
}

func (f *fakeUserRepo) Create(ctx context.Context, email, hash, username, displayName string) (*domain.User, error) {
	if f.CreateFn == nil {
		panic("fakeUserRepo.Create chamado sem mock configurado")
	}
	return f.CreateFn(ctx, email, hash, username, displayName)
}
func (f *fakeUserRepo) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	if f.FindByIDFn == nil {
		panic("fakeUserRepo.FindByID chamado sem mock configurado")
	}
	return f.FindByIDFn(ctx, id)
}
func (f *fakeUserRepo) UpdatePassword(ctx context.Context, id int64, newHash string) error {
	if f.UpdatePasswordFn == nil {
		panic("fakeUserRepo.UpdatePassword chamado sem mock configurado")
	}
	return f.UpdatePasswordFn(ctx, id, newHash)
}
func (f *fakeUserRepo) SetEmailVerified(ctx context.Context, id int64) error {
	if f.SetEmailVerifiedFn == nil {
		panic("fakeUserRepo.SetEmailVerified chamado sem mock configurado")
	}
	return f.SetEmailVerifiedFn(ctx, id)
}

func (f *fakeUserRepo) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	if f.FindByEmailFn == nil {
		panic("fakeUserRepo.FindByEmail chamado sem mock configurado")
	}
	return f.FindByEmailFn(ctx, email)
}

// ============================================================
// Mocks: token repos (AuthHandler)
// ============================================================

type fakeVerifyRepo struct {
	CreateFn  func(ctx context.Context, userID int64, hash string, expires time.Time) error
	ConsumeFn func(ctx context.Context, hash string) (int64, error)
}

func (f *fakeVerifyRepo) Create(ctx context.Context, userID int64, hash string, expires time.Time) error {
	if f.CreateFn == nil {
		return nil // best-effort no caller; permite "no-op" sem panic
	}
	return f.CreateFn(ctx, userID, hash, expires)
}
func (f *fakeVerifyRepo) Consume(ctx context.Context, hash string) (int64, error) {
	if f.ConsumeFn == nil {
		panic("fakeVerifyRepo.Consume chamado sem mock")
	}
	return f.ConsumeFn(ctx, hash)
}

type fakeResetRepo struct {
	CreateFn  func(ctx context.Context, userID int64, hash string, expires time.Time) error
	ConsumeFn func(ctx context.Context, hash string) (int64, error)
}

func (f *fakeResetRepo) Create(ctx context.Context, userID int64, hash string, expires time.Time) error {
	if f.CreateFn == nil {
		return nil
	}
	return f.CreateFn(ctx, userID, hash, expires)
}
func (f *fakeResetRepo) Consume(ctx context.Context, hash string) (int64, error) {
	if f.ConsumeFn == nil {
		panic("fakeResetRepo.Consume chamado sem mock")
	}
	return f.ConsumeFn(ctx, hash)
}

type fakeRefreshRepo struct {
	CreateFn           func(ctx context.Context, userID int64, hash string, expires time.Time) (int64, error)
	RotateFn           func(ctx context.Context, oldHash, newHash string, expires time.Time) (int64, int64, error)
	RevokeFn           func(ctx context.Context, hash string) error
	RevokeAllForUserFn func(ctx context.Context, userID int64) error
}

func (f *fakeRefreshRepo) Create(ctx context.Context, userID int64, hash string, expires time.Time) (int64, error) {
	if f.CreateFn == nil {
		// Default: aceita silenciosamente, devolve id=1. Auth.Register/Login
		// chamam isso pra emitir o par e raramente o teste precisa controlar.
		return 1, nil
	}
	return f.CreateFn(ctx, userID, hash, expires)
}
func (f *fakeRefreshRepo) Rotate(ctx context.Context, oldHash, newHash string, expires time.Time) (int64, int64, error) {
	if f.RotateFn == nil {
		panic("fakeRefreshRepo.Rotate chamado sem mock")
	}
	return f.RotateFn(ctx, oldHash, newHash, expires)
}
func (f *fakeRefreshRepo) Revoke(ctx context.Context, hash string) error {
	if f.RevokeFn == nil {
		return nil
	}
	return f.RevokeFn(ctx, hash)
}
func (f *fakeRefreshRepo) RevokeAllForUser(ctx context.Context, userID int64) error {
	if f.RevokeAllForUserFn == nil {
		return nil
	}
	return f.RevokeAllForUserFn(ctx, userID)
}

// ============================================================
// Mock: reviewRepository (ReviewHandler)
// ============================================================

type fakeReviewRepo struct {
	CreateFn  func(ctx context.Context, assetID, userID int64, rating int, comment string) (*domain.Review, error)
	UpdateFn  func(ctx context.Context, reviewID, userID int64, rating int, comment string) (*domain.Review, error)
	DeleteFn  func(ctx context.Context, reviewID, userID int64) error
	ListFn    func(ctx context.Context, assetID int64) ([]*domain.Review, error)
	SummaryFn func(ctx context.Context, assetID int64) (*domain.ReviewSummary, error)
}

func (f *fakeReviewRepo) Create(ctx context.Context, assetID, userID int64, rating int, comment string) (*domain.Review, error) {
	if f.CreateFn == nil {
		panic("fakeReviewRepo.Create chamado sem mock configurado")
	}
	return f.CreateFn(ctx, assetID, userID, rating, comment)
}
func (f *fakeReviewRepo) Update(ctx context.Context, reviewID, userID int64, rating int, comment string) (*domain.Review, error) {
	if f.UpdateFn == nil {
		panic("fakeReviewRepo.Update chamado sem mock configurado")
	}
	return f.UpdateFn(ctx, reviewID, userID, rating, comment)
}
func (f *fakeReviewRepo) Delete(ctx context.Context, reviewID, userID int64) error {
	if f.DeleteFn == nil {
		panic("fakeReviewRepo.Delete chamado sem mock configurado")
	}
	return f.DeleteFn(ctx, reviewID, userID)
}
func (f *fakeReviewRepo) ListByAsset(ctx context.Context, assetID int64) ([]*domain.Review, error) {
	if f.ListFn == nil {
		panic("fakeReviewRepo.ListByAsset chamado sem mock configurado")
	}
	return f.ListFn(ctx, assetID)
}
func (f *fakeReviewRepo) Summary(ctx context.Context, assetID int64) (*domain.ReviewSummary, error) {
	if f.SummaryFn == nil {
		panic("fakeReviewRepo.Summary chamado sem mock configurado")
	}
	return f.SummaryFn(ctx, assetID)
}

// ============================================================
// Mock: purchaseCheck (compartilhado entre Cart/Review)
// ============================================================

type fakePurchaseCheck struct {
	IsPurchasedFn func(ctx context.Context, userID, assetID int64) (bool, error)
}

func (f *fakePurchaseCheck) IsPurchased(ctx context.Context, userID, assetID int64) (bool, error) {
	if f.IsPurchasedFn == nil {
		panic("fakePurchaseCheck.IsPurchased chamado sem mock configurado")
	}
	return f.IsPurchasedFn(ctx, userID, assetID)
}

// ============================================================
// Mock: cartRepository (CartHandler)
// ============================================================

type fakeCartRepo struct {
	AddAssetFn       func(ctx context.Context, userID, assetID int64) error
	AddPackFn        func(ctx context.Context, userID, packID int64) error
	RemoveAssetFn    func(ctx context.Context, userID, assetID int64) error
	RemovePackFn     func(ctx context.Context, userID, packID int64) error
	ClearFn          func(ctx context.Context, userID int64) error
	ListAssetsFn     func(ctx context.Context, userID int64) ([]*domain.Asset, error)
	ListPacksFn      func(ctx context.Context, userID int64) ([]*domain.Pack, error)
	ListAssetIDsFn   func(ctx context.Context, userID int64) ([]int64, error)
	ListPackIDsFn    func(ctx context.Context, userID int64) ([]int64, error)
}

func (f *fakeCartRepo) AddAsset(ctx context.Context, userID, assetID int64) error {
	if f.AddAssetFn == nil {
		panic("fakeCartRepo.AddAsset chamado sem mock configurado")
	}
	return f.AddAssetFn(ctx, userID, assetID)
}
func (f *fakeCartRepo) AddPack(ctx context.Context, userID, packID int64) error {
	if f.AddPackFn == nil {
		panic("fakeCartRepo.AddPack chamado sem mock configurado")
	}
	return f.AddPackFn(ctx, userID, packID)
}
func (f *fakeCartRepo) RemoveAsset(ctx context.Context, userID, assetID int64) error {
	if f.RemoveAssetFn == nil {
		panic("fakeCartRepo.RemoveAsset chamado sem mock configurado")
	}
	return f.RemoveAssetFn(ctx, userID, assetID)
}
func (f *fakeCartRepo) RemovePack(ctx context.Context, userID, packID int64) error {
	if f.RemovePackFn == nil {
		panic("fakeCartRepo.RemovePack chamado sem mock configurado")
	}
	return f.RemovePackFn(ctx, userID, packID)
}
func (f *fakeCartRepo) Clear(ctx context.Context, userID int64) error {
	if f.ClearFn == nil {
		panic("fakeCartRepo.Clear chamado sem mock configurado")
	}
	return f.ClearFn(ctx, userID)
}
func (f *fakeCartRepo) ListAssetsByUser(ctx context.Context, userID int64) ([]*domain.Asset, error) {
	if f.ListAssetsFn == nil {
		panic("fakeCartRepo.ListAssetsByUser chamado sem mock configurado")
	}
	return f.ListAssetsFn(ctx, userID)
}
func (f *fakeCartRepo) ListPacksByUser(ctx context.Context, userID int64) ([]*domain.Pack, error) {
	if f.ListPacksFn == nil {
		panic("fakeCartRepo.ListPacksByUser chamado sem mock configurado")
	}
	return f.ListPacksFn(ctx, userID)
}
func (f *fakeCartRepo) ListAssetIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	if f.ListAssetIDsFn == nil {
		panic("fakeCartRepo.ListAssetIDsByUser chamado sem mock configurado")
	}
	return f.ListAssetIDsFn(ctx, userID)
}
func (f *fakeCartRepo) ListPackIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	if f.ListPackIDsFn == nil {
		panic("fakeCartRepo.ListPackIDsByUser chamado sem mock configurado")
	}
	return f.ListPackIDsFn(ctx, userID)
}

// ============================================================
// Mock: purchaseRepository (CartHandler) + notificationSink
// ============================================================

type fakePurchaseRepo struct {
	CheckoutFn       func(ctx context.Context, userID int64) (*domain.CheckoutSession, error)
	FindSessionFn    func(ctx context.Context, sessionID string, userID int64) (*domain.CheckoutSession, error)
	ConfirmSessionFn func(ctx context.Context, sessionID string, userID int64) (*domain.CheckoutSession, bool, error)
	ListFn           func(ctx context.Context, userID int64) ([]*domain.Purchase, error)
	ListIDsFn        func(ctx context.Context, userID int64) ([]int64, error)
	SellerStatsFn    func(ctx context.Context, sellerID int64, recentLimit int) (*domain.SellerStats, error)
}

func (f *fakePurchaseRepo) Checkout(ctx context.Context, userID int64) (*domain.CheckoutSession, error) {
	if f.CheckoutFn == nil {
		panic("fakePurchaseRepo.Checkout chamado sem mock configurado")
	}
	return f.CheckoutFn(ctx, userID)
}
func (f *fakePurchaseRepo) FindSession(ctx context.Context, sessionID string, userID int64) (*domain.CheckoutSession, error) {
	if f.FindSessionFn == nil {
		panic("fakePurchaseRepo.FindSession chamado sem mock configurado")
	}
	return f.FindSessionFn(ctx, sessionID, userID)
}
func (f *fakePurchaseRepo) ConfirmSession(ctx context.Context, sessionID string, userID int64) (*domain.CheckoutSession, bool, error) {
	if f.ConfirmSessionFn == nil {
		panic("fakePurchaseRepo.ConfirmSession chamado sem mock configurado")
	}
	return f.ConfirmSessionFn(ctx, sessionID, userID)
}
func (f *fakePurchaseRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.Purchase, error) {
	if f.ListFn == nil {
		panic("fakePurchaseRepo.ListByUser chamado sem mock configurado")
	}
	return f.ListFn(ctx, userID)
}
func (f *fakePurchaseRepo) ListPurchasedIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	if f.ListIDsFn == nil {
		panic("fakePurchaseRepo.ListPurchasedIDsByUser chamado sem mock configurado")
	}
	return f.ListIDsFn(ctx, userID)
}
func (f *fakePurchaseRepo) SellerStats(ctx context.Context, sellerID int64, recentLimit int) (*domain.SellerStats, error) {
	if f.SellerStatsFn == nil {
		panic("fakePurchaseRepo.SellerStats chamado sem mock configurado")
	}
	return f.SellerStatsFn(ctx, sellerID, recentLimit)
}

// fakeNotificationSink: registra cada chamada pra que o teste
// possa verificar que os hooks foram disparados pós-Checkout/Review.
type fakeNotificationSink struct {
	SoldAssetsFn         func(ctx context.Context, buyerID int64, purchaseIDs []int64) error
	BuyerPurchasesFn     func(ctx context.Context, buyerID int64, purchaseIDs []int64) error
	ForReviewFn          func(ctx context.Context, reviewerID, assetID int64) error
	SoldAssetsCalls      [][]int64 // captura purchaseIDs do hook do vendedor
	BuyerPurchasesCalls  [][]int64 // captura purchaseIDs do hook do comprador
	ForReviewCalls       [][2]int64
}

func (f *fakeNotificationSink) CreateForSoldAssets(ctx context.Context, buyerID int64, purchaseIDs []int64) error {
	f.SoldAssetsCalls = append(f.SoldAssetsCalls, purchaseIDs)
	if f.SoldAssetsFn != nil {
		return f.SoldAssetsFn(ctx, buyerID, purchaseIDs)
	}
	return nil
}
func (f *fakeNotificationSink) CreateForBuyerPurchases(ctx context.Context, buyerID int64, purchaseIDs []int64) error {
	f.BuyerPurchasesCalls = append(f.BuyerPurchasesCalls, purchaseIDs)
	if f.BuyerPurchasesFn != nil {
		return f.BuyerPurchasesFn(ctx, buyerID, purchaseIDs)
	}
	return nil
}
func (f *fakeNotificationSink) CreateForReview(ctx context.Context, reviewerID, assetID int64) error {
	f.ForReviewCalls = append(f.ForReviewCalls, [2]int64{reviewerID, assetID})
	if f.ForReviewFn != nil {
		return f.ForReviewFn(ctx, reviewerID, assetID)
	}
	return nil
}

// ============================================================
// Mock: assetRepository (AssetHandler)
// ============================================================

type fakeAssetRepo struct {
	CreateFn          func(ctx context.Context, ownerID int64, title, description string, tags []string, priceCents int64, thumbnailPath, modelPath string) (*domain.Asset, error)
	FindByIDFn        func(ctx context.Context, id int64) (*domain.Asset, error)
	ListFn            func(ctx context.Context) ([]*domain.Asset, error)
	ListPaginatedFn   func(ctx context.Context, page, pageSize int) ([]*domain.Asset, int64, error)
	ListByOwnerFn     func(ctx context.Context, ownerID int64) ([]*domain.Asset, error)
	UpdateFn          func(ctx context.Context, id, ownerID int64, title, description string, tags []string, priceCents int64) (*domain.Asset, error)
	UpdateThumbnailFn func(ctx context.Context, id, ownerID int64, newPath string) (string, error)
	UpdateModelFn     func(ctx context.Context, id, ownerID int64, newPath string) (string, error)
	DeleteFn          func(ctx context.Context, id, ownerID int64) (string, string, error)
	TagsFn            func(ctx context.Context) ([]*domain.TagCount, error)
	SimilarFn         func(ctx context.Context, assetID int64, limit int) ([]*domain.Asset, error)
	TrendingFn        func(ctx context.Context, limit int) ([]*domain.Asset, error)
}

func (f *fakeAssetRepo) Create(ctx context.Context, ownerID int64, title, description string, tags []string, priceCents int64, thumbnailPath, modelPath string) (*domain.Asset, error) {
	if f.CreateFn == nil {
		panic("fakeAssetRepo.Create chamado sem mock configurado")
	}
	return f.CreateFn(ctx, ownerID, title, description, tags, priceCents, thumbnailPath, modelPath)
}
func (f *fakeAssetRepo) FindByID(ctx context.Context, id int64) (*domain.Asset, error) {
	if f.FindByIDFn == nil {
		panic("fakeAssetRepo.FindByID chamado sem mock configurado")
	}
	return f.FindByIDFn(ctx, id)
}
func (f *fakeAssetRepo) List(ctx context.Context) ([]*domain.Asset, error) {
	if f.ListFn == nil {
		panic("fakeAssetRepo.List chamado sem mock configurado")
	}
	return f.ListFn(ctx)
}
func (f *fakeAssetRepo) ListPaginated(ctx context.Context, page, pageSize int) ([]*domain.Asset, int64, error) {
	if f.ListPaginatedFn == nil {
		panic("fakeAssetRepo.ListPaginated chamado sem mock configurado")
	}
	return f.ListPaginatedFn(ctx, page, pageSize)
}
func (f *fakeAssetRepo) ListByOwner(ctx context.Context, ownerID int64) ([]*domain.Asset, error) {
	if f.ListByOwnerFn == nil {
		panic("fakeAssetRepo.ListByOwner chamado sem mock configurado")
	}
	return f.ListByOwnerFn(ctx, ownerID)
}
func (f *fakeAssetRepo) Update(ctx context.Context, id, ownerID int64, title, description string, tags []string, priceCents int64) (*domain.Asset, error) {
	if f.UpdateFn == nil {
		panic("fakeAssetRepo.Update chamado sem mock configurado")
	}
	return f.UpdateFn(ctx, id, ownerID, title, description, tags, priceCents)
}
func (f *fakeAssetRepo) UpdateThumbnail(ctx context.Context, id, ownerID int64, newPath string) (string, error) {
	if f.UpdateThumbnailFn == nil {
		panic("fakeAssetRepo.UpdateThumbnail chamado sem mock configurado")
	}
	return f.UpdateThumbnailFn(ctx, id, ownerID, newPath)
}
func (f *fakeAssetRepo) UpdateModel(ctx context.Context, id, ownerID int64, newPath string) (string, error) {
	if f.UpdateModelFn == nil {
		panic("fakeAssetRepo.UpdateModel chamado sem mock configurado")
	}
	return f.UpdateModelFn(ctx, id, ownerID, newPath)
}
func (f *fakeAssetRepo) Delete(ctx context.Context, id, ownerID int64) (string, string, error) {
	if f.DeleteFn == nil {
		panic("fakeAssetRepo.Delete chamado sem mock configurado")
	}
	return f.DeleteFn(ctx, id, ownerID)
}
func (f *fakeAssetRepo) ListTagsWithCounts(ctx context.Context) ([]*domain.TagCount, error) {
	if f.TagsFn == nil {
		panic("fakeAssetRepo.ListTagsWithCounts chamado sem mock configurado")
	}
	return f.TagsFn(ctx)
}
func (f *fakeAssetRepo) ListSimilar(ctx context.Context, assetID int64, limit int) ([]*domain.Asset, error) {
	if f.SimilarFn == nil {
		panic("fakeAssetRepo.ListSimilar chamado sem mock configurado")
	}
	return f.SimilarFn(ctx, assetID, limit)
}
func (f *fakeAssetRepo) ListTrending(ctx context.Context, limit int) ([]*domain.Asset, error) {
	if f.TrendingFn == nil {
		panic("fakeAssetRepo.ListTrending chamado sem mock configurado")
	}
	return f.TrendingFn(ctx, limit)
}

// fakeFileStorage atende a interface fileStorage do AssetHandler.
// Pra testes que não exercitam upload, todos os métodos panic se
// chamados — confirma que os caminhos testados não tocam em I/O.
type fakeFileStorage struct {
	SaveThumbnailFn func(fh *multipart.FileHeader) (string, error)
	SaveModelFn     func(fh *multipart.FileHeader) (string, error)
	RemoveFn        func(relPath string) error
}

func (f *fakeFileStorage) SaveThumbnail(fh *multipart.FileHeader) (string, error) {
	if f.SaveThumbnailFn == nil {
		panic("fakeFileStorage.SaveThumbnail chamado sem mock configurado")
	}
	return f.SaveThumbnailFn(fh)
}
func (f *fakeFileStorage) SaveModel(fh *multipart.FileHeader) (string, error) {
	if f.SaveModelFn == nil {
		panic("fakeFileStorage.SaveModel chamado sem mock configurado")
	}
	return f.SaveModelFn(fh)
}
func (f *fakeFileStorage) Remove(relPath string) error {
	if f.RemoveFn == nil {
		// Remove é chamado em cleanup; OK ser no-op silencioso.
		return nil
	}
	return f.RemoveFn(relPath)
}

// ============================================================
// Mock: userProfileRepository (UserHandler)
// ============================================================

type fakeUserProfileRepo struct {
	FindByIDFn         func(ctx context.Context, id int64) (*domain.User, error)
	FindByUsernameFn   func(ctx context.Context, username string) (*domain.User, error)
	UpdateProfileFn    func(ctx context.Context, id int64, displayName, bio string) (*domain.User, error)
	SetAvatarFn        func(ctx context.Context, id int64, newPath string) (string, error)
	ClearAvatarFn      func(ctx context.Context, id int64) (string, error)
	ListWithCountFn    func(ctx context.Context, limit int) ([]*domain.PublicUser, error)
	ListWithCountPageFn func(ctx context.Context, page, pageSize int) ([]*domain.PublicUser, int64, error)
}

func (f *fakeUserProfileRepo) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	if f.FindByIDFn == nil {
		panic("fakeUserProfileRepo.FindByID chamado sem mock")
	}
	return f.FindByIDFn(ctx, id)
}
func (f *fakeUserProfileRepo) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	if f.FindByUsernameFn == nil {
		panic("fakeUserProfileRepo.FindByUsername chamado sem mock")
	}
	return f.FindByUsernameFn(ctx, username)
}
func (f *fakeUserProfileRepo) UpdateProfile(ctx context.Context, id int64, displayName, bio string) (*domain.User, error) {
	if f.UpdateProfileFn == nil {
		panic("fakeUserProfileRepo.UpdateProfile chamado sem mock")
	}
	return f.UpdateProfileFn(ctx, id, displayName, bio)
}
func (f *fakeUserProfileRepo) SetAvatar(ctx context.Context, id int64, newPath string) (string, error) {
	if f.SetAvatarFn == nil {
		panic("fakeUserProfileRepo.SetAvatar chamado sem mock")
	}
	return f.SetAvatarFn(ctx, id, newPath)
}
func (f *fakeUserProfileRepo) ClearAvatar(ctx context.Context, id int64) (string, error) {
	if f.ClearAvatarFn == nil {
		panic("fakeUserProfileRepo.ClearAvatar chamado sem mock")
	}
	return f.ClearAvatarFn(ctx, id)
}
func (f *fakeUserProfileRepo) ListWithAssetCount(ctx context.Context, limit int) ([]*domain.PublicUser, error) {
	if f.ListWithCountFn == nil {
		panic("fakeUserProfileRepo.ListWithAssetCount chamado sem mock")
	}
	return f.ListWithCountFn(ctx, limit)
}
func (f *fakeUserProfileRepo) ListWithAssetCountPaginated(ctx context.Context, page, pageSize int) ([]*domain.PublicUser, int64, error) {
	if f.ListWithCountPageFn == nil {
		panic("fakeUserProfileRepo.ListWithAssetCountPaginated chamado sem mock")
	}
	return f.ListWithCountPageFn(ctx, page, pageSize)
}

// avatarStorage do UserHandler — minimal subset do fileStorage.
type fakeAvatarStorage struct {
	SaveAvatarFn func(fh *multipart.FileHeader) (string, error)
	RemoveFn     func(relPath string) error
}

func (f *fakeAvatarStorage) SaveAvatar(fh *multipart.FileHeader) (string, error) {
	if f.SaveAvatarFn == nil {
		panic("fakeAvatarStorage.SaveAvatar chamado sem mock")
	}
	return f.SaveAvatarFn(fh)
}
func (f *fakeAvatarStorage) Remove(relPath string) error {
	if f.RemoveFn == nil {
		return nil
	}
	return f.RemoveFn(relPath)
}

// ============================================================
// Mock: favoriteRepository (FavoriteHandler)
// ============================================================

type fakeFavoriteRepo struct {
	AddFn       func(ctx context.Context, userID, assetID int64) error
	RemoveFn    func(ctx context.Context, userID, assetID int64) error
	ListFn      func(ctx context.Context, userID int64) ([]*domain.Asset, error)
	ListIDsFn   func(ctx context.Context, userID int64) ([]int64, error)
}

func (f *fakeFavoriteRepo) Add(ctx context.Context, userID, assetID int64) error {
	if f.AddFn == nil {
		panic("fakeFavoriteRepo.Add chamado sem mock")
	}
	return f.AddFn(ctx, userID, assetID)
}
func (f *fakeFavoriteRepo) Remove(ctx context.Context, userID, assetID int64) error {
	if f.RemoveFn == nil {
		panic("fakeFavoriteRepo.Remove chamado sem mock")
	}
	return f.RemoveFn(ctx, userID, assetID)
}
func (f *fakeFavoriteRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.Asset, error) {
	if f.ListFn == nil {
		panic("fakeFavoriteRepo.ListByUser chamado sem mock")
	}
	return f.ListFn(ctx, userID)
}
func (f *fakeFavoriteRepo) ListIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	if f.ListIDsFn == nil {
		panic("fakeFavoriteRepo.ListIDsByUser chamado sem mock")
	}
	return f.ListIDsFn(ctx, userID)
}

// ============================================================
// Mock: notificationRepository (NotificationHandler)
// ============================================================

type fakeNotificationRepo struct {
	ListFn         func(ctx context.Context, userID int64, limit int) ([]*domain.Notification, error)
	UnreadCountFn  func(ctx context.Context, userID int64) (int64, error)
	MarkAllReadFn  func(ctx context.Context, userID int64) error
}

func (f *fakeNotificationRepo) ListByUser(ctx context.Context, userID int64, limit int) ([]*domain.Notification, error) {
	if f.ListFn == nil {
		panic("fakeNotificationRepo.ListByUser chamado sem mock")
	}
	return f.ListFn(ctx, userID, limit)
}
func (f *fakeNotificationRepo) UnreadCount(ctx context.Context, userID int64) (int64, error) {
	if f.UnreadCountFn == nil {
		panic("fakeNotificationRepo.UnreadCount chamado sem mock")
	}
	return f.UnreadCountFn(ctx, userID)
}
func (f *fakeNotificationRepo) MarkAllRead(ctx context.Context, userID int64) error {
	if f.MarkAllReadFn == nil {
		panic("fakeNotificationRepo.MarkAllRead chamado sem mock")
	}
	return f.MarkAllReadFn(ctx, userID)
}

// ============================================================
// Mock: packRepository (PackHandler)
// ============================================================

type fakePackRepo struct {
	CreateFn          func(ctx context.Context, ownerID int64, title, description string, price int64, thumb string, assetIDs []int64) (*domain.Pack, error)
	FindByIDFn        func(ctx context.Context, id int64) (*domain.Pack, error)
	ListFn            func(ctx context.Context, page, pageSize int) ([]*domain.Pack, int64, error)
	ListByOwnerFn     func(ctx context.Context, ownerID int64) ([]*domain.Pack, error)
	ListByAssetIDFn   func(ctx context.Context, assetID int64) ([]*domain.Pack, error)
	UpdateFn          func(ctx context.Context, id, ownerID int64, title, description string, price int64, assetIDs []int64) (*domain.Pack, error)
	DeleteFn          func(ctx context.Context, id, ownerID int64) (string, error)
	UpdateThumbnailFn func(ctx context.Context, id, ownerID int64, newPath string) (string, error)
}

func (f *fakePackRepo) Create(ctx context.Context, ownerID int64, title, description string, price int64, thumb string, assetIDs []int64) (*domain.Pack, error) {
	if f.CreateFn == nil {
		panic("fakePackRepo.Create chamado sem mock")
	}
	return f.CreateFn(ctx, ownerID, title, description, price, thumb, assetIDs)
}
func (f *fakePackRepo) FindByID(ctx context.Context, id int64) (*domain.Pack, error) {
	if f.FindByIDFn == nil {
		panic("fakePackRepo.FindByID chamado sem mock")
	}
	return f.FindByIDFn(ctx, id)
}
func (f *fakePackRepo) List(ctx context.Context, page, pageSize int) ([]*domain.Pack, int64, error) {
	if f.ListFn == nil {
		panic("fakePackRepo.List chamado sem mock")
	}
	return f.ListFn(ctx, page, pageSize)
}
func (f *fakePackRepo) ListByOwner(ctx context.Context, ownerID int64) ([]*domain.Pack, error) {
	if f.ListByOwnerFn == nil {
		panic("fakePackRepo.ListByOwner chamado sem mock")
	}
	return f.ListByOwnerFn(ctx, ownerID)
}
func (f *fakePackRepo) ListByAssetID(ctx context.Context, assetID int64) ([]*domain.Pack, error) {
	if f.ListByAssetIDFn == nil {
		panic("fakePackRepo.ListByAssetID chamado sem mock")
	}
	return f.ListByAssetIDFn(ctx, assetID)
}
func (f *fakePackRepo) Update(ctx context.Context, id, ownerID int64, title, description string, price int64, assetIDs []int64) (*domain.Pack, error) {
	if f.UpdateFn == nil {
		panic("fakePackRepo.Update chamado sem mock")
	}
	return f.UpdateFn(ctx, id, ownerID, title, description, price, assetIDs)
}
func (f *fakePackRepo) Delete(ctx context.Context, id, ownerID int64) (string, error) {
	if f.DeleteFn == nil {
		panic("fakePackRepo.Delete chamado sem mock")
	}
	return f.DeleteFn(ctx, id, ownerID)
}
func (f *fakePackRepo) UpdateThumbnail(ctx context.Context, id, ownerID int64, newPath string) (string, error) {
	if f.UpdateThumbnailFn == nil {
		panic("fakePackRepo.UpdateThumbnail chamado sem mock")
	}
	return f.UpdateThumbnailFn(ctx, id, ownerID, newPath)
}

// ============================================================
// Asserts curtos
// ============================================================

func assertStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status: want %d, got %d (body: %s)", want, w.Code, w.Body.String())
	}
}

func assertJSONString(t *testing.T, w *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	body := decodeJSON(t, w)
	got, ok := body[key].(string)
	if !ok {
		t.Fatalf("key %q ausente ou não-string em %v", key, body)
	}
	if got != want {
		t.Fatalf("key %q: want %q, got %q", key, want, got)
	}
}

// suppress: usado em tabelas de teste pra forçar uso de http import
// quando algum case não chama doJSON direto. Sem isso, gofmt remove
// imports não usados em algumas configs.
var _ = http.StatusOK
