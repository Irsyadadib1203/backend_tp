package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/response"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

// ProviderProductHandler mengelola pemetaan SKU per provider untuk satu nominal
// (tabel provider_products). Berlaku untuk provider mana pun, jadi menambah
// provider baru tidak butuh kolom atau endpoint baru.
type ProviderProductHandler struct {
	products  repository.ProviderProductRepository
	nominals  repository.NominalRepository
	providers repository.ProviderRepository
}

func NewProviderProductHandler(products repository.ProviderProductRepository, nominals repository.NominalRepository, providers repository.ProviderRepository) *ProviderProductHandler {
	return &ProviderProductHandler{products: products, nominals: nominals, providers: providers}
}

type providerProductRequest struct {
	ProviderID   uint   `json:"provider_id"`
	ProviderCode string `json:"provider_code"`
	ProductCode  string `json:"product_code" binding:"required"`
	IsActive     *bool  `json:"is_active"`
	Priority     int    `json:"priority"`
}

func (h *ProviderProductHandler) loadNominal(c *gin.Context) (*domain.Nominal, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "ID nominal tidak valid", nil)
		return nil, false
	}
	nominal, err := h.nominals.FindByID(uint(id))
	if err != nil || nominal == nil {
		response.NotFound(c, "Nominal tidak ditemukan")
		return nil, false
	}
	return nominal, true
}

// List: GET /admin/nominals/:id/provider-products
func (h *ProviderProductHandler) List(c *gin.Context) {
	nominal, ok := h.loadNominal(c)
	if !ok {
		return
	}
	list, err := h.products.ListByNominalID(nominal.ID)
	if err != nil {
		response.InternalServerError(c, "Gagal mengambil pemetaan SKU", err)
		return
	}
	response.Success(c, "Pemetaan SKU provider", list)
}

// Save: PUT /admin/nominals/:id/provider-products  {provider_id|provider_code, product_code}
func (h *ProviderProductHandler) Save(c *gin.Context) {
	nominal, ok := h.loadNominal(c)
	if !ok {
		return
	}
	var req providerProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Data pemetaan SKU tidak valid", err.Error())
		return
	}
	code := strings.TrimSpace(req.ProductCode)
	if code == "" || len(code) > 100 {
		response.BadRequest(c, "product_code wajib diisi (maksimal 100 karakter)", nil)
		return
	}

	var rec *domain.Provider
	var err error
	switch {
	case req.ProviderID > 0:
		rec, err = h.providers.GetByID(req.ProviderID)
	case strings.TrimSpace(req.ProviderCode) != "":
		rec, err = h.providers.GetByCode(strings.ToUpper(strings.TrimSpace(req.ProviderCode)))
	default:
		response.BadRequest(c, "provider_id atau provider_code wajib diisi", nil)
		return
	}
	if err != nil || rec == nil {
		response.NotFound(c, "Provider tidak ditemukan")
		return
	}

	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	if err := h.products.Upsert(&domain.ProviderProduct{
		NominalID:   nominal.ID,
		ProviderID:  rec.ID,
		ProductCode: code,
		Priority:    req.Priority,
		IsActive:    active,
	}); err != nil {
		response.InternalServerError(c, "Gagal menyimpan pemetaan SKU", err)
		return
	}

	// Transisi: kolom lama masih dipakai UI admin dan sinkron harga Digiflazz.
	// Hapus blok ini saat kolom lama dihilangkan.
	switch strings.ToUpper(strings.TrimSpace(rec.Code)) {
	case provider.DigiflazzCode:
		nominal.ProviderProductCode = code
		_ = h.nominals.Update(nominal)
	case provider.KiosgamerCode:
		nominal.KiosgamerProductCode = code
		_ = h.nominals.Update(nominal)
	}

	list, _ := h.products.ListByNominalID(nominal.ID)
	response.Success(c, "Pemetaan SKU tersimpan", list)
}

// Delete: DELETE /admin/nominals/:id/provider-products/:provider_id
func (h *ProviderProductHandler) Delete(c *gin.Context) {
	nominal, ok := h.loadNominal(c)
	if !ok {
		return
	}
	providerID, err := strconv.ParseUint(c.Param("provider_id"), 10, 64)
	if err != nil || providerID == 0 {
		response.BadRequest(c, "ID provider tidak valid", nil)
		return
	}
	list, err := h.products.ListByNominalID(nominal.ID)
	if err != nil {
		response.InternalServerError(c, "Gagal mengambil pemetaan SKU", err)
		return
	}
	for _, pp := range list {
		if pp.ProviderID == uint(providerID) {
			if err := h.products.Delete(pp.ID); err != nil {
				response.InternalServerError(c, "Gagal menghapus pemetaan SKU", err)
				return
			}
			response.Success(c, "Pemetaan SKU dihapus", nil)
			return
		}
	}
	response.NotFound(c, "Pemetaan SKU untuk provider ini tidak ditemukan")
}
