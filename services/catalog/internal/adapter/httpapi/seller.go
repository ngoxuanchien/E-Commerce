package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

type skuInputJSON struct {
	ID            *string `json:"id"`
	Name          string  `json:"name"`
	Price         *int64  `json:"price"`
	Stock         *int32  `json:"stock"`
	ExpectedStock *int32  `json:"expected_stock"`
	Active        *bool   `json:"active"`
}

// productInputJSON is both ProductInput (create) and ProductPatch (update);
// the domain decides which fields are required.
type productInputJSON struct {
	CategoryID  *string        `json:"category_id"`
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	Status      *string        `json:"status"`
	ImageKeys   []string       `json:"image_keys"`
	Skus        []skuInputJSON `json:"skus"`
}

func (in productInputJSON) toDomain() domain.ProductInput {
	out := domain.ProductInput{
		CategoryID: in.CategoryID, Name: in.Name, Description: in.Description, Status: in.Status, ImageKeys: in.ImageKeys,
	}
	if in.Skus != nil {
		out.Skus = make([]domain.SkuInput, len(in.Skus))
		for i, s := range in.Skus {
			out.Skus[i] = domain.SkuInput{ID: s.ID, Name: s.Name, Price: s.Price, Stock: s.Stock, ExpectedStock: s.ExpectedStock, Active: s.Active}
		}
	}
	return out
}

type uploadRequestJSON struct {
	ContentType string `json:"content_type"`
}

func (h *handlers) listSellerProducts(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := map[string]string{}
	limit := parseLimit(q, f)
	var status *domain.ProductStatus
	if raw := q.Get("status"); raw != "" {
		if st, ok := domain.ParseProductStatus(raw); ok {
			status = &st
		} else {
			f["status"] = "must be one of: draft active archived"
		}
	}
	if len(f) > 0 {
		writeError(w, r, &domain.ValidationError{Fields: f})
		return
	}
	page, err := h.Products.ListMine(r.Context(), uid, status, q.Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPage(page, h.toProductJSON))
}

func (h *handlers) createSellerProduct(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in productInputJSON
	if err := httpx.Decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Products.Create(r.Context(), uid, in.toDomain())
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.toProductJSON(p))
}

func (h *handlers) updateSellerProduct(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	var in productInputJSON
	if err := httpx.Decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Products.Update(r.Context(), uid, id, in.toDomain())
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toProductJSON(p))
}

func (h *handlers) archiveSellerProduct(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	if err := h.Products.Archive(r.Context(), uid, id); err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *handlers) createUpload(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in uploadRequestJSON
	if err := httpx.Decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	u, err := h.Products.PresignUpload(r.Context(), uid, in.ContentType)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, uploadJSON{UploadURL: u.URL, Fields: u.Fields, ObjectKey: u.ObjectKey, PublicURL: u.PublicURL})
}
