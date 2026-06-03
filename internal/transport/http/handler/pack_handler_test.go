package handler

import (
	"context"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/manoIvans/manomesh/internal/domain"
)

// Testes do PackHandler. Cobre o CRUD inteiro:
//   - Create multipart: sucesso (com/sem thumb), validações de campo,
//     ErrPackInvalidItems (ownership + count) + cleanup do thumb se
//     repo falha
//   - GetByID público
//   - List paginado (envelope obrigatório)
//   - MyPacks (filtrado pelo JWT)
//   - Update JSON: 200, 403, 404, validação
//   - Delete: 200 + cleanup do thumb, 403, 404
//   - ReplaceThumbnail: sucesso, rollback do novo se DB falha

func setupPacks(
	t *testing.T,
	repo *fakePackRepo,
	store *fakeFileStorage,
	authedUserID int64,
) *gin.Engine {
	t.Helper()
	if store == nil {
		store = &fakeFileStorage{}
	}
	h := NewPackHandler(repo, store)
	eng := newTestEngine(t)
	// Públicas
	eng.GET("/packs", h.List)
	eng.GET("/packs/:id", h.GetByID)
	eng.GET("/assets/:id/packs", h.ByAssetID)
	// Protegidas
	eng.POST("/packs", withAuthUser(authedUserID), h.Create)
	eng.PUT("/packs/:id", withAuthUser(authedUserID), h.Update)
	eng.DELETE("/packs/:id", withAuthUser(authedUserID), h.Delete)
	eng.PUT("/packs/:id/thumbnail", withAuthUser(authedUserID), h.ReplaceThumbnail)
	eng.GET("/my/packs", withAuthUser(authedUserID), h.MyPacks)
	return eng
}

// ============================================================
// Create (multipart)
// ============================================================

func TestPackCreate_Success_WithThumbnail(t *testing.T) {
	repo := &fakePackRepo{
		CreateFn: func(_ context.Context, ownerID int64, title, _ string, price int64, thumb string, ids []int64) (*domain.Pack, error) {
			if ownerID != 42 {
				t.Errorf("ownerID: %d", ownerID)
			}
			if title != "Medieval Pack" {
				t.Errorf("title trim falhou: %q", title)
			}
			if price != 9900 {
				t.Errorf("price: %d", price)
			}
			if thumb != "thumbnails/cover.png" {
				t.Errorf("thumb path: %s", thumb)
			}
			if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 5 {
				t.Errorf("asset_ids: want [1 2 5], got %v", ids)
			}
			return &domain.Pack{ID: 99, OwnerID: ownerID, Title: title, PriceCents: price}, nil
		},
	}
	store := &fakeFileStorage{
		SaveThumbnailFn: func(*multipart.FileHeader) (string, error) {
			return "thumbnails/cover.png", nil
		},
	}
	eng := setupPacks(t, repo, store, 42)

	w := doMultipartWithRepeats(t, eng, http.MethodPost, "/packs",
		map[string][]string{
			"title":       {"  Medieval Pack  "},
			"description": {"10 assets de fantasia"},
			"price_cents": {"9900"},
			"asset_ids":   {"1", "2", "5"},
		},
		[]multipartFile{
			{field: "thumbnail", filename: "cover.png", content: []byte("png")},
		},
		"",
	)
	assertStatus(t, w, http.StatusCreated)
}

func TestPackCreate_Success_NoThumbnail(t *testing.T) {
	repo := &fakePackRepo{
		CreateFn: func(_ context.Context, _ int64, _, _ string, _ int64, thumb string, _ []int64) (*domain.Pack, error) {
			if thumb != "" {
				t.Errorf("thumb deveria ser vazio quando sem upload, got %q", thumb)
			}
			return &domain.Pack{ID: 1}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 42)

	w := doMultipartWithRepeats(t, eng, http.MethodPost, "/packs",
		map[string][]string{
			"title":       {"pack"},
			"description": {""},
			"price_cents": {"0"},
			"asset_ids":   {"1", "2"},
		},
		nil,
		"",
	)
	assertStatus(t, w, http.StatusCreated)
}

func TestPackCreate_TooFewItems(t *testing.T) {
	// 1 item só → handler valida antes de tocar no storage/repo.
	eng := setupPacks(t, &fakePackRepo{}, nil, 42)
	w := doMultipartWithRepeats(t, eng, http.MethodPost, "/packs",
		map[string][]string{
			"title":       {"x"},
			"description": {""},
			"price_cents": {"100"},
			"asset_ids":   {"1"},
		},
		nil,
		"",
	)
	assertStatus(t, w, http.StatusBadRequest)
}

func TestPackCreate_InvalidAssetIDs(t *testing.T) {
	// asset_ids não-numérico → handler rejeita.
	eng := setupPacks(t, &fakePackRepo{}, nil, 42)
	w := doMultipartWithRepeats(t, eng, http.MethodPost, "/packs",
		map[string][]string{
			"title":       {"x"},
			"description": {""},
			"price_cents": {"100"},
			"asset_ids":   {"abc", "2"},
		},
		nil,
		"",
	)
	assertStatus(t, w, http.StatusBadRequest)
}

func TestPackCreate_RepoRejectsItems_CleansThumb(t *testing.T) {
	// Thumb salvo, mas repo devolve ErrPackInvalidItems (asset alheio
	// ou inexistente) → handler deve fazer cleanup do thumb antes do 400.
	removed := []string{}
	store := &fakeFileStorage{
		SaveThumbnailFn: func(*multipart.FileHeader) (string, error) {
			return "thumbnails/oops.png", nil
		},
		RemoveFn: func(p string) error {
			removed = append(removed, p)
			return nil
		},
	}
	repo := &fakePackRepo{
		CreateFn: func(_ context.Context, _ int64, _, _ string, _ int64, _ string, _ []int64) (*domain.Pack, error) {
			return nil, domain.ErrPackInvalidItems
		},
	}
	eng := setupPacks(t, repo, store, 42)
	w := doMultipartWithRepeats(t, eng, http.MethodPost, "/packs",
		map[string][]string{
			"title":       {"x"},
			"description": {""},
			"price_cents": {"100"},
			"asset_ids":   {"99", "100"}, // ids não pertencem ao user
		},
		[]multipartFile{
			{field: "thumbnail", filename: "x.png", content: []byte("png")},
		},
		"",
	)
	assertStatus(t, w, http.StatusBadRequest)
	if len(removed) != 1 || removed[0] != "thumbnails/oops.png" {
		t.Errorf("thumb deveria ter sido limpa, got %v", removed)
	}
}

// ============================================================
// GetByID
// ============================================================

func TestPackGetByID_Success(t *testing.T) {
	repo := &fakePackRepo{
		FindByIDFn: func(_ context.Context, id int64) (*domain.Pack, error) {
			if id != 5 {
				t.Errorf("id: %d", id)
			}
			return &domain.Pack{
				ID: 5, Title: "Pack",
				Items: []*domain.Asset{{ID: 1}, {ID: 2}},
			}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/packs/5", nil, "")
	assertStatus(t, w, http.StatusOK)
	assertJSONString(t, w, "title", "Pack")
}

func TestPackGetByID_NotFound(t *testing.T) {
	repo := &fakePackRepo{
		FindByIDFn: func(_ context.Context, _ int64) (*domain.Pack, error) {
			return nil, domain.ErrPackNotFound
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/packs/999", nil, "")
	assertStatus(t, w, http.StatusNotFound)
}

// ============================================================
// List (envelope obrigatório)
// ============================================================

func TestPackList_DefaultPagination(t *testing.T) {
	repo := &fakePackRepo{
		ListFn: func(_ context.Context, page, pageSize int) ([]*domain.Pack, int64, error) {
			if page != 1 || pageSize != 20 {
				t.Errorf("default pagination: page=%d size=%d", page, pageSize)
			}
			return []*domain.Pack{{ID: 1}}, 1, nil
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/packs", nil, "")
	assertStatus(t, w, http.StatusOK)
	body := decodeJSON(t, w)
	if body["total"] != float64(1) {
		t.Errorf("envelope total: %v", body)
	}
}

func TestPackList_WithPage(t *testing.T) {
	repo := &fakePackRepo{
		ListFn: func(_ context.Context, page, pageSize int) ([]*domain.Pack, int64, error) {
			if page != 3 || pageSize != 50 {
				t.Errorf("pagination: page=%d size=%d", page, pageSize)
			}
			return []*domain.Pack{}, 0, nil
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/packs?page=3&page_size=50", nil, "")
	assertStatus(t, w, http.StatusOK)
}

// ============================================================
// MyPacks
// ============================================================

func TestPackByAssetID_Success(t *testing.T) {
	repo := &fakePackRepo{
		ListByAssetIDFn: func(_ context.Context, assetID int64) ([]*domain.Pack, error) {
			if assetID != 42 {
				t.Errorf("assetID: want 42, got %d", assetID)
			}
			return []*domain.Pack{
				{ID: 7, Title: "Medieval Pack"},
				{ID: 8, Title: "Bundle Sci-fi"},
			}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/assets/42/packs", nil, "")
	assertStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "Medieval Pack") {
		t.Errorf("body deveria conter titulo do pack: %s", w.Body.String())
	}
}

func TestPackByAssetID_EmptyArray(t *testing.T) {
	repo := &fakePackRepo{
		ListByAssetIDFn: func(_ context.Context, _ int64) ([]*domain.Pack, error) {
			return []*domain.Pack{}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 0)
	w := doJSON(t, eng, http.MethodGet, "/assets/99/packs", nil, "")
	assertStatus(t, w, http.StatusOK)
	// Garante array `[]` (não null) pra que front possa iterar direto.
	if w.Body.String() != "[]" {
		t.Errorf("array vazio esperado, got %s", w.Body.String())
	}
}

func TestMyPacks_FilteredByJWT(t *testing.T) {
	repo := &fakePackRepo{
		ListByOwnerFn: func(_ context.Context, ownerID int64) ([]*domain.Pack, error) {
			if ownerID != 42 {
				t.Errorf("ownerID: want 42, got %d", ownerID)
			}
			return []*domain.Pack{{ID: 1, OwnerID: ownerID}}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 42)
	w := doJSON(t, eng, http.MethodGet, "/my/packs", nil, "")
	assertStatus(t, w, http.StatusOK)
}

// ============================================================
// Update
// ============================================================

func TestPackUpdate_Success(t *testing.T) {
	repo := &fakePackRepo{
		UpdateFn: func(_ context.Context, id, ownerID int64, title, _ string, price int64, ids []int64) (*domain.Pack, error) {
			if id != 5 || ownerID != 42 {
				t.Errorf("ids: id=%d ownerID=%d", id, ownerID)
			}
			if title != "Updated" || price != 5000 || len(ids) != 2 {
				t.Errorf("args errados")
			}
			return &domain.Pack{ID: id, Title: title}, nil
		},
	}
	eng := setupPacks(t, repo, nil, 42)
	w := doJSON(t, eng, http.MethodPut, "/packs/5", map[string]any{
		"title":       "Updated",
		"description": "novo",
		"price_cents": 5000,
		"asset_ids":   []int{1, 2},
	}, "")
	assertStatus(t, w, http.StatusOK)
}

func TestPackUpdate_Forbidden(t *testing.T) {
	repo := &fakePackRepo{
		UpdateFn: func(_ context.Context, _, _ int64, _, _ string, _ int64, _ []int64) (*domain.Pack, error) {
			return nil, domain.ErrPackForbidden
		},
	}
	eng := setupPacks(t, repo, nil, 42)
	w := doJSON(t, eng, http.MethodPut, "/packs/5", map[string]any{
		"title": "x", "description": "y", "price_cents": 100,
		"asset_ids": []int{1, 2},
	}, "")
	assertStatus(t, w, http.StatusForbidden)
}

func TestPackUpdate_NotFound(t *testing.T) {
	repo := &fakePackRepo{
		UpdateFn: func(_ context.Context, _, _ int64, _, _ string, _ int64, _ []int64) (*domain.Pack, error) {
			return nil, domain.ErrPackNotFound
		},
	}
	eng := setupPacks(t, repo, nil, 42)
	w := doJSON(t, eng, http.MethodPut, "/packs/999", map[string]any{
		"title": "x", "description": "y", "price_cents": 100,
		"asset_ids": []int{1, 2},
	}, "")
	assertStatus(t, w, http.StatusNotFound)
}

func TestPackUpdate_BindingError(t *testing.T) {
	// asset_ids com 1 item só → binding `min=2` rejeita.
	eng := setupPacks(t, &fakePackRepo{}, nil, 42)
	w := doJSON(t, eng, http.MethodPut, "/packs/5", map[string]any{
		"title": "x", "description": "y", "price_cents": 100,
		"asset_ids": []int{1},
	}, "")
	assertStatus(t, w, http.StatusBadRequest)
}

// ============================================================
// Delete
// ============================================================

func TestPackDelete_Success_CleansThumb(t *testing.T) {
	removed := []string{}
	repo := &fakePackRepo{
		DeleteFn: func(_ context.Context, id, ownerID int64) (string, error) {
			if id != 5 || ownerID != 42 {
				t.Errorf("ids: id=%d ownerID=%d", id, ownerID)
			}
			return "thumbnails/old.png", nil
		},
	}
	store := &fakeFileStorage{
		RemoveFn: func(p string) error {
			removed = append(removed, p)
			return nil
		},
	}
	eng := setupPacks(t, repo, store, 42)
	w := doJSON(t, eng, http.MethodDelete, "/packs/5", nil, "")
	assertStatus(t, w, http.StatusNoContent)
	if len(removed) != 1 || removed[0] != "thumbnails/old.png" {
		t.Errorf("thumb deveria ter sido removida, got %v", removed)
	}
}

func TestPackDelete_NoThumb_NoRemove(t *testing.T) {
	// Pack sem thumb (DeleteFn devolve "") → handler NÃO chama Remove.
	removed := []string{}
	repo := &fakePackRepo{
		DeleteFn: func(_ context.Context, _, _ int64) (string, error) {
			return "", nil
		},
	}
	store := &fakeFileStorage{
		RemoveFn: func(p string) error {
			removed = append(removed, p)
			return nil
		},
	}
	eng := setupPacks(t, repo, store, 42)
	w := doJSON(t, eng, http.MethodDelete, "/packs/5", nil, "")
	assertStatus(t, w, http.StatusNoContent)
	if len(removed) != 0 {
		t.Errorf("Remove não deveria ser chamado, got %v", removed)
	}
}

func TestPackDelete_Forbidden(t *testing.T) {
	repo := &fakePackRepo{
		DeleteFn: func(_ context.Context, _, _ int64) (string, error) {
			return "", domain.ErrPackForbidden
		},
	}
	eng := setupPacks(t, repo, nil, 42)
	w := doJSON(t, eng, http.MethodDelete, "/packs/5", nil, "")
	assertStatus(t, w, http.StatusForbidden)
}

// ============================================================
// ReplaceThumbnail
// ============================================================

func TestPackReplaceThumbnail_Success_RemovesOld(t *testing.T) {
	removed := []string{}
	store := &fakeFileStorage{
		SaveThumbnailFn: func(*multipart.FileHeader) (string, error) {
			return "thumbnails/new.png", nil
		},
		RemoveFn: func(p string) error {
			removed = append(removed, p)
			return nil
		},
	}
	repo := &fakePackRepo{
		UpdateThumbnailFn: func(_ context.Context, _, _ int64, newPath string) (string, error) {
			if newPath != "thumbnails/new.png" {
				t.Errorf("newPath: %s", newPath)
			}
			return "thumbnails/old.png", nil
		},
		FindByIDFn: func(_ context.Context, id int64) (*domain.Pack, error) {
			return &domain.Pack{ID: id}, nil
		},
	}
	eng := setupPacks(t, repo, store, 42)
	w := doMultipart(t, eng, http.MethodPut, "/packs/5/thumbnail",
		nil,
		[]multipartFile{
			{field: "thumbnail", filename: "new.png", content: []byte("png")},
		},
		"",
	)
	assertStatus(t, w, http.StatusOK)
	if len(removed) != 1 || removed[0] != "thumbnails/old.png" {
		t.Errorf("antigo deveria ser removido, got %v", removed)
	}
}

func TestPackReplaceThumbnail_DBFails_RollsBackNew(t *testing.T) {
	removed := []string{}
	store := &fakeFileStorage{
		SaveThumbnailFn: func(*multipart.FileHeader) (string, error) {
			return "thumbnails/new.png", nil
		},
		RemoveFn: func(p string) error {
			removed = append(removed, p)
			return nil
		},
	}
	repo := &fakePackRepo{
		UpdateThumbnailFn: func(_ context.Context, _, _ int64, _ string) (string, error) {
			return "", domain.ErrPackForbidden
		},
	}
	eng := setupPacks(t, repo, store, 42)
	w := doMultipart(t, eng, http.MethodPut, "/packs/5/thumbnail",
		nil,
		[]multipartFile{
			{field: "thumbnail", filename: "x.png", content: []byte("png")},
		},
		"",
	)
	assertStatus(t, w, http.StatusForbidden)
	if len(removed) != 1 || removed[0] != "thumbnails/new.png" {
		t.Errorf("novo deveria rollback, got %v", removed)
	}
}
