package service

import (
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
	Group    string `json:"group"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	IconURL  string `json:"icon_url"`
	Active   bool   `json:"active"`
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
	Success bool             `json:"success"`
	Message string           `json:"message"`
	Data    []TripayChannel  `json:"data"`
}

// SyncResult reports what a sync run did, so the admin can see it worked
// without having to open the payment_methods table themselves.
type SyncResult struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Skipped []string `json:"skipped"` // channels Tripay returned but we could not map safely
}

type TripayChannelService interface {
	// FetchChannels calls Tripay's payment-channel API and returns the raw list.
	FetchChannels() ([]TripayChannel, error)
	// SyncPaymentMethods fetches the channel list and upserts each one into
	// payment_methods, using total_fee (flat+percent) so the full cost of
	// the channel is charged to the buyer rather than absorbed by the merchant.
	SyncPaymentMethods() (*SyncResult, error)
}

type tripayChannelService struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	paymentRepo repository.PaymentRepository
}

func NewTripayChannelService(apiKey, baseURL string, paymentRepo repository.PaymentRepository) TripayChannelService {
	return &tripayChannelService{
		apiKey:      apiKey,
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		paymentRepo: paymentRepo,
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