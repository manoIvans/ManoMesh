package handler

import (
	"context"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/manoIvans/manomesh/internal/domain"
)

// packRepository é o contrato mínimo que o PackHandler exige. Mesma
// motivação dos outros handlers: depender da interface (não da impl
// concreta) torna os tests triviais via fake.
type packRepository interface {
	Create(ctx context.Context, ownerID int64, title, description string, priceCents int64, thumbnailPath string, assetIDs []int64) (*domain.Pack, error)
	FindByID(ctx context.Context, id int64) (*domain.Pack, error)
	List(ctx context.Context, page, pageSize int) ([]*domain.Pack, int64, error)
	ListByOwner(ctx context.Context, ownerID int64) ([]*domain.Pack, error)
	ListByAssetID(ctx context.Context, assetID int64) ([]*domain.Pack, error)
	Update(ctx context.Context, id, ownerID int64, title, description string, priceCents int64, assetIDs []int64) (*domain.Pack, error)
	Delete(ctx context.Context, id, ownerID int64) (string, error)
	UpdateThumbnail(ctx context.Context, id, ownerID int64, newPath string) (string, error)
}

// packStorage usa o subset do fileStorage que o PackHandler precisa.
// SaveThumbnail é reaproveitada do storage de assets (mesma validação
// de tipo PNG/JPG/WEBP, mesmo dir on disk).
type packStorage interface {
	SaveThumbnail(fh *multipart.FileHeader) (string, error)
	Remove(relPath string) error
}

type PackHandler struct {
	packs   packRepository
	storage packStorage
}

func NewPackHandler(packs packRepository, storage packStorage) *PackHandler {
	return &PackHandler{packs: packs, storage: storage}
}

// Limite do body multipart no Create. Thumb é até 8MiB (mesmo cap dos
// outros uploads de imagem); resto é metadado pequeno + asset_ids[].
const maxPackUploadBytes = 8 << 20

// Create cria um pack. Multipart com:
//
//	title, description, price_cents — campos texto
//	asset_ids                       — repetir o campo (asset_ids=1&asset_ids=2…)
//	thumbnail (opcional)            — imagem PNG/JPG/WEBP
//
// Validação:
//   - title 1..200, description ≤ 2000, price ≥ 0
//   - asset_ids: 2..50 ids únicos
//   - todos os asset_ids pertencem ao usuário do JWT (checado no repo)
//
// Em erro: cleanup do thumbnail recém-salvo (se houver) pra não vazar disco.
func (h *PackHandler) Create(c *gin.Context) {
	ownerID, ok := userIDFromContext(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPackUploadBytes)
	if err := c.Request.ParseMultipartForm(2 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "form inválido: " + err.Error()})
		return
	}

	req, err := parsePackForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Thumbnail é opcional — se vier, salva antes do INSERT pra que rollback
	// seja simétrico ao create de asset (cleanup quando passos posteriores
	// falham).
	var thumbPath string
	if fh, err := c.FormFile("thumbnail"); err == nil {
		thumbPath, err = h.storage.SaveThumbnail(fh)
		if err != nil {
			writeStorageError(c, err, "thumbnail")
			return
		}
	}

	pack, err := h.packs.Create(
		c.Request.Context(),
		ownerID,
		req.title, req.description,
		req.priceCents,
		thumbPath,
		req.assetIDs,
	)
	if err != nil {
		if thumbPath != "" {
			if rmErr := h.storage.Remove(thumbPath); rmErr != nil {
				log.Printf("cleanup pack thumb: %v", rmErr)
			}
		}
		switch {
		case errors.Is(err, domain.ErrPackInvalidItems):
			c.JSON(http.StatusBadRequest, gin.H{"error": "items inválidos (verifique se você é dono e se há 2-50 assets únicos)"})
		default:
			serverError(c, "create pack", err, "falha ao criar pack")
		}
		return
	}
	c.JSON(http.StatusCreated, pack)
}

// packForm encapsula o resultado validado do multipart pra que Create
// fique enxuto.
type packForm struct {
	title       string
	description string
	priceCents  int64
	assetIDs    []int64
}

// parsePackForm valida os campos texto do multipart. asset_ids vem como
// array repetido (igual `tags` em asset/Create); converte strings → int64
// e descarta zeros/duplicatas.
func parsePackForm(c *gin.Context) (packForm, error) {
	title := strings.TrimSpace(c.PostForm("title"))
	description := strings.TrimSpace(c.PostForm("description"))
	rawPrice := strings.TrimSpace(c.PostForm("price_cents"))

	if l := len(title); l < 1 || l > 200 {
		return packForm{}, errors.New("title deve ter entre 1 e 200 caracteres")
	}
	if len(description) > 2000 {
		return packForm{}, errors.New("description deve ter no máximo 2000 caracteres")
	}

	price, err := strconv.ParseInt(rawPrice, 10, 64)
	if err != nil || price < 0 {
		return packForm{}, errors.New("price_cents deve ser um inteiro >= 0")
	}

	rawIDs := c.PostFormArray("asset_ids")
	ids, err := parseUniqueInt64s(rawIDs)
	if err != nil {
		return packForm{}, err
	}
	if len(ids) < domain.MinPackItems || len(ids) > domain.MaxPackItems {
		return packForm{}, errors.New("pack precisa de 2 a 50 assets únicos")
	}

	return packForm{
		title:       title,
		description: description,
		priceCents:  price,
		assetIDs:    ids,
	}, nil
}

// parseUniqueInt64s converte strings → int64 descartando zeros,
// negativos e duplicatas. Erro genérico em qualquer valor não-numérico.
func parseUniqueInt64s(raw []string) ([]int64, error) {
	seen := make(map[int64]struct{}, len(raw))
	out := make([]int64, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return nil, errors.New("asset_ids deve conter inteiros positivos")
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}

// updatePackRequest cobre o PUT JSON: substituição completa de metadados
// + items. Frontend reusa o mesmo form que o Create (sem o file).
type updatePackRequest struct {
	Title       string  `json:"title"       binding:"required,min=1,max=200"`
	Description string  `json:"description" binding:"max=2000"`
	PriceCents  int64   `json:"price_cents" binding:"gte=0"`
	AssetIDs    []int64 `json:"asset_ids"   binding:"required,min=2,max=50"`
}

// Update edita metadados + items (PUT). Thumbnail NÃO entra aqui — usar
// PUT /packs/:id/thumbnail dedicado (mantém o padrão de Asset).
func (h *PackHandler) Update(c *gin.Context) {
	ownerID, ok := userIDFromContext(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	var req updatePackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pack, err := h.packs.Update(
		c.Request.Context(),
		id, ownerID,
		strings.TrimSpace(req.Title),
		strings.TrimSpace(req.Description),
		req.PriceCents,
		req.AssetIDs,
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPackNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "pack não encontrado"})
		case errors.Is(err, domain.ErrPackForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "este pack não é seu"})
		case errors.Is(err, domain.ErrPackInvalidItems):
			c.JSON(http.StatusBadRequest, gin.H{"error": "items inválidos"})
		default:
			serverError(c, "update pack", err, "falha ao atualizar pack")
		}
		return
	}
	c.JSON(http.StatusOK, pack)
}

// Delete remove o pack e o arquivo de thumbnail (se houver). Items são
// removidos por CASCADE no schema.
func (h *PackHandler) Delete(c *gin.Context) {
	ownerID, ok := userIDFromContext(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	thumb, err := h.packs.Delete(c.Request.Context(), id, ownerID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPackNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "pack não encontrado"})
		case errors.Is(err, domain.ErrPackForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "este pack não é seu"})
		default:
			serverError(c, "delete pack", err, "falha ao excluir pack")
		}
		return
	}
	if thumb != "" {
		if rmErr := h.storage.Remove(thumb); rmErr != nil {
			log.Printf("remove pack thumb on delete %q: %v", thumb, rmErr)
		}
	}
	c.Status(http.StatusNoContent)
}

// GetByID público — devolve pack com items aninhados pra que o frontend
// monte a página de detalhe sem N+1.
func (h *PackHandler) GetByID(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	pack, err := h.packs.FindByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrPackNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pack não encontrado"})
			return
		}
		serverError(c, "get pack", err, "falha ao buscar pack")
		return
	}
	c.JSON(http.StatusOK, pack)
}

// List público — devolve packs paginados (envelope obrigatório, sem
// dual-mode; é entidade nova, não precisa compat). Default page_size=20,
// cap 100 via parsePagination.
func (h *PackHandler) List(c *gin.Context) {
	// Sem ?page=, default page=1 e mantém envelope (sem modo legado).
	pg, ps, asked, ok := parsePagination(c)
	if !ok {
		return
	}
	if !asked {
		pg, ps = 1, defaultPageSize
	}

	items, total, err := h.packs.List(c.Request.Context(), pg, ps)
	if err != nil {
		serverError(c, "list packs", err, "falha ao listar packs")
		return
	}
	c.JSON(http.StatusOK, page[*domain.Pack]{
		Items: items, Page: pg, PageSize: ps, Total: total,
	})
}

// ByAssetID público: packs que CONTÊM o asset. Alimenta o badge
// "também faz parte do pack X" no AssetDetail. Sem items aninhados
// (caller já está olhando o asset). 200 com [] quando o asset não
// está em nenhum pack.
func (h *PackHandler) ByAssetID(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	packs, err := h.packs.ListByAssetID(c.Request.Context(), id)
	if err != nil {
		serverError(c, "list packs by asset", err, "falha ao listar packs do asset")
		return
	}
	c.JSON(http.StatusOK, packs)
}

// MyPacks: packs do vendedor logado. Sem paginação — vendedor não cria
// 100 packs (catálogos pessoais são pequenos).
func (h *PackHandler) MyPacks(c *gin.Context) {
	ownerID, ok := userIDFromContext(c)
	if !ok {
		return
	}
	packs, err := h.packs.ListByOwner(c.Request.Context(), ownerID)
	if err != nil {
		serverError(c, "list my packs", err, "falha ao listar seus packs")
		return
	}
	c.JSON(http.StatusOK, packs)
}

// ReplaceThumbnail troca o arquivo físico — mesmo padrão de
// ReplaceThumbnail do asset (multipart, salva novo, UPDATE DB,
// remove antigo, rollback do novo se DB falha).
func (h *PackHandler) ReplaceThumbnail(c *gin.Context) {
	ownerID, ok := userIDFromContext(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxReplaceThumbnailBytes)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "form inválido: " + err.Error()})
		return
	}

	fh, err := c.FormFile("thumbnail")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "campo 'thumbnail' é obrigatório"})
		return
	}

	newPath, err := h.storage.SaveThumbnail(fh)
	if err != nil {
		writeStorageError(c, err, "thumbnail")
		return
	}

	oldPath, err := h.packs.UpdateThumbnail(c.Request.Context(), id, ownerID, newPath)
	if err != nil {
		if rmErr := h.storage.Remove(newPath); rmErr != nil {
			log.Printf("rollback pack thumbnail: %v", rmErr)
		}
		switch {
		case errors.Is(err, domain.ErrPackNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "pack não encontrado"})
		case errors.Is(err, domain.ErrPackForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "este pack não é seu"})
		default:
			serverError(c, "replace pack thumb", err, "falha ao atualizar thumbnail")
		}
		return
	}

	if oldPath != "" {
		if err := h.storage.Remove(oldPath); err != nil {
			log.Printf("remove old pack thumb %q: %v", oldPath, err)
		}
	}

	pack, err := h.packs.FindByID(c.Request.Context(), id)
	if err != nil {
		serverError(c, "reload pack after thumb", err, "falha ao recarregar pack")
		return
	}
	c.JSON(http.StatusOK, pack)
}
