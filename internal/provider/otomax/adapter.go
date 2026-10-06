// Package otomax adapts the reusable OtoMax HTTP protocol to the Provider
// boundary. It intentionally owns status translation only; transaction state,
// refunds, persistence, and SSE remain in TransactionService.applyResult.
package otomax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"topup-backend/internal/protocol/otomaxhttp"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

const FFZStoreCode = "FFZSTORE"

type Adapter struct {
	code       string
	providers  repository.ProviderRepository
	httpClient *http.Client
}

func New(code string, providers repository.ProviderRepository, httpClient ...*http.Client) *Adapter {
	var client *http.Client
	if len(httpClient) > 0 {
		client = httpClient[0]
	}
	return &Adapter{code: strings.ToUpper(strings.TrimSpace(code)), providers: providers, httpClient: client}
}

func (a *Adapter) Code() string { return a.code }

func (a *Adapter) Purchase(ctx context.Context, request provider.PurchaseRequest) (*provider.Result, error) {
	if request.ExistingProviderOrderID != "" && request.ExistingProviderOrderID != "-" {
		return a.CheckStatus(ctx, provider.StatusRequest{
			RefID: request.RefID, ProductCode: request.ProductCode, CustomerID: request.CustomerID,
			GameSlug: request.GameSlug, ProviderOrderID: request.ExistingProviderOrderID, ProviderData: request.ProviderData,
		})
	}
	client, cfg, record, err := a.load()
	if err != nil {
		return nil, configurationError(err)
	}
	if cfg.Purchase == nil {
		return nil, configurationError(errors.New("OtoMax purchase endpoint is not configured"))
	}
	response, err := client.Execute(ctx, endpointFromConfig(cfg.Purchase, cfg), values(request.RefID, request.ProductCode, request.CustomerID, request.ServerID, "", request.CallbackURL, record.APIKey))
	if err != nil {
		return nil, transportError(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, httpStatusError(response.StatusCode, response.Body)
	}
	parsed, err := otomaxhttp.ParseResponse(response.Body)
	if err != nil {
		return nil, parseError(err, response.Body)
	}
	return resultFromParsed(parsed, cfg, false), nil
}

func (a *Adapter) CheckStatus(ctx context.Context, request provider.StatusRequest) (*provider.Result, error) {
	client, cfg, record, err := a.load()
	if err != nil {
		return nil, configurationError(err)
	}
	if cfg.Status == nil {
		return nil, configurationError(errors.New("OtoMax status endpoint is not configured"))
	}
	invoiceNumber := callbackInvoiceNumber(request.ProviderData)
	if invoiceNumber == "" {
		// Purchase responses expose RefId, while the documented status endpoint
		// requires invoice_number. Treating them as interchangeable could query
		// or mutate the wrong provider order. Keep the transaction pending until
		// a callback supplies its invoice_number or the mapping is confirmed.
		return pendingResult("OtoMax: menunggu invoice_number untuk status check"), nil
	}
	response, err := client.Execute(ctx, endpointFromConfig(cfg.Status, cfg), values(request.RefID, request.ProductCode, request.CustomerID, "", invoiceNumber, "", record.APIKey))
	if err != nil {
		return nil, transportError(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, httpStatusError(response.StatusCode, response.Body)
	}
	parsed, err := otomaxhttp.ParseResponse(response.Body)
	if err != nil {
		return nil, parseError(err, response.Body)
	}
	return resultFromParsed(parsed, cfg, true), nil
}

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	client, cfg, record, err := a.load()
	if err != nil {
		return 0, configurationError(err)
	}
	if cfg.Balance == nil {
		return 0, configurationError(errors.New("OtoMax user endpoint is not configured"))
	}
	response, err := client.Execute(ctx, endpointFromConfig(cfg.Balance, cfg), values("", "", "", "", "", "", record.APIKey))
	if err != nil {
		return 0, transportError(err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return 0, httpStatusError(response.StatusCode, response.Body)
	}
	info, err := otomaxhttp.ParseUser(response.Body)
	if err != nil {
		return 0, parseError(err, response.Body)
	}
	return info.Balance, nil
}

// ParseCallback supports the documented unauthenticated GET callback. No
// signature is invented; the caller's existing callback route response is kept.
func (a *Adapter) ParseCallback(request *http.Request) (*provider.Result, string, error) {
	if request == nil {
		return nil, "", parseError(errors.New("nil OtoMax callback request"), nil)
	}
	values := request.URL.Query()
	trxID := strings.TrimSpace(values.Get("trx_id"))
	invoiceNumber := strings.TrimSpace(values.Get("invoice_number"))
	status := strings.ToUpper(strings.TrimSpace(values.Get("status")))
	if trxID == "" {
		return nil, "", parseError(errors.New("OtoMax callback has no trx_id for transaction correlation"), nil)
	}
	if status == "" {
		return nil, "", parseError(errors.New("OtoMax callback has no status"), nil)
	}
	_, _, _, err := a.load()
	if err != nil {
		return nil, "", configurationError(err)
	}
	message, sn := values.Get("msg"), values.Get("sn")
	raw, _ := json.Marshal(map[string]string{
		"invoice_number": invoiceNumber, "trx_id": trxID, "status": status,
		"product_price": values.Get("product_price"), "sn": sn, "msg": message,
	})
	result := resultFromStatus(status, invoiceNumber, sn, message, raw, false)
	return result, trxID, nil
}

func (a *Adapter) load() (*otomaxhttp.Client, *provider.DeclarativeProviderConfig, *domainProvider, error) {
	if a.providers == nil {
		return nil, nil, nil, errors.New("provider repository is not configured")
	}
	record, err := a.providers.GetByCode(a.code)
	if err != nil || record == nil {
		return nil, nil, nil, errors.New("OtoMax provider is not configured")
	}
	if !record.IsActive {
		return nil, nil, nil, errors.New("OtoMax provider is inactive")
	}
	cfg, err := provider.ValidateProviderConfig(record.Config)
	if err != nil {
		return nil, nil, nil, err
	}
	if !strings.EqualFold(cfg.Protocol, "otomax_http") {
		return nil, nil, nil, fmt.Errorf("provider protocol must be otomax_http")
	}
	client, err := otomaxhttp.NewClient(cfg.BaseURL, a.httpClient)
	if err != nil {
		return nil, nil, nil, err
	}
	return client, cfg, &domainProvider{APIKey: record.APIKey}, nil
}

// domainProvider prevents protocol details from depending on the domain model.
type domainProvider struct{ APIKey string }

func endpointFromConfig(ep *provider.EndpointConfig, cfg *provider.DeclarativeProviderConfig) otomaxhttp.Endpoint {
	timeout := 20 * time.Second
	if ep.TimeoutSeconds > 0 {
		timeout = time.Duration(ep.TimeoutSeconds) * time.Second
	}
	maxResponseLen := int64(0)
	if cfg.Parse != nil {
		maxResponseLen = cfg.Parse.MaxResponseBytes
	}
	return otomaxhttp.Endpoint{Method: ep.Method, Path: ep.Path, Timeout: timeout, Query: ep.Query, Headers: ep.Headers, Body: ep.Body, MaxResponseLen: maxResponseLen}
}

func values(refID, productCode, customerID, serverID, providerOrderID, callbackURL, apiKey string) otomaxhttp.TemplateValues {
	return otomaxhttp.TemplateValues{RefID: refID, ProductCode: productCode, CustomerID: customerID, ServerID: serverID, ProviderOrderID: providerOrderID, CallbackURL: callbackURL, Secrets: map[string]string{"apikey": apiKey}}
}

func resultFromParsed(parsed *otomaxhttp.ParsedResponse, _ *provider.DeclarativeProviderConfig, statusCheck bool) *provider.Result {
	orderID := parsed.ProviderOrderID
	if statusCheck {
		// The status endpoint may echo RefId. Do not overwrite the stored
		// invoice_number used to construct this request with that distinct ID.
		orderID = ""
	}
	return resultFromStatus(parsed.Status, orderID, parsed.SerialNumber, string(parsed.Raw), parsed.Raw, statusCheck)
}

func resultFromStatus(status, orderID, sn, message string, raw []byte, statusCheck bool) *provider.Result {
	result := &provider.Result{ProviderOrderID: orderID, SN: sn, Message: message, ProviderStatus: status, Raw: raw, UpdateSN: sn != ""}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		result.Status = provider.StatusSuccess
		result.StatusReason = "OtoMax: status terkonfirmasi sukses"
		result.IncludeSNInSuccessEvent = !statusCheck
	case "PENDING", "PARTIAL_SUCCESS":
		result.Status = provider.StatusPending
		if strings.EqualFold(status, "PARTIAL_SUCCESS") {
			result.StatusReason = "OtoMax: partial success membutuhkan pengecekan lanjutan"
		} else {
			result.StatusReason = "OtoMax: pesanan sedang diproses"
		}
	case "FAILED", "REFUNDED":
		result.Status = provider.StatusFailedFinal
		result.StatusReason = "OtoMax: transaksi gagal final"
	default:
		result.Status = provider.StatusPending
		result.StatusReason = "OtoMax: status provider belum dikenali"
	}
	return result
}

func configurationError(err error) *provider.ProviderError {
	return &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration, ProviderStatus: "Konfigurasi Error", Message: err.Error(), Cause: err}
}

func parseError(err error, raw []byte) *provider.ProviderError {
	return &provider.ProviderError{Status: provider.StatusPending, Kind: provider.ErrorHold, ProviderStatus: "Provider Pending", Message: "OtoMax response tidak dapat dipahami", Cause: err, Raw: raw}
}

func transportError(err error) *provider.ProviderError {
	kind := provider.ErrorTemporary
	if errors.Is(err, context.DeadlineExceeded) {
		kind = provider.ErrorTimeout
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			kind = provider.ErrorTimeout
		}
	}
	return &provider.ProviderError{Status: provider.StatusPending, Kind: kind, ProviderStatus: "Provider Pending", Message: "OtoMax provider tidak dapat dihubungi", Cause: redactTransportError(err)}
}

var sensitiveTransportQuery = regexp.MustCompile(`(?i)(api[-_]?key|authorization|token|secret|signature|cookie|session)=([^&\s"']+)`)

// redactTransportError preserves a useful diagnostic cause without allowing an
// http.Client URL error to expose credentials embedded in a configured query.
func redactTransportError(err error) error {
	if err == nil {
		return nil
	}
	message := sensitiveTransportQuery.ReplaceAllString(err.Error(), "$1=***MASKED***")
	return errors.New(message)
}

func httpStatusError(statusCode int, raw []byte) *provider.ProviderError {
	return &provider.ProviderError{Status: provider.StatusPending, Kind: provider.ErrorTemporary, ProviderStatus: "Provider Pending", Message: fmt.Sprintf("OtoMax HTTP status %d", statusCode), Raw: raw}
}

func pendingResult(reason string) *provider.Result {
	return &provider.Result{Status: provider.StatusPending, ProviderStatus: "PENDING", Message: reason, StatusReason: reason}
}

func callbackInvoiceNumber(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var callback struct {
		InvoiceNumber string `json:"invoice_number"`
	}
	if json.Unmarshal([]byte(raw), &callback) != nil {
		return ""
	}
	return strings.TrimSpace(callback.InvoiceNumber)
}
