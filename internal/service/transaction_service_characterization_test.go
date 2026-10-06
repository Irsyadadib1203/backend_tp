package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/sse"
	"topup-backend/internal/provider"
)

// These fakes deliberately model only observability required by the legacy
// transaction-service characterization tests. They do not change production code.
type characterizationTxRepo struct {
	byID, byRef, byInvoice map[uint]*domain.Transaction
	updates                int
	statusUpdates          []characterizationStatusUpdate
}

type characterizationStatusUpdate struct {
	id     uint
	status domain.TransactionStatus
	reason string
}

func (r *characterizationTxRepo) Create(*domain.Transaction) error { return nil }
func (r *characterizationTxRepo) FindByID(id uint) (*domain.Transaction, error) {
	tx := r.byID[id]
	if tx == nil {
		return nil, errors.New("not found")
	}
	return tx, nil
}
func (r *characterizationTxRepo) FindByInvoiceNumber(invoice string) (*domain.Transaction, error) {
	for _, tx := range r.byID {
		if tx.InvoiceNumber == invoice {
			return tx, nil
		}
	}
	return nil, errors.New("not found")
}
func (r *characterizationTxRepo) FindByRefID(refID string) (*domain.Transaction, error) {
	for _, tx := range r.byID {
		if tx.RefID == refID {
			return tx, nil
		}
	}
	return nil, errors.New("not found")
}
func (r *characterizationTxRepo) FindByIdempotencyKey(string) (*domain.Transaction, error) {
	return nil, errors.New("not found")
}
func (r *characterizationTxRepo) Update(*domain.Transaction) error { r.updates++; return nil }
func (r *characterizationTxRepo) UpdateStatus(id uint, status domain.TransactionStatus, reason string) error {
	r.statusUpdates = append(r.statusUpdates, characterizationStatusUpdate{id, status, reason})
	return nil
}
func (r *characterizationTxRepo) MarkAsProcessingIfPending(uint, string, time.Time) (bool, error) {
	return false, nil
}
func (r *characterizationTxRepo) FindProcessingOlderThan(time.Duration, int) ([]domain.Transaction, error) {
	return nil, nil
}
func (r *characterizationTxRepo) ClaimProcessingForReconciliation(uint, time.Time) (bool, error) {
	return true, nil
}
func (r *characterizationTxRepo) FindProcessingBalanceHolds(uint, int) ([]domain.Transaction, error) {
	return nil, nil
}
func (r *characterizationTxRepo) ListRecent(int) ([]domain.Transaction, error) { return nil, nil }
func (r *characterizationTxRepo) ListByUser(uint, int, int) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (r *characterizationTxRepo) ListAdmin(int, int, string, string, string, string) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (r *characterizationTxRepo) GetDashboardStats() (map[string]interface{}, error) { return nil, nil }

type characterizationNominalRepo struct{ nominals map[uint]*domain.Nominal }

func (r *characterizationNominalRepo) Create(*domain.Nominal) error { return nil }
func (r *characterizationNominalRepo) FindByID(id uint) (*domain.Nominal, error) {
	return r.nominals[id], nil
}
func (r *characterizationNominalRepo) FindByProviderCode(string) (*domain.Nominal, error) {
	return nil, nil
}
func (r *characterizationNominalRepo) FindBySellerCode(string) (*domain.Nominal, error) {
	return nil, nil
}
func (r *characterizationNominalRepo) Update(*domain.Nominal) error                { return nil }
func (r *characterizationNominalRepo) Delete(uint) error                           { return nil }
func (r *characterizationNominalRepo) ListByGameID(uint) ([]domain.Nominal, error) { return nil, nil }
func (r *characterizationNominalRepo) ListAllAdmin(int, int, uint, uint, string) ([]domain.Nominal, int64, error) {
	return nil, 0, nil
}
func (r *characterizationNominalRepo) ListForSellerH2H() ([]domain.Nominal, error) { return nil, nil }
func (r *characterizationNominalRepo) UpsertFromDigiflazz([]domain.Nominal) error  { return nil }
func (r *characterizationNominalRepo) BatchSwitchProvider([]uint, uint) error      { return nil }
func (r *characterizationNominalRepo) SwitchProviderByGame(uint, uint) error       { return nil }

type characterizationGameRepo struct{ games map[uint]*domain.Game }

func (r *characterizationGameRepo) Create(*domain.Game) error { return nil }
func (r *characterizationGameRepo) FindByID(id uint) (*domain.Game, error) {
	if r == nil || r.games == nil {
		return nil, nil
	}
	return r.games[id], nil
}
func (r *characterizationGameRepo) FindBySlug(string) (*domain.Game, error) { return nil, nil }
func (r *characterizationGameRepo) Update(*domain.Game) error               { return nil }
func (r *characterizationGameRepo) Delete(uint) error                       { return nil }
func (r *characterizationGameRepo) ListPublic() ([]domain.Game, error)      { return nil, nil }
func (r *characterizationGameRepo) ListAdmin(int, int, string, string) ([]domain.Game, int64, error) {
	return nil, 0, nil
}
func (r *characterizationGameRepo) SaveProviderMapping(*domain.GameProvider) error { return nil }
func (r *characterizationGameRepo) GetProviderMappings(uint) ([]domain.GameProvider, error) {
	return nil, nil
}

type characterizationProviderRepo struct{ byID map[uint]*domain.Provider }

func (r *characterizationProviderRepo) GetByCode(string) (*domain.Provider, error) { return nil, nil }
func (r *characterizationProviderRepo) GetByID(id uint) (*domain.Provider, error) {
	return r.byID[id], nil
}
func (r *characterizationProviderRepo) List() ([]domain.Provider, error)    { return nil, nil }
func (r *characterizationProviderRepo) Update(*domain.Provider) error       { return nil }
func (r *characterizationProviderRepo) UpdateBalance(uint, float64) error   { return nil }
func (r *characterizationProviderRepo) LogWebhook(*domain.WebhookLog) error { return nil }
func (r *characterizationProviderRepo) ListWebhookLogs(int, int, string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

type characterizationUserRepo struct {
	hasRefund bool
	credits   int
}

func (r *characterizationUserRepo) Create(*domain.User) error                { return nil }
func (r *characterizationUserRepo) FindByID(uint) (*domain.User, error)      { return &domain.User{}, nil }
func (r *characterizationUserRepo) FindByEmail(string) (*domain.User, error) { return nil, nil }
func (r *characterizationUserRepo) FindByAPIKey(string) (*domain.User, *domain.APIKey, error) {
	return nil, nil, nil
}
func (r *characterizationUserRepo) Update(*domain.User) error { return nil }
func (r *characterizationUserRepo) UpdateBalance(_ uint, _ float64, mutation domain.MutationType, _, _, _ string) error {
	if mutation == domain.MutationCredit {
		r.credits++
	}
	return nil
}
func (r *characterizationUserRepo) HasMutation(string, string) (bool, error) { return r.hasRefund, nil }
func (r *characterizationUserRepo) List(int, int, string) ([]domain.User, int64, error) {
	return nil, 0, nil
}
func (r *characterizationUserRepo) Delete(uint) error                              { return nil }
func (r *characterizationUserRepo) CreateAPIKey(*domain.APIKey) error              { return nil }
func (r *characterizationUserRepo) GetAPIKeyByUserID(uint) (*domain.APIKey, error) { return nil, nil }

type characterizationDigiflazz struct {
	createResp              *DigiflazzTransactionResponse
	createErr               error
	checkResp               *DigiflazzTransactionResponse
	checkErr                error
	createCalls, checkCalls int
}

func (d *characterizationDigiflazz) GetPriceList() ([]DigiflazzPriceListItem, error) { return nil, nil }
func (d *characterizationDigiflazz) CheckBalance() (float64, error)                  { return 0, nil }
func (d *characterizationDigiflazz) CreateTransaction(string, string, string, bool) (*DigiflazzTransactionResponse, error) {
	d.createCalls++
	return d.createResp, d.createErr
}
func (d *characterizationDigiflazz) CheckTransactionStatus(string, string, string) (*DigiflazzTransactionResponse, error) {
	d.checkCalls++
	return d.checkResp, d.checkErr
}
func (d *characterizationDigiflazz) ProcessCallback([]byte, string) (*DigiflazzCallbackPayload, error) {
	return nil, nil
}

type characterizationKiosgamer struct {
	placeResult, pollResult, recentResult *KiosgamerOrderResult
	placeErr, pollErr, recentErr          error
	placeCalls, pollCalls, recentCalls    int
	lastAppID                             int
}

func (k *characterizationKiosgamer) SaveCredentials(string, string) error { return nil }
func (k *characterizationKiosgamer) Status(context.Context) (*KiosgamerStatus, error) {
	return nil, nil
}
func (k *characterizationKiosgamer) HealthCheck(context.Context) (*KiosgamerUserInfo, error) {
	return nil, nil
}
func (k *characterizationKiosgamer) EnsureSession(context.Context) (*KiosgamerUserInfo, error) {
	return nil, nil
}
func (k *characterizationKiosgamer) RecoverSession(context.Context) error   { return nil }
func (k *characterizationKiosgamer) GenerateTOTP(time.Time) (string, error) { return "", nil }
func (k *characterizationKiosgamer) PlaceOrder(context.Context, string, string, string, string, string) (*KiosgamerOrderResult, error) {
	k.placeCalls++
	return k.placeResult, k.placeErr
}
func (k *characterizationKiosgamer) PollOrder(context.Context, string) (*KiosgamerOrderResult, error) {
	k.pollCalls++
	return k.pollResult, k.pollErr
}
func (k *characterizationKiosgamer) CheckRecentOrder(_ context.Context, appID int) (*KiosgamerOrderResult, error) {
	k.recentCalls++
	k.lastAppID = appID
	return k.recentResult, k.recentErr
}
func (k *characterizationKiosgamer) KeepAlive(context.Context) error { return nil }
func (k *characterizationKiosgamer) FetchCatalog(context.Context, string) ([]KiosgamerCatalogItem, error) {
	return nil, nil
}
func (k *characterizationKiosgamer) AutoSyncMapping(context.Context, uint, string) (*KiosgamerSyncResult, error) {
	return nil, nil
}
func (k *characterizationKiosgamer) UpdateNominalKiosgamerCode(uint, string) error { return nil }

func newCharacterizationService(txRepo *characterizationTxRepo, nominalRepo *characterizationNominalRepo, gameRepo *characterizationGameRepo, providerRepo *characterizationProviderRepo, userRepo *characterizationUserRepo, digi *characterizationDigiflazz, kios *characterizationKiosgamer) *transactionService {
	return &transactionService{txRepo: txRepo, nominalRepo: nominalRepo, gameRepo: gameRepo, providerRepo: providerRepo, userRepo: userRepo, digiflazzBuyer: digi, kiosgamerService: kios, legacyCharacterization: true}
}

func characterizationTx() *domain.Transaction {
	userID := uint(7)
	return &domain.Transaction{ID: 10, InvoiceNumber: "INV-10", RefID: "REF-10", NominalID: 11, GameID: 12, CustomerID: "12345", ServerID: "99", SellingPrice: 10000, TotalAmount: 10000, UserID: &userID, PaymentMethod: "SALDO", Status: domain.StatusProcessing}
}

func characterizationDigiflazzResponse(status, message, sn string) *DigiflazzTransactionResponse {
	r := &DigiflazzTransactionResponse{}
	r.Data.RefID, r.Data.Status, r.Data.Message, r.Data.SN = "DF-ORDER", status, message, sn
	return r
}

func TestFulfillOrderLegacy_PreconditionsAndDigiflazzCharacterization(t *testing.T) {
	t.Run("nominal missing returns exact error", func(t *testing.T) {
		tx := characterizationTx()
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{}}, nil, nil, &characterizationUserRepo{}, &characterizationDigiflazz{}, nil)
		if err := svc.FulfillOrder(tx); err == nil || err.Error() != "nominal not found" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("margin guard holds without calling provider", func(t *testing.T) {
		tx := characterizationTx()
		digi := &characterizationDigiflazz{}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		nom := &domain.Nominal{ID: 11, BasePrice: 10001, ProviderProductCode: "SKU"}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, &characterizationUserRepo{}, digi, nil)
		if err := svc.FulfillOrder(tx); err != nil {
			t.Fatal(err)
		}
		if tx.Status != domain.StatusProcessing || tx.ProviderStatus != "Pending (Harga Naik)" || digi.createCalls != 0 || repo.updates != 1 || len(repo.statusUpdates) != 1 {
			t.Fatalf("unexpected guard result: %+v calls=%d updates=%d status=%d", tx, digi.createCalls, repo.updates, len(repo.statusUpdates))
		}
	})

	cases := []struct {
		name, status, message string
		err                   error
		wantStatus            domain.TransactionStatus
		wantProviderStatus    string
		wantRefund, wantRetry bool
	}{
		{"request error", "", "", errors.New("network down"), domain.StatusProcessing, "Pending", false, true},
		{"success", "Sukses", "ok", nil, domain.StatusSuccess, "Sukses", false, false},
		{"provider balance failure holds", "Gagal", "saldo provider habis", nil, domain.StatusProcessing, "Pending (Kendala Provider)", false, false},
		{"final failure refunds", "Gagal", "id pelanggan tidak ditemukan", nil, domain.StatusFailed, "Gagal", true, false},
		{"pending holds", "Pending", "menunggu", nil, domain.StatusProcessing, "Pending", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := characterizationTx()
			digi := &characterizationDigiflazz{createResp: characterizationDigiflazzResponse(tc.status, tc.message, "SN-1"), createErr: tc.err}
			repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
			user := &characterizationUserRepo{}
			nom := &domain.Nominal{ID: 11, BasePrice: 100, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}
			svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, user, digi, nil)
			err := svc.FulfillOrder(tx)
			if (err != nil) != (tc.err != nil) || tx.Status != tc.wantStatus || tx.ProviderStatus != tc.wantProviderStatus || (tx.RetryCount == 1) != tc.wantRetry || (user.credits == 1) != tc.wantRefund || repo.updates != 1 {
				t.Fatalf("err=%v tx=%+v credits=%d updates=%d", err, tx, user.credits, repo.updates)
			}
			if tc.err == nil && (tx.ProviderOrderID != "DF-ORDER" || tx.SN != "SN-1" || tx.PaymentReference != "SN-1" || tx.ProviderCallbackData == "") {
				t.Fatalf("response fields not persisted: %+v", tx)
			}
			if tc.wantStatus == domain.StatusSuccess || tc.wantStatus == domain.StatusFailed {
				if tx.CompletedAt == nil {
					t.Fatal("expected completion time")
				}
			}
		})
	}
}

func TestFulfillOrderLegacy_KiosgamerCharacterization(t *testing.T) {
	t.Run("provider repository resolves KIOSGAMER", func(t *testing.T) {
		tx := characterizationTx()
		tx.ProviderOrderID = "ORDER-1"
		kios := &characterizationKiosgamer{pollResult: &KiosgamerOrderResult{OrderID: "ORDER-1", Status: "pending"}}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		nom := &domain.Nominal{ID: 11, ProviderID: 2, KiosgamerProductCode: "KG", BasePrice: 1}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, &characterizationGameRepo{games: map[uint]*domain.Game{12: {Slug: "free-fire"}}}, &characterizationProviderRepo{byID: map[uint]*domain.Provider{2: {Code: "KIOSGAMER"}}}, &characterizationUserRepo{}, &characterizationDigiflazz{}, kios)
		if err := svc.FulfillOrder(tx); err != nil || kios.pollCalls != 1 || kios.placeCalls != 0 {
			t.Fatalf("err=%v poll=%d place=%d", err, kios.pollCalls, kios.placeCalls)
		}
	})

	t.Run("missing Kiosgamer SKU holds without refund", func(t *testing.T) {
		tx := characterizationTx()
		kios := &characterizationKiosgamer{}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		nom := &domain.Nominal{ID: 11, Name: "FF", BasePrice: 1, Provider: &domain.Provider{Code: "KIOSGAMER"}}
		user := &characterizationUserRepo{}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, user, &characterizationDigiflazz{}, kios)
		if err := svc.FulfillOrder(tx); err == nil || tx.Status != domain.StatusProcessing || tx.ProviderStatus != "Konfigurasi Error" || user.credits != 0 || kios.placeCalls != 0 {
			t.Fatalf("err=%v tx=%+v", err, tx)
		}
	})

	cases := []struct {
		name    string
		callErr error
		result  *KiosgamerOrderResult
		want    domain.TransactionStatus
		ps      string
		refund  bool
	}{
		{"challenge error", ErrKiosgamerChallengeRequired, nil, domain.StatusProcessing, "Challenge Required", false},
		{"session error", ErrKiosgamerSessionExpired, nil, domain.StatusProcessing, "Session Error", false},
		{"balance error", errors.New("shell balance low"), nil, domain.StatusProcessing, "Provider Pending", false},
		{"fatal input error", errors.New("player id tidak ditemukan"), nil, domain.StatusFailed, "Gagal", true},
		{"unknown error", errors.New("upstream unavailable"), nil, domain.StatusProcessing, "Provider Pending", false},
		{"successful order", nil, &KiosgamerOrderResult{OrderID: "KG-1", Status: "success", Message: "ok", SerialNumber: "SN-KG"}, domain.StatusSuccess, "Sukses", false},
		{"provider failure holds", nil, &KiosgamerOrderResult{Status: "failed", Message: "saldo shell habis"}, domain.StatusProcessing, "Pending (Kendala Provider)", false},
		{"provider final failure refunds", nil, &KiosgamerOrderResult{Status: "failed", Message: "id salah"}, domain.StatusFailed, "Gagal", true},
		{"provider pending", nil, &KiosgamerOrderResult{Status: "pending", Message: "wait"}, domain.StatusProcessing, "Pending", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := characterizationTx()
			kios := &characterizationKiosgamer{placeErr: tc.callErr, placeResult: tc.result}
			repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
			user := &characterizationUserRepo{}
			nom := &domain.Nominal{ID: 11, BasePrice: 1, KiosgamerProductCode: "KG", Provider: &domain.Provider{Code: "KIOSGAMER"}}
			svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, &characterizationGameRepo{games: map[uint]*domain.Game{12: {Slug: "free-fire"}}}, nil, user, &characterizationDigiflazz{}, kios)
			err := svc.FulfillOrder(tx)
			if tx.Status != tc.want || tx.ProviderStatus != tc.ps || (user.credits == 1) != tc.refund || kios.placeCalls != 1 {
				t.Fatalf("err=%v tx=%+v credits=%d place=%d", err, tx, user.credits, kios.placeCalls)
			}
			if tc.callErr != nil && (err == nil || tx.RetryCount != 1) {
				t.Fatalf("expected returned error and retry, got err=%v retry=%d", err, tx.RetryCount)
			}
			if tc.result != nil && tc.result.Status == "success" && (tx.ProviderOrderID != "KG-1" || tx.SN != "SN-KG" || tx.PaymentReference != "SN-KG" || tx.CompletedAt == nil) {
				t.Fatalf("success fields missing: %+v", tx)
			}
		})
	}

	t.Run("safe retry polls and never places a second order", func(t *testing.T) {
		tx := characterizationTx()
		tx.ProviderOrderID = "KG-EXISTING"
		kios := &characterizationKiosgamer{pollResult: &KiosgamerOrderResult{OrderID: "KG-EXISTING", Status: "pending"}}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		nom := &domain.Nominal{ID: 11, BasePrice: 1, KiosgamerProductCode: "KG", Provider: &domain.Provider{Code: "KIOSGAMER"}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, &characterizationGameRepo{games: map[uint]*domain.Game{12: {Slug: "free-fire"}}}, nil, &characterizationUserRepo{}, &characterizationDigiflazz{}, kios)
		if err := svc.FulfillOrder(tx); err != nil || kios.pollCalls != 1 || kios.placeCalls != 0 {
			t.Fatalf("err=%v poll=%d place=%d", err, kios.pollCalls, kios.placeCalls)
		}
	})
}

func TestCheckProviderStatusAndCallbackLegacyCharacterization(t *testing.T) {
	t.Run("Digiflazz check returns legacy wrapped error", func(t *testing.T) {
		tx := characterizationTx()
		digi := &characterizationDigiflazz{checkErr: errors.New("down")}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: {ID: 11, ProviderProductCode: "SKU"}}}, nil, nil, &characterizationUserRepo{}, digi, nil)
		_, err := svc.CheckProviderStatus(tx.ID)
		if err == nil || !strings.Contains(err.Error(), "gagal cek status Digiflazz: down") || digi.checkCalls != 1 {
			t.Fatalf("err=%v calls=%d", err, digi.checkCalls)
		}
	})

	for _, tc := range []struct {
		name, status, message string
		want                  domain.TransactionStatus
		providerStatus        string
		refund                bool
	}{
		{"Digiflazz check success", "Sukses", "ok", domain.StatusSuccess, "Sukses", false},
		{"Digiflazz check provider balance failure holds", "Gagal", "saldo provider habis", domain.StatusProcessing, "Pending (Kendala Provider)", false},
		{"Digiflazz check final failure refunds", "Gagal", "id pelanggan salah", domain.StatusFailed, "Gagal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := characterizationTx()
			digi := &characterizationDigiflazz{checkResp: characterizationDigiflazzResponse(tc.status, tc.message, "CHECK-SN")}
			repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
			user := &characterizationUserRepo{}
			svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: {ID: 11, ProviderProductCode: "SKU"}}}, nil, nil, user, digi, nil)
			if _, err := svc.CheckProviderStatus(tx.ID); err != nil || tx.Status != tc.want || tx.ProviderStatus != tc.providerStatus || (user.credits == 1) != tc.refund || tx.ProviderOrderID != "DF-ORDER" || tx.SN != "CHECK-SN" {
				t.Fatalf("err=%v tx=%+v credits=%d", err, tx, user.credits)
			}
			if tc.want == domain.StatusSuccess || tc.want == domain.StatusFailed {
				if tx.CompletedAt == nil {
					t.Fatal("expected completion time")
				}
			}
		})
	}

	t.Run("Kiosgamer check polls existing order and overwrites payment reference", func(t *testing.T) {
		tx := characterizationTx()
		tx.ProviderOrderID = "KG-1"
		tx.PaymentReference = "OLD"
		kios := &characterizationKiosgamer{pollResult: &KiosgamerOrderResult{OrderID: "KG-1", Status: "success", SerialNumber: "NEW-SN"}}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: {ID: 11, Provider: &domain.Provider{Code: "KIOSGAMER"}}}}, nil, nil, &characterizationUserRepo{}, &characterizationDigiflazz{}, kios)
		got, err := svc.CheckProviderStatus(tx.ID)
		if err != nil || got != tx || kios.pollCalls != 1 || kios.placeCalls != 0 || tx.PaymentReference != "NEW-SN" || tx.CompletedAt == nil {
			t.Fatalf("err=%v tx=%+v poll=%d place=%d", err, tx, kios.pollCalls, kios.placeCalls)
		}
	})

	t.Run("Kiosgamer check uses CODM recent-order app id without creating order", func(t *testing.T) {
		tx := characterizationTx()
		kios := &characterizationKiosgamer{recentResult: &KiosgamerOrderResult{Status: "pending"}}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: {ID: 11, Provider: &domain.Provider{Code: "KIOSGAMER"}}}}, &characterizationGameRepo{games: map[uint]*domain.Game{12: {Slug: "call-of-duty-mobile"}}}, nil, &characterizationUserRepo{}, &characterizationDigiflazz{}, kios)
		if _, err := svc.CheckProviderStatus(tx.ID); err != nil || kios.recentCalls != 1 || kios.lastAppID != 100054 || kios.placeCalls != 0 {
			t.Fatalf("err=%v recent=%d app=%d place=%d", err, kios.recentCalls, kios.lastAppID, kios.placeCalls)
		}
	})

	t.Run("Kiosgamer check returns wrapped error without creating order", func(t *testing.T) {
		tx := characterizationTx()
		kios := &characterizationKiosgamer{recentErr: errors.New("history unavailable")}
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: {ID: 11, Provider: &domain.Provider{Code: "KIOSGAMER"}}}}, &characterizationGameRepo{games: map[uint]*domain.Game{}}, nil, &characterizationUserRepo{}, &characterizationDigiflazz{}, kios)
		if _, err := svc.CheckProviderStatus(tx.ID); err == nil || !strings.Contains(err.Error(), "gagal cek status Kiosgamer: history unavailable") || kios.placeCalls != 0 {
			t.Fatalf("err=%v place=%d", err, kios.placeCalls)
		}
	})

	t.Run("callback validates payload and final transactions are idempotent", func(t *testing.T) {
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{}}
		svc := newCharacterizationService(repo, nil, nil, nil, &characterizationUserRepo{}, nil, nil)
		if err := svc.HandleDigiflazzCallback(nil); err == nil || err.Error() != "empty callback data" {
			t.Fatalf("got %v", err)
		}
		missing := &DigiflazzCallbackPayload{}
		missing.Data.RefID = "MISSING"
		if err := svc.HandleDigiflazzCallback(missing); err == nil || err.Error() != "transaction not found for callback" {
			t.Fatalf("got %v", err)
		}
		for i, status := range []domain.TransactionStatus{domain.StatusSuccess, domain.StatusFailed, domain.StatusRefunded} {
			payload := &DigiflazzCallbackPayload{}
			payload.Data.RefID = fmt.Sprintf("REF-FINAL-%d", i)
			payload.Data.Status = "Sukses"
			finalTx := characterizationTx()
			finalTx.ID = uint(100 + i)
			finalTx.RefID = payload.Data.RefID
			finalTx.Status = status
			repo.byID[finalTx.ID] = finalTx
			if err := svc.HandleDigiflazzCallback(payload); err != nil || repo.updates != 0 || len(repo.statusUpdates) != 0 {
				t.Fatalf("status=%s err=%v updates=%d statuses=%d", status, err, repo.updates, len(repo.statusUpdates))
			}
		}
	})

	t.Run("callback falls back to invoice, succeeds, and final failure refunds", func(t *testing.T) {
		tx := characterizationTx()
		tx.RefID = "other"
		tx.InvoiceNumber = "REF-INVOICE"
		repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
		user := &characterizationUserRepo{}
		svc := newCharacterizationService(repo, nil, nil, nil, user, nil, nil)
		payload := &DigiflazzCallbackPayload{}
		payload.Data.RefID = "REF-INVOICE"
		payload.Data.Status = "Sukses"
		payload.Data.SN = "CALLBACK-SN"
		if err := svc.HandleDigiflazzCallback(payload); err != nil || tx.Status != domain.StatusSuccess || tx.SN != "CALLBACK-SN" || tx.CompletedAt == nil {
			t.Fatalf("err=%v tx=%+v", err, tx)
		}
		// Use a fresh non-final transaction to preserve the legacy final-failure behavior.
		failed := characterizationTx()
		failed.ID = 20
		failed.RefID = "REF-FAIL"
		repo.byID[failed.ID] = failed
		failPayload := &DigiflazzCallbackPayload{}
		failPayload.Data.RefID = "REF-FAIL"
		failPayload.Data.Status = "Gagal"
		failPayload.Data.Message = "id salah"
		if err := svc.HandleDigiflazzCallback(failPayload); err != nil || failed.Status != domain.StatusFailed || failed.CompletedAt == nil || user.credits != 1 {
			t.Fatalf("err=%v tx=%+v credits=%d", err, failed, user.credits)
		}
		hold := characterizationTx()
		hold.ID = 30
		hold.RefID = "REF-HOLD"
		repo.byID[hold.ID] = hold
		holdPayload := &DigiflazzCallbackPayload{}
		holdPayload.Data.RefID = "REF-HOLD"
		holdPayload.Data.Status = "Gagal"
		holdPayload.Data.Message = "saldo provider habis"
		if err := svc.HandleDigiflazzCallback(holdPayload); err != nil || hold.Status != domain.StatusProcessing || hold.ProviderStatus != "Pending (Kendala Provider)" || user.credits != 1 {
			t.Fatalf("err=%v tx=%+v credits=%d", err, hold, user.credits)
		}
	})
}

func TestFulfillOrderLegacy_SSEStatusUpdateOnDigiflazzSuccess(t *testing.T) {
	tx := characterizationTx()
	digi := &characterizationDigiflazz{createResp: characterizationDigiflazzResponse("Sukses", "ok", "SN-SSE")}
	repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
	nom := &domain.Nominal{ID: 11, BasePrice: 1, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}
	svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, &characterizationUserRepo{}, digi, nil)

	ch := make(sse.ClientChan, 1)
	sse.GlobalHub.Register(tx.InvoiceNumber, ch)
	defer sse.GlobalHub.Unregister(tx.InvoiceNumber, ch)
	if err := svc.FulfillOrder(tx); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		if !strings.Contains(event, `"type":"status_update"`) || !strings.Contains(event, `"status":"success"`) || !strings.Contains(event, `"sn":"SN-SSE"`) {
			t.Fatalf("unexpected SSE event: %s", event)
		}
	case <-time.After(time.Second):
		t.Fatal("expected status_update SSE event")
	}
}

type registryTestProvider struct {
	code                      string
	purchaseResult            *provider.Result
	purchaseErr               error
	checkResult               *provider.Result
	checkErr                  error
	callbackResult            *provider.Result
	callbackRefID             string
	callbackErr               error
	callbackCalls             int
	purchaseCalls, checkCalls int
	lastPurchase              provider.PurchaseRequest
	lastStatus                provider.StatusRequest
}

func (p *registryTestProvider) Code() string { return p.code }
func (p *registryTestProvider) Purchase(_ context.Context, request provider.PurchaseRequest) (*provider.Result, error) {
	p.purchaseCalls++
	p.lastPurchase = request
	return p.purchaseResult, p.purchaseErr
}
func (p *registryTestProvider) CheckStatus(_ context.Context, request provider.StatusRequest) (*provider.Result, error) {
	p.checkCalls++
	p.lastStatus = request
	return p.checkResult, p.checkErr
}
func (*registryTestProvider) Balance(context.Context) (float64, error) { return 0, nil }
func (p *registryTestProvider) ParseCallback(*http.Request) (*provider.Result, string, error) {
	p.callbackCalls++
	return p.callbackResult, p.callbackRefID, p.callbackErr
}

func newRegistryForTest(t *testing.T, p provider.Provider) *provider.Registry {
	t.Helper()
	r := provider.NewRegistry()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryIsTheOnlyTransactionProviderPath(t *testing.T) {
	nom := &domain.Nominal{ID: 11, BasePrice: 1, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}

	registryTx := characterizationTx()
	registryProvider := &registryTestProvider{code: provider.DigiflazzCode, purchaseResult: &provider.Result{Status: provider.StatusSuccess, ProviderOrderID: "REG-1", SN: "REG-SN", Message: "ok", ProviderStatus: "Sukses", StatusReason: "Provider completed transaction successfully", UpdateSN: true, IncludeSNInSuccessEvent: true}}
	txRepo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{registryTx.ID: registryTx}}
	registrySvc := newCharacterizationService(txRepo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, &characterizationUserRepo{}, nil, nil)
	registrySvc.legacyCharacterization = false
	registrySvc.providerRegistry = newRegistryForTest(t, registryProvider)
	if err := registrySvc.FulfillOrder(registryTx); err != nil || registryProvider.purchaseCalls != 1 || registryTx.Status != domain.StatusSuccess || registryTx.ProviderOrderID != "REG-1" || registryTx.SN != "REG-SN" {
		t.Fatalf("err=%v tx=%+v provider=%d", err, registryTx, registryProvider.purchaseCalls)
	}
}

func TestRegistryApplyResultCharacterization(t *testing.T) {
	cases := []struct {
		name                  string
		result                *provider.Result
		callErr               error
		want                  domain.TransactionStatus
		wantRetry, wantRefund bool
	}{
		{"pending", &provider.Result{Status: provider.StatusPending, ProviderStatus: "Pending", StatusReason: "Waiting for provider callback"}, nil, domain.StatusProcessing, false, false},
		{"hold", &provider.Result{Status: provider.StatusFailedHold, ProviderStatus: "Pending (Kendala Provider)", StatusReason: "Digiflazz: saldo"}, nil, domain.StatusProcessing, false, false},
		{"final", &provider.Result{Status: provider.StatusFailedFinal, ProviderStatus: "Gagal", StatusReason: "Provider failed: id salah", IncludeCompletedAtInFailedEvent: true}, nil, domain.StatusFailed, false, true},
		{"provider error", nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary, ProviderStatus: "Pending", Message: "connection down", Cause: errors.New("connection down")}, domain.StatusProcessing, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := characterizationTx()
			repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
			user := &characterizationUserRepo{}
			svc := newCharacterizationService(repo, nil, nil, nil, user, nil, nil)
			err := svc.applyResult(tx, tc.result, tc.callErr, providerResultSourceFulfill)
			if tx.Status != tc.want || (tx.RetryCount == 1) != tc.wantRetry || (user.credits == 1) != tc.wantRefund || repo.updates != 1 {
				t.Fatalf("err=%v tx=%+v credits=%d updates=%d", err, tx, user.credits, repo.updates)
			}
			if tc.want == domain.StatusFailed && tx.CompletedAt == nil {
				t.Fatal("expected completed time")
			}
			if tc.callErr != nil && (err == nil || err.Error() != "connection down") {
				t.Fatalf("expected cause, got %v", err)
			}
		})
	}
}

func TestRegistryReconcileAppliesProviderResultsWithoutPurchase(t *testing.T) {
	cases := []struct {
		name       string
		result     *provider.Result
		checkErr   error
		wantStatus domain.TransactionStatus
		wantRefund bool
		wantErr    bool
	}{
		{"success", &provider.Result{Status: provider.StatusSuccess, ProviderOrderID: "DF-1", SN: "SN-1", ProviderStatus: "Sukses", StatusReason: "Digiflazz: terkonfirmasi sukses", UpdateSN: true}, nil, domain.StatusSuccess, false, false},
		{"pending", &provider.Result{Status: provider.StatusPending, ProviderStatus: "Pending", StatusReason: "Waiting for provider callback"}, nil, domain.StatusProcessing, false, false},
		{"final failure refunds once", &provider.Result{Status: provider.StatusFailedFinal, ProviderStatus: "Gagal", StatusReason: "Digiflazz gagal: ID salah"}, nil, domain.StatusFailed, true, false},
		{"hold failure remains processing", &provider.Result{Status: provider.StatusFailedHold, ProviderStatus: "Pending (Kendala Provider)", StatusReason: "Digiflazz: saldo provider"}, nil, domain.StatusProcessing, false, false},
		{"provider error remains hold", nil, &provider.ProviderError{Status: provider.StatusFailedHold, Kind: provider.ErrorTimeout, ProviderStatus: "Pending", Message: "timeout", Cause: errors.New("timeout")}, domain.StatusProcessing, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := characterizationTx()
			tx.ProviderOrderID = "EXISTING-ORDER"
			repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
			user := &characterizationUserRepo{}
			p := &registryTestProvider{code: provider.DigiflazzCode, checkResult: tc.result, checkErr: tc.checkErr}
			nom := &domain.Nominal{ID: 11, BasePrice: 1, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}
			svc := newCharacterizationService(repo, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, user, nil, nil)
			svc.providerRegistry = newRegistryForTest(t, p)

			_, err := svc.ReconcileProcessingTransaction(tx.ID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if p.purchaseCalls != 0 || p.checkCalls != 1 || p.lastStatus.ProviderOrderID != "EXISTING-ORDER" {
				t.Fatalf("purchase=%d check=%d request=%+v", p.purchaseCalls, p.checkCalls, p.lastStatus)
			}
			if tx.Status != tc.wantStatus || (user.credits == 1) != tc.wantRefund {
				t.Fatalf("tx=%+v credits=%d", tx, user.credits)
			}
		})
	}
}

func TestRegistryReconcileSkipsAlreadyFinalTransaction(t *testing.T) {
	tx := characterizationTx()
	tx.Status = domain.StatusSuccess
	p := &registryTestProvider{code: provider.DigiflazzCode}
	nom := &domain.Nominal{ID: 11, BasePrice: 1, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}
	svc := newCharacterizationService(&characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, &characterizationUserRepo{}, nil, nil)
	svc.legacyCharacterization = false
	svc.providerRegistry = newRegistryForTest(t, p)

	if got, err := svc.ReconcileProcessingTransaction(tx.ID); err != nil || got != tx || p.checkCalls != 0 {
		t.Fatalf("got=%+v err=%v checkCalls=%d", got, err, p.checkCalls)
	}
}

func TestRegistryPurchasePassesExistingOrderIDWithoutCreatingBusinessDuplicate(t *testing.T) {
	tx := characterizationTx()
	tx.ProviderOrderID = "EXISTING-ORDER"
	nom := &domain.Nominal{ID: 11, BasePrice: 1, ProviderProductCode: "SKU", Provider: &domain.Provider{Code: "DIGIFLAZZ"}}
	p := &registryTestProvider{code: provider.DigiflazzCode, purchaseResult: &provider.Result{Status: provider.StatusPending, ProviderStatus: "Pending", StatusReason: "Waiting for provider callback"}}
	svc := newCharacterizationService(&characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}, &characterizationNominalRepo{nominals: map[uint]*domain.Nominal{11: nom}}, nil, nil, &characterizationUserRepo{}, nil, nil)
	svc.legacyCharacterization = false
	svc.providerRegistry = newRegistryForTest(t, p)
	if err := svc.FulfillOrder(tx); err != nil || p.purchaseCalls != 1 || p.lastPurchase.ExistingProviderOrderID != "EXISTING-ORDER" {
		t.Fatalf("err=%v calls=%d request=%+v", err, p.purchaseCalls, p.lastPurchase)
	}
}

func TestRegistryCallbackFinalResultIsIdempotent(t *testing.T) {
	userID := uint(7)
	tx := characterizationTx()
	tx.Status = domain.StatusProcessing
	tx.PaymentMethod = "SALDO"
	tx.UserID = &userID
	result := &provider.Result{
		Status: provider.StatusFailedFinal, ProviderOrderID: "INV-456", ProviderStatus: "REFUNDED",
		StatusReason: "OtoMax: transaksi gagal final", Raw: []byte(`{"trx_id":"REF-123","invoice_number":"INV-456"}`),
	}
	p := &registryTestProvider{code: "FFZSTORE", callbackResult: result, callbackRefID: tx.RefID}
	repo := &characterizationTxRepo{byID: map[uint]*domain.Transaction{tx.ID: tx}}
	user := &characterizationUserRepo{}
	svc := newCharacterizationService(repo, nil, nil, nil, user, nil, nil)
	svc.legacyCharacterization = false
	svc.providerRegistry = newRegistryForTest(t, p)

	for i := 0; i < 2; i++ {
		if _, _, err := svc.HandleProviderCallback("ffzstore", httptest.NewRequest(http.MethodGet, "/callback", nil)); err != nil {
			t.Fatalf("callback %d: %v", i+1, err)
		}
	}
	if tx.Status != domain.StatusFailed || tx.ProviderOrderID != "INV-456" || user.credits != 1 || p.callbackCalls != 2 {
		t.Fatalf("tx=%+v credits=%d parserCalls=%d", tx, user.credits, p.callbackCalls)
	}
}
