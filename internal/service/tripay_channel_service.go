package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/repository"
)

// TripayChannel mirrors the shape of one entry returned by
// GET {base_url}/merchant/payment-channel
type TripayChannel struct {
	Group       string `json:"group"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	IconURL     string `json:"icon_url"`
	Active      bool   `json:"active"`
	FeeMerchant struct {
		Flat    float64 `json:"flat"`
		Percent float64 `json:"percent"`
	} `json:"fee_merchant"`
	FeeCustomer struct {
		Flat    float64 `json:"flat"`
		Percent float64 `json:"percent"`
	} `json:"fee_customer"`
	TotalFee struct {
		Flat    float64 `json:"flat"`
		Percent string  `json:"percent"` // Tripay returns this one as a string, e.g. "0.70"
	} `json:"total_fee"`
	MinimumAmount float64 `json:"minimum_amount"`
	MaximumAmount float64 `json:"maximum_amount"`
}

type tripayChannelListResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    []TripayChannel `json:"data"`
}

// SyncResult reports what a sync run did, so the admin can see it worked
// without having to open the payment_methods table themselves.
type SyncResult struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Skipped []string `json:"skipped"` // channels Tripay returned but we could not map safely
}

type TripayOrderItem struct {
	SKU      string `json:"sku,omitempty"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
}

type TripayCreateTxRequest struct {
	Method        string            `json:"method"`
	MerchantRef   string            `json:"merchant_ref"`
	Amount        int64             `json:"amount"`
	CustomerName  string            `json:"customer_name"`
	CustomerEmail string            `json:"customer_email"`
	CustomerPhone string            `json:"customer_phone"`
	OrderItems    []TripayOrderItem `json:"order_items"`
	CallbackURL   string            `json:"callback_url,omitempty"`
	ReturnURL     string            `json:"return_url,omitempty"`
	ExpiredTime   int64             `json:"expired_time,omitempty"`
	Signature     string            `json:"signature"`
}

type TripayCreateTxResponse struct {
	Success bool                     `json:"success"`
	Message string                   `json:"message"`
	Data    *TripayTransactionDetail `json:"data"`
}

type TripayTransactionDetail struct {
	Reference      string              `json:"reference"`
	MerchantRef    string              `json:"merchant_ref"`
	PaymentMethod  string              `json:"payment_method"`
	PaymentName    string              `json:"payment_name"`
	Amount         int64               `json:"amount"`
	FeeCustomer    int64               `json:"fee_customer"`
	TotalFee       int64               `json:"total_fee"`
	AmountReceived int64               `json:"amount_received"`
	PayCode        string              `json:"pay_code"`
	PayURL         string              `json:"pay_url"`
	CheckoutURL    string              `json:"checkout_url"`
	Status         string              `json:"status"` // "UNPAID"
	ExpiredTime    int64               `json:"expired_time"`
	QRString       string              `json:"qr_string"`
	QRURL          string              `json:"qr_url"`
	Instructions   []TripayInstruction `json:"instructions"`
}

type TripayInstruction struct {
	Title string   `json:"title"`
	Steps []string `json:"steps"`
}

type TripayChannelService interface {
	// FetchChannels calls Tripay's payment-channel API and returns the raw list.
	FetchChannels() ([]TripayChannel, error)
	// SyncPaymentMethods fetches the channel list and upserts each one into
	// payment_methods, using total_fee (flat+percent) so the full cost of
	// the channel is charged to the buyer rather than absorbed by the merchant.
	SyncPaymentMethods() (*SyncResult, error)
	// CreateTransaction creates a closed payment transaction in Tripay (returns VA/QRIS/link).
	CreateTransaction(req *TripayCreateTxRequest) (*TripayTransactionDetail, error)
}

type tripayChannelService struct {
	apiKey       string
	privateKey   string
	merchantCode string
	baseURL      string
	httpClient   *http.Client
	paymentRepo  repository.PaymentRepository
}

func NewTripayChannelService(apiKey, privateKey, merchantCode, baseURL string, paymentRepo repository.PaymentRepository) TripayChannelService {
	return &tripayChannelService{
		apiKey:       apiKey,
		privateKey:   privateKey,
		merchantCode: merchantCode,
		baseURL:      strings.TrimRight(baseURL, "/"),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		paymentRepo:  paymentRepo,
	}
}

func (s *tripayChannelService) FetchChannels() ([]TripayChannel, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("TRIPAY_API_KEY is not configured")
	}

	url := s.baseURL + "/merchant/payment-channel"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach Tripay: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tripay returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed tripayChannelListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse Tripay response: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("Tripay API error: %s", parsed.Message)
	}

	return parsed.Data, nil
}

func (s *tripayChannelService) SyncPaymentMethods() (*SyncResult, error) {
	channels, err := s.FetchChannels()
	if err != nil {
		return nil, err
	}

	result := &SyncResult{}

	for i, ch := range channels {
		if ch.Code == "" {
			result.Skipped = append(result.Skipped, fmt.Sprintf("entry #%d (missing code)", i))
			continue
		}

		// total_fee.percent comes back as a string from Tripay; parse it
		// defensively instead of assuming it always parses cleanly.
		percentFee, perr := strconv.ParseFloat(ch.TotalFee.Percent, 64)
		if perr != nil {
			percentFee = 0
		}
		fixedFee := ch.TotalFee.Flat

		minAmount := ch.MinimumAmount
		if minAmount <= 0 {
			minAmount = 1000
		}
		maxAmount := ch.MaximumAmount
		if maxAmount <= 0 {
			maxAmount = 50000000
		}

		existing, _ := s.paymentRepo.GetByCode(ch.Code)

		if existing != nil && existing.ID != 0 {
			existing.Name = ch.Name
			existing.Category = domain.PaymentCategory(ch.Group)
			existing.FixedFee = fixedFee
			existing.PercentFee = percentFee
			existing.MinAmount = minAmount
			existing.MaxAmount = maxAmount
			existing.ImageURL = ch.IconURL
			existing.IsActive = ch.Active
			if err := s.paymentRepo.Update(existing); err != nil {
				result.Skipped = append(result.Skipped, ch.Code+" (update failed: "+err.Error()+")")
				continue
			}
			result.Updated = append(result.Updated, ch.Code)
			continue
		}

		newMethod := &domain.PaymentMethod{
			Code:       ch.Code,
			Name:       ch.Name,
			Category:   domain.PaymentCategory(ch.Group),
			FixedFee:   fixedFee,
			PercentFee: percentFee,
			MinAmount:  minAmount,
			MaxAmount:  maxAmount,
			ImageURL:   ch.IconURL,
			IsActive:   ch.Active,
		}
		if err := s.paymentRepo.Create(newMethod); err != nil {
			result.Skipped = append(result.Skipped, ch.Code+" (create failed: "+err.Error()+")")
			continue
		}
		result.Created = append(result.Created, ch.Code)
	}

	return result, nil
}

func (s *tripayChannelService) CreateTransaction(req *TripayCreateTxRequest) (*TripayTransactionDetail, error) {
	if s.apiKey == "" || s.privateKey == "" || s.merchantCode == "" {
		return nil, fmt.Errorf("Tripay credentials (API key, private key, merchant code) belum lengkap dikonfigurasi di .env")
	}

	// Signature Tripay: HMAC-SHA256(merchantCode + merchantRef + amount, privateKey)
	mac := hmac.New(sha256.New, []byte(s.privateKey))
	dataToSign := fmt.Sprintf("%s%s%d", s.merchantCode, req.MerchantRef, req.Amount)
	mac.Write([]byte(dataToSign))
	req.Signature = hex.EncodeToString(mac.Sum(nil))

	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := s.baseURL + "/transaction/create"
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal terhubung ke Tripay: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return nil, fmt.Errorf("Tripay mengembalikan HTTP %d: %s", resp.StatusCode, string(body))
	}

	var parsed TripayCreateTxResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("gagal membaca respon Tripay: %w", err)
	}
	if !parsed.Success || parsed.Data == nil {
		return nil, fmt.Errorf("Tripay API: %s", parsed.Message)
	}

	return parsed.Data, nil
}