package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

type WebhookService interface {
	DispatchH2HCallback(targetURL, apiKey string, data *H2HResponseData)
}

type webhookService struct {
	providerRepo repository.ProviderRepository
	httpClient   *http.Client
}

func NewWebhookService(providerRepo repository.ProviderRepository) WebhookService {
	return &webhookService{
		providerRepo: providerRepo,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// DispatchH2HCallback mengirim callback ke partner.
// sign = md5(ref_id + apikey + status)
func (s *webhookService) DispatchH2HCallback(targetURL, apiKey string, data *H2HResponseData) {
	if targetURL == "" || data == nil {
		return
	}

	sign := crypto.MD5Hash(data.RefID + apiKey + data.Status)
	data.Sign = sign

	payloadBytes, err := json.Marshal(map[string]interface{}{
		"data": data,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Callback-Signature", sign)

	resp, err := s.httpClient.Do(req)
	statusCode := 0
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else {
		statusCode = resp.StatusCode
		resp.Body.Close()
	}

	_ = s.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookOutgoing,
		ProviderName: "H2H_CLIENT_WEBHOOK",
		URL:          targetURL,
		Payload:      string(payloadBytes),
		StatusCode:   statusCode,
		ErrorMessage: errMsg,
		CreatedAt:    time.Now(),
	})

	if err != nil {
		fmt.Printf("[Webhook] Failed to dispatch callback to %s: %v\n", targetURL, err)
	}
}