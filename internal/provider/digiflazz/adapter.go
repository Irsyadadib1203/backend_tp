package digiflazz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type Adapter struct {
	buyer service.DigiflazzBuyerService
}

func New(buyer service.DigiflazzBuyerService) *Adapter { return &Adapter{buyer: buyer} }

func (a *Adapter) Code() string { return provider.DigiflazzCode }

func (a *Adapter) Purchase(_ context.Context, r provider.PurchaseRequest) (*provider.Result, error) {
	requestRaw, _ := json.Marshal(map[string]interface{}{"action": "purchase", "ref_id": r.RefID, "buyer_sku_code": r.ProductCode, "customer_no": provider.CustomerNumber(r.CustomerID, r.ServerID), "testing": false})
	if r.ExistingProviderOrderID != "" {
		result, err := a.CheckStatus(context.Background(), provider.StatusRequest{
			RefID: r.RefID, ProductCode: r.ProductCode, CustomerID: r.CustomerID,
			GameSlug: r.GameSlug, ProviderOrderID: r.ExistingProviderOrderID,
		})
		if result != nil {
			result.RequestRaw = requestRaw
		}
		return result, err
	}
	resp, err := a.buyer.CreateTransaction(r.RefID, r.ProductCode, provider.CustomerNumber(r.CustomerID, r.ServerID), false)
	if err != nil {
		raw, _ := json.Marshal(map[string]interface{}{"error": err.Error(), "ref_id": r.RefID, "timestamp": time.Now().Format(time.RFC3339)})
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: errorKind(err.Error()), ProviderStatus: "Pending", Message: err.Error(), Cause: err, Raw: raw, RequestRaw: requestRaw}
	}
	result := resultFromResponse(resp, false)
	result.RequestRaw = requestRaw
	return result, nil
}

func (a *Adapter) CheckStatus(_ context.Context, r provider.StatusRequest) (*provider.Result, error) {
	customerNo := provider.CustomerNumber(r.CustomerID, r.ServerID)
	requestRaw, _ := json.Marshal(map[string]interface{}{"action": "check_status", "ref_id": r.RefID, "buyer_sku_code": r.ProductCode, "customer_no": customerNo})
	resp, err := a.buyer.CheckTransactionStatus(r.RefID, r.ProductCode, customerNo)
	if err != nil {
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: errorKind(err.Error()), ProviderStatus: "Pending", Message: err.Error(), Cause: err, RequestRaw: requestRaw}
	}
	result := resultFromResponse(resp, true)
	result.RequestRaw = requestRaw
	return result, nil
}

func (a *Adapter) Balance(context.Context) (float64, error) { return a.buyer.CheckBalance() }

func (a *Adapter) SyncCatalog(context.Context) ([]provider.CatalogItem, error) {
	items, err := a.buyer.GetPriceList()
	if err != nil {
		return nil, err
	}
	result := make([]provider.CatalogItem, 0, len(items))
	for _, item := range items {
		result = append(result, provider.CatalogItem{ProductCode: item.BuyerSkuCode, Name: item.ProductName, BasePrice: item.Price, IsActive: item.BuyerProductStatus})
	}
	return result, nil
}

func (a *Adapter) ParseCallback(r *http.Request) (*provider.Result, string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary, ProviderStatus: "Callback Error", Message: err.Error(), Cause: err}
	}
	signature := r.Header.Get("X-Hub-Signature")
	if signature == "" {
		signature = r.Header.Get("X-Digiflazz-Delivery")
	}
	payload, err := a.buyer.ProcessCallback(body, signature)
	if err != nil {
		return nil, "", &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorAuthentication, ProviderStatus: "Callback Error", Message: err.Error(), Cause: err}
	}
	result, mapErr := resultFromCallback(payload)
	if mapErr != nil {
		return nil, "", mapErr
	}
	return result, payload.Data.RefID, nil
}

func resultFromResponse(resp *service.DigiflazzTransactionResponse, statusCheck bool) *provider.Result {
	if resp == nil {
		return &provider.Result{Status: provider.StatusFailedHold, ProviderStatus: "Pending", Message: "empty Digiflazz response"}
	}
	raw, _ := json.Marshal(resp.Data)
	return mapResult(resp.Data.RefID, resp.Data.Status, resp.Data.Message, resp.Data.SN, raw, statusCheck)
}

func resultFromCallback(payload *service.DigiflazzCallbackPayload) (*provider.Result, error) {
	if payload == nil || payload.Data.RefID == "" {
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary, ProviderStatus: "Callback Error", Message: "empty callback data"}
	}
	raw, _ := json.Marshal(payload.Data)
	return mapResult(payload.Data.RefID, payload.Data.Status, payload.Data.Message, payload.Data.SN, raw, false), nil
}

func mapResult(orderID, status, message, sn string, raw []byte, statusCheck bool) *provider.Result {
	result := &provider.Result{ProviderOrderID: orderID, SN: sn, Message: message, ProviderStatus: status, Raw: raw, UpdateSN: true}
	switch status {
	case "Sukses":
		result.Status = provider.StatusSuccess
		result.IncludeSNInSuccessEvent = true
		if statusCheck {
			result.StatusReason = "Digiflazz: terkonfirmasi sukses"
		} else {
			result.StatusReason = "Provider completed transaction successfully"
		}
	case "Gagal":
		if isInternalOrProviderBalanceError(message) {
			result.Status = provider.StatusFailedHold
			result.ProviderStatus = "Pending (Kendala Provider)"
			result.StatusReason = "Digiflazz: " + message
		} else {
			result.Status = provider.StatusFailedFinal
			result.IncludeCompletedAtInFailedEvent = true
			if statusCheck {
				result.StatusReason = "Digiflazz gagal: " + message
			} else {
				result.StatusReason = "Provider failed: " + message
			}
		}
	default:
		result.Status = provider.StatusPending
		if !statusCheck {
			result.StatusReason = "Waiting for provider callback"
		}
	}
	return result
}

func errorKind(message string) provider.ErrorKind {
	if strings.Contains(strings.ToLower(message), "timeout") || strings.Contains(strings.ToLower(message), "connection") {
		return provider.ErrorTimeout
	}
	return provider.ErrorTemporary
}

func isInternalOrProviderBalanceError(message string) bool {
	m := strings.ToLower(message)
	for _, fragment := range []string{"saldo", "balance", "shell", "challenge", "captcha", "session", "reauth", "timeout", "timed out", "connection", "preflight", "totp", "uid", "konfigurasi", "modal", "harga naik", "harga modal", "server", "maintenance", "jaringan"} {
		if strings.Contains(m, fragment) {
			return true
		}
	}
	return false
}
