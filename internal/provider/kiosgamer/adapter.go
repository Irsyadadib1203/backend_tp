package kiosgamer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

const (
	defaultAppID = 100067
	codmAppID    = 100054
)

type Adapter struct{ service service.KiosgamerService }

func New(s service.KiosgamerService) *Adapter { return &Adapter{service: s} }
func (a *Adapter) Code() string               { return provider.KiosgamerCode }

func (a *Adapter) Purchase(ctx context.Context, r provider.PurchaseRequest) (*provider.Result, error) {
	// Safe-retry: if an order already exists, poll its status without placing a new order.
	// This check must precede the SKU guard so that existing orders can be polled
	// even when ProductCode is not passed through (idempotency requirement §3.3, §8).
	if r.ExistingProviderOrderID != "" && r.ExistingProviderOrderID != "-" {
		return a.checkStatus(ctx, provider.StatusRequest{RefID: r.RefID, ProductCode: r.ProductCode, CustomerID: r.CustomerID, GameSlug: r.GameSlug, ProviderOrderID: r.ExistingProviderOrderID}, false)
	}
	// Only new orders require a configured product code.
	if strings.TrimSpace(r.ProductCode) == "" {
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration, ProviderStatus: "Konfigurasi Error", Message: fmt.Sprintf("SKU Kiosgamer belum dikonfigurasi untuk '%s'. Silakan isi item_id di Nominals lalu retry.", r.ProductName)}
	}
	result, err := a.service.PlaceOrder(ctx, r.RefID, r.ProductCode, r.CustomerID, r.ServerID, r.GameSlug)
	if err != nil {
		return nil, classifyError(err)
	}
	return mapResult(result, false), nil
}

func (a *Adapter) CheckStatus(ctx context.Context, r provider.StatusRequest) (*provider.Result, error) {
	return a.checkStatus(ctx, r, true)
}

func (a *Adapter) checkStatus(ctx context.Context, r provider.StatusRequest, statusCheck bool) (*provider.Result, error) {
	var (
		result *service.KiosgamerOrderResult
		err    error
	)
	if r.ProviderOrderID != "" && r.ProviderOrderID != "-" {
		result, err = a.service.PollOrder(ctx, r.ProviderOrderID)
	} else {
		result, err = a.service.CheckRecentOrder(ctx, appID(r.GameSlug))
	}
	if err != nil {
		return nil, classifyError(err)
	}
	return mapResult(result, statusCheck), nil
}

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	info, err := a.service.HealthCheck(ctx)
	if err != nil {
		return 0, err
	}
	if info == nil || info.OAuth == nil {
		return 0, errors.New("kiosgamer balance unavailable")
	}
	return info.OAuth.ShellBalance, nil
}

func (a *Adapter) Supports(gameSlug string) bool {
	slug := strings.ToLower(strings.TrimSpace(gameSlug))
	return strings.Contains(slug, "free-fire") || strings.Contains(slug, "freefire") || strings.Contains(slug, "codm") || strings.Contains(slug, "call-of-duty")
}

func appID(gameSlug string) int {
	slug := strings.ToLower(gameSlug)
	if strings.Contains(slug, "codm") || strings.Contains(slug, "call-of-duty") {
		return codmAppID
	}
	return defaultAppID
}

func mapResult(result *service.KiosgamerOrderResult, statusCheck bool) *provider.Result {
	if result == nil {
		return &provider.Result{Status: provider.StatusFailedHold, ProviderStatus: "Provider Pending", Message: "empty Kiosgamer response"}
	}
	raw, _ := json.Marshal(result)
	mapped := &provider.Result{ProviderOrderID: result.OrderID, SN: result.SerialNumber, Message: result.Message, Raw: raw, UpdateSN: !statusCheck}
	switch result.Status {
	case "success":
		mapped.Status, mapped.ProviderStatus = provider.StatusSuccess, "Sukses"
		if statusCheck {
			mapped.StatusReason, mapped.PaymentReferencePolicy = "Kiosgamer: status dicek dan terkonfirmasi sukses", provider.PaymentReferenceAlways
		} else {
			mapped.StatusReason, mapped.IncludeSNInSuccessEvent = "Kiosgamer: top up berhasil diproses", true
		}
	case "failed":
		if isInternalOrProviderBalanceError(result.Message) {
			mapped.Status, mapped.ProviderStatus = provider.StatusFailedHold, "Pending (Kendala Provider)"
			mapped.StatusReason = "Kiosgamer: " + result.Message
		} else {
			mapped.Status, mapped.ProviderStatus = provider.StatusFailedFinal, "Gagal"
			mapped.StatusReason = "Kiosgamer gagal: " + result.Message
			mapped.RefundReason = "Pengembalian dana: top up Kiosgamer gagal"
			mapped.IncludeCompletedAtInFailedEvent = !statusCheck
		}
	default:
		mapped.Status, mapped.ProviderStatus = provider.StatusPending, "Pending"
		if !statusCheck {
			mapped.StatusReason = "Kiosgamer: pesanan sedang diproses"
		}
	}
	return mapped
}

// classifyError retains the legacy ordering in transaction_service.go.
func classifyError(err error) *provider.ProviderError {
	message, lower := err.Error(), strings.ToLower(err.Error())
	switch {
	case errors.Is(err, service.ErrKiosgamerChallengeRequired) || strings.Contains(lower, "challenge") || strings.Contains(lower, "anti-bot"):
		return &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorAuthentication, ProviderStatus: "Challenge Required", Message: fmt.Sprintf("Kiosgamer anti-bot challenge: %v", err), Cause: err}
	case errors.Is(err, service.ErrKiosgamerReauthRequired) || errors.Is(err, service.ErrKiosgamerSessionExpired) || errors.Is(err, service.ErrKiosgamerNotConfigured) || strings.Contains(lower, "session") || strings.Contains(lower, "reauth"):
		return &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorAuthentication, ProviderStatus: "Session Error", Message: fmt.Sprintf("Kiosgamer session error: %v", err), Cause: err}
	case containsAny(lower, "saldo", "shell", "balance", "uid", "totp", "preflight", "timeout", "connection"):
		kind := provider.ErrorTemporary
		if strings.Contains(lower, "timeout") || strings.Contains(lower, "connection") {
			kind = provider.ErrorTimeout
		}
		if strings.Contains(lower, "saldo") || strings.Contains(lower, "shell") || strings.Contains(lower, "balance") {
			kind = provider.ErrorBalance
		}
		return &provider.ProviderError{Status: provider.StatusFailedHold, Kind: kind, ProviderStatus: "Provider Pending", Message: fmt.Sprintf("Kiosgamer kendala teknis: %v", err), Cause: err}
	case isUserOrProductFatalError(message):
		return &provider.ProviderError{Status: provider.StatusFailedFinal, Kind: provider.ErrorFinal, ProviderStatus: "Gagal", Message: fmt.Sprintf("Kiosgamer gagal: %v", err), Cause: err, PersistBeforeStatus: true}
	default:
		return &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary, ProviderStatus: "Provider Pending", Message: fmt.Sprintf("Kiosgamer: %v", err), Cause: err}
	}
}

func containsAny(value string, fragments ...string) bool {
	for _, f := range fragments {
		if strings.Contains(value, f) {
			return true
		}
	}
	return false
}

func isInternalOrProviderBalanceError(message string) bool {
	return containsAny(strings.ToLower(message), "saldo", "balance", "shell", "challenge", "captcha", "session", "reauth", "timeout", "timed out", "connection", "preflight", "totp", "uid", "konfigurasi", "modal", "harga naik", "harga modal", "server", "maintenance", "jaringan")
}

func isUserOrProductFatalError(message string) bool {
	return containsAny(strings.ToLower(message), "tidak ditemukan", "not found", "invalid", "salah", "unregistered", "tujuan salah", "nomor salah", "id salah", "user id", "player id", "karakter", "role", "banned", "diblokir", "produk tidak", "product not", "tidak tersedia", "ditutup", "cut off", "out of stock", "gangguan pusat", "tidak aktif")
}
