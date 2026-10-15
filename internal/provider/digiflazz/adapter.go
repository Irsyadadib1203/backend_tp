package digiflazz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"topup-backend/internal/provider"
)

// ---------------------------------------------------------------------------
// Pengelompokan status Digiflazz berdasarkan RC. Jika aturannya berubah, ubah di sini saja.
//
//	SUKSES      RC 00
//	GAGAL       RC 02, 51, 54                        -> transaksi failed + refund otomatis
//	PROCESSING  RC 01, 03, 50, 51, 52, 53, 54, 55    -> Digiflazz masih memproses
//	PENDING     RC 40, 41, 42, 43, 44, 45, 47, 49,
//	            semua RC lain, RC kosong, dan balasan tanpa RC (404 dsb.)
//	                                                 -> ditahan, perlu dicek admin
//
// Jika satu RC terdaftar di lebih dari satu kelompok, urutan prioritasnya:
// SUKSES > GAGAL > PROCESSING > PENDING.
// Hapus dari rcFailed jika ingin keduanya menjadi PROCESSING.
//
// Status transaksi di database hanya success, failed, dan processing. Kelompok
// PROCESSING dan PENDING sama-sama membuat transaksi tetap processing; bedanya
// terlihat di kolom provider_status ("Processing (RC xx)" / "Pending (RC xx)").
// Status teks dari Digiflazz ("Sukses"/"Gagal"/"Pending") tidak dipakai untuk
// menentukan status, hanya disimpan sebagai informasi.
// ---------------------------------------------------------------------------

func rcSet(codes ...string) map[string]bool {
	set := make(map[string]bool, len(codes))
	for _, code := range codes {
		set[code] = true
	}
	return set
}

var (
	rcSuccess    = rcSet("00")
	rcFailed     = rcSet("02", "51", "54")
	rcProcessing = rcSet("03")
)

type statusGroup int

const (
	groupPending statusGroup = iota // default: RC lain, RC kosong, balasan tanpa RC
	groupProcessing
	groupSuccess
	groupFailed
)

func groupOfRC(rc string) statusGroup {
	switch {
	case rcSuccess[rc]:
		return groupSuccess
	case rcFailed[rc]:
		return groupFailed
	case rcProcessing[rc]:
		return groupProcessing
	default:
		return groupPending
	}
}

type Adapter struct {
	client Client
}

func New(client Client) *Adapter { return &Adapter{client: client} }

func (a *Adapter) Code() string { return provider.DigiflazzCode }

func (a *Adapter) Purchase(_ context.Context, r provider.PurchaseRequest) (*provider.Result, error) {
	if r.ExistingProviderOrderID != "" {
		// ServerID WAJIB ikut: Digiflazz mengecek status dengan POST /transaction
		// (customer_no ikut dikirim), jadi tanpa ServerID customer_no hanya berisi
		// customer_id.
		return a.CheckStatus(context.Background(), provider.StatusRequest{
			RefID: r.RefID, ProductCode: r.ProductCode, CustomerID: r.CustomerID, ServerID: r.ServerID,
			GameSlug: r.GameSlug, ProviderOrderID: r.ExistingProviderOrderID, ProviderData: r.ProviderData,
		})
	}
	resp, err := a.client.CreateTransaction(r.RefID, r.ProductCode, provider.CustomerNumber(r.CustomerID, r.ServerID), false)
	if err != nil {
		var rawReq []byte
		var rawResp []byte
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			rawReq = apiErr.RawRequest
			rawResp = apiErr.RawResponse
		}
		if len(rawResp) == 0 {
			rawResp, _ = json.Marshal(map[string]interface{}{"error": err.Error(), "ref_id": r.RefID, "timestamp": time.Now().Format(time.RFC3339)})
		}
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: errorKind(err.Error()), ProviderStatus: "Pending", Message: err.Error(), Cause: err, Raw: rawResp, RequestRaw: rawReq}
	}
	result := resultFromResponse(resp, false)
	return result, nil
}

func (a *Adapter) CheckStatus(_ context.Context, r provider.StatusRequest) (*provider.Result, error) {
	customerNo := provider.CustomerNumber(r.CustomerID, r.ServerID)
	// Digiflazz mengecek status dengan ref_id yang sama saat order dibuat.
	refID := strings.TrimSpace(r.RefID)
	if refID == "" || refID == "-" {
		return nil, &provider.ProviderError{
			Status:         provider.StatusFailedHold,
			Kind:           provider.ErrorConfiguration,
			ProviderStatus: "Konfigurasi Error",
			Message:        "Digiflazz: ref_id kosong, tidak bisa cek status",
		}
	}
	resp, err := a.client.CheckTransactionStatus(refID, r.ProductCode, customerNo)
	if err != nil {
		var rawReq []byte
		var rawResp []byte
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			rawReq = apiErr.RawRequest
			rawResp = apiErr.RawResponse
		}
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: errorKind(err.Error()), ProviderStatus: "Pending", Message: err.Error(), Cause: err, Raw: rawResp, RequestRaw: rawReq}
	}
	result := resultFromResponse(resp, true)
	return result, nil
}

func (a *Adapter) Balance(context.Context) (float64, error) { return a.client.CheckBalance() }

func (a *Adapter) SyncCatalog(context.Context) ([]provider.CatalogItem, error) {
	items, err := a.client.GetPriceList()
	if err != nil {
		return nil, err
	}
	result := make([]provider.CatalogItem, 0, len(items))
	for _, item := range items {
		result = append(result, provider.CatalogItem{ProductCode: item.BuyerSkuCode, Name: item.ProductName, BasePrice: item.Price, IsActive: item.BuyerProductStatus && item.SellerProductStatus})
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
	payload, err := a.client.ProcessCallback(body, signature)
	if err != nil {
		return nil, "", &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorAuthentication, ProviderStatus: "Callback Error", Message: err.Error(), Cause: err, Raw: body}
	}
	result, mapErr := resultFromCallback(payload, body)
	if mapErr != nil {
		return nil, "", mapErr
	}
	return result, payload.Data.RefID, nil
}

func resultFromResponse(resp *TransactionResponse, statusCheck bool) *provider.Result {
	if resp == nil {
		return &provider.Result{Status: provider.StatusPending, ProviderStatus: "Pending (RC -)", Message: "empty Digiflazz response"}
	}
	raw := resp.RawResponse
	if len(raw) == 0 {
		raw, _ = json.Marshal(resp.Data)
	}
	res := mapResult(strings.TrimSpace(resp.Data.TrxID), resp.Data.RC, resp.Data.Status, resp.Data.Message, resp.Data.SN, raw, statusCheck)
	if len(resp.RawRequest) > 0 {
		res.RequestRaw = resp.RawRequest
	}
	return res
}

func resultFromCallback(payload *CallbackPayload, raw []byte) (*provider.Result, error) {
	if payload == nil || payload.Data.RefID == "" {
		return nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary, ProviderStatus: "Callback Error", Message: "empty callback data", Raw: raw}
	}
	if len(raw) == 0 {
		raw, _ = json.Marshal(payload.Data)
	}
	return mapResult(strings.TrimSpace(payload.Data.TrxID), payload.Data.RC, payload.Data.Status, payload.Data.Message, payload.Data.SN, raw, false), nil
}

// mapResult menerjemahkan balasan Digiflazz menjadi hasil standar berdasarkan kelompok RC.
func mapResult(orderID, rc, status, message, sn string, raw []byte, statusCheck bool) *provider.Result {
	rc = strings.TrimSpace(rc)
	label := rc
	if label == "" {
		label = "-"
	}
	result := &provider.Result{ProviderOrderID: orderID, SN: sn, Message: message, ProviderStatus: status, Raw: raw, UpdateSN: true}

	switch groupOfRC(rc) {
	case groupSuccess:
		result.Status = provider.StatusSuccess
		result.ProviderStatus = "Sukses"
		result.IncludeSNInSuccessEvent = true
		if statusCheck {
			result.StatusReason = "Digiflazz: terkonfirmasi sukses"
		} else {
			result.StatusReason = "Provider completed transaction successfully"
		}

	case groupFailed:
		result.Status = provider.StatusFailedFinal
		result.ProviderStatus = "Gagal"
		result.IncludeCompletedAtInFailedEvent = true
		if statusCheck {
			result.StatusReason = "Digiflazz gagal: " + message
		} else {
			result.StatusReason = "Provider failed: " + message
		}

	case groupProcessing:
		// Digiflazz masih memproses: transaksi tetap processing, tunggu callback / cek status.
		result.Status = provider.StatusPending
		result.ProviderStatus = fmt.Sprintf("Processing (RC %s)", label)
		if !statusCheck {
			if rc == "03" {
				result.StatusReason = "Waiting for provider callback"
			} else {
				result.StatusReason = fmt.Sprintf("Digiflazz memproses (RC %s): %s", label, message)
			}
		}

	default:
		// PENDING: RC 40-49 tertentu, RC lain, RC kosong, atau balasan tanpa RC.
		// Transaksi tetap processing dan perlu dicek admin.
		result.Status = provider.StatusPending
		result.ProviderStatus = fmt.Sprintf("Pending (RC %s)", label)
		result.StatusReason = fmt.Sprintf("Digiflazz pending (RC %s): %s", label, message)
	}
	return result
}

func errorKind(message string) provider.ErrorKind {
	if strings.Contains(strings.ToLower(message), "timeout") || strings.Contains(strings.ToLower(message), "connection") {
		return provider.ErrorTimeout
	}
	return provider.ErrorTemporary
}
