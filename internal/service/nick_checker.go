package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LimitStatus int

const (
	LimitUnknown LimitStatus = iota
	LimitAvailable
	LimitReached
)

func (l LimitStatus) String() string {
	switch l {
	case LimitAvailable:
		return "✅ Tersedia"
	case LimitReached:
		return "❌ Limit"
	default:
		return "⚠️ Tidak diketahui"
	}
}

// inquiryProduct = produk yang dicek lewat endpoint inquiry gopay.
type inquiryProduct struct {
	Label         string
	ProductItemID int
}

// firstTopupGoods = ID goods di mobapay (shelf "Diamond Ganda pada Top Up Pertama").
type firstTopupGood struct {
	Label   string
	GoodsID int
}

var (
	reDigits = regexp.MustCompile(`^\d{3,15}$`)

	// ISI productItemId Monthly Elite Bundle yang benar (belum ada di data kamu).
	weeklyPass   = inquiryProduct{"Weekly Pass", 366}
	weeklyElite  = inquiryProduct{"Weekly Elite Bundle", 2175}
	monthlyElite = inquiryProduct{"Monthly Elite Bundle", 0 /* TODO */}

	firstTopupGoods = []firstTopupGood{
		{"50 + 50", 126306},
		{"150 + 150", 126307},
		{"250 + 250", 126315},
		{"500 + 500", 126316},
	}

	countryNames = map[string]string{
		"id": "Indonesia", "my": "Malaysia", "sg": "Singapore", "ph": "Philippines",
		"th": "Thailand", "vn": "Vietnam", "kh": "Cambodia", "mm": "Myanmar",
		"br": "Brazil", "ru": "Russia", "tr": "Turkey",
	}
)

const (
	gopayBase       = "https://gopay.co.id/games/v1/order"
	mobapayShopURL  = "https://api.mobapay.com/api/app_shop"
	inquiryProdID   = 19
	inquiryPayChan  = 91
	inquiryDummyHP  = "628123456789"
	limitErrMessage = "Error_Act_Subscrip_OneTimeProd_LeftTimeLimit"
)

type NickResult struct {
	UserID, ZoneID, Nickname, Region string
	WeeklyPass, WeeklyElite, MonthlyElite LimitStatus
	FirstTopup []struct {
		Label  string
		Status LimitStatus
	}
}

type NickChecker struct {
	http *http.Client
}

func NewNickChecker() *NickChecker {
	return &NickChecker{http: &http.Client{Timeout: 12 * time.Second}}
}

// Check menjalankan 3 request secara paralel.
func (n *NickChecker) Check(ctx context.Context, userID, zoneID string) (*NickResult, error) {
	if !reDigits.MatchString(userID) || !reDigits.MatchString(zoneID) {
		return nil, fmt.Errorf("format ID/Server tidak valid")
	}

	res := &NickResult{UserID: userID, ZoneID: zoneID}

	// Step 1 dulu: kalau akun tidak valid, tidak perlu cek yang lain.
	nick, country, err := n.fetchAccount(ctx, userID, zoneID)
	if err != nil {
		return nil, err
	}
	res.Nickname = nick
	res.Region = countryName(country)

	var wg sync.WaitGroup
	wg.Add(5)

	go func() { defer wg.Done(); res.WeeklyPass = n.inquiryLimit(ctx, weeklyPass, userID, zoneID) }()
	go func() { defer wg.Done(); res.WeeklyElite = n.inquiryLimit(ctx, weeklyElite, userID, zoneID) }()
	go func() { defer wg.Done(); res.MonthlyElite = n.inquiryLimit(ctx, monthlyElite, userID, zoneID) }()

	var ft map[int]LimitStatus
	go func() {
		defer wg.Done()
		ft = n.fetchFirstTopup(ctx, userID, zoneID)
	}()
	wg.Done() // slot kosong (dipakai agar jumlah goroutine jelas); lihat catatan di bawah
	wg.Wait()

	for _, g := range firstTopupGoods {
		st, ok := ft[g.GoodsID]
		if !ok {
			st = LimitUnknown
		}
		res.FirstTopup = append(res.FirstTopup, struct {
			Label  string
			Status LimitStatus
		}{g.Label, st})
	}
	return res, nil
}

// ---- Step 1: nickname + negara ----

func (n *NickChecker) fetchAccount(ctx context.Context, userID, zoneID string) (nick, country string, err error) {
	payload := map[string]any{
		"code": "MOBILE_LEGENDS",
		"data": map[string]string{"userId": userID, "zoneId": zoneID},
	}
	status, body, err := n.postJSON(ctx, gopayBase+"/user-account", payload)
	if err != nil {
		return "", "", fmt.Errorf("gagal menghubungi server: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", "", fmt.Errorf("akun tidak ditemukan (HTTP %d)", status)
	}

	var r struct {
		Message string `json:"message"`
		Data    struct {
			CountryOrigin string `json:"countryOrigin"`
			Username      string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Data.Username == "" {
		return "", "", fmt.Errorf("akun tidak ditemukan")
	}
	// "Wang+Linnn," -> "Wang Linnn,"
	name, uerr := url.QueryUnescape(r.Data.Username)
	if uerr != nil {
		name = r.Data.Username
	}
	return name, r.Data.CountryOrigin, nil
}

// ---- Step 2: first top up (mobapay) ----

func (n *NickChecker) fetchFirstTopup(ctx context.Context, userID, zoneID string) map[int]LimitStatus {
	out := map[int]LimitStatus{}

	q := url.Values{}
	q.Set("app_id", "100000")
	q.Set("game_user_key", userID)
	q.Set("game_server_key", zoneID)
	q.Set("country", "ID")
	q.Set("language", "id")
	q.Set("network", "")
	q.Set("net", "")
	q.Set("coupon_id", "")
	q.Set("shop_id", "")

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, mobapayShopURL+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := n.http.Do(req)
	if err != nil {
		log.Printf("[CekNick] mobapay error: %v", err)
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var r struct {
		Data struct {
			ShelfLocation []struct {
				Goods []struct {
					ID         int `json:"id"`
					GoodsLimit struct {
						ReachedLimit bool `json:"reached_limit"`
					} `json:"goods_limit"`
				} `json:"goods"`
			} `json:"shelf_location"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		log.Printf("[CekNick] parse mobapay: %v", err)
		return out
	}

	for _, shelf := range r.Data.ShelfLocation {
		for _, g := range shelf.Goods {
			if g.GoodsLimit.ReachedLimit {
				out[g.ID] = LimitReached
			} else {
				out[g.ID] = LimitAvailable
			}
		}
	}
	// SKU yang tidak ada di shelf => tetap LimitUnknown.
	return out
}

// ---- Step 3: weekly pass / bundle (inquiry gopay) ----

func (n *NickChecker) inquiryLimit(ctx context.Context, p inquiryProduct, userID, zoneID string) LimitStatus {
	if p.ProductItemID == 0 {
		return LimitUnknown
	}
	payload := map[string]any{
		"productId":          inquiryProdID,
		"productItemId":      p.ProductItemID,
		"data":               map[string]string{"userId": userID, "zoneId": zoneID},
		"paymentChannelId":   inquiryPayChan,
		"phoneNumber":        inquiryDummyHP,
		"voucher":            "",
		"referralCode":       "",
		"paymentPhoneNumber": "",
		"quantity":           1,
	}
	status, body, err := n.postJSON(ctx, gopayBase+"/inquiry", payload)
	if err != nil {
		log.Printf("[CekNick] inquiry %s error: %v", p.Label, err)
		return LimitUnknown
	}

	switch {
	case status == http.StatusCreated || status == http.StatusOK:
		return LimitAvailable
	case status == http.StatusNotFound && strings.Contains(string(body), limitErrMessage):
		return LimitReached
	default:
		log.Printf("[CekNick] inquiry %s status=%d body=%s", p.Label, status, string(body))
		return LimitUnknown
	}
}

// ---- helpers ----

func (n *NickChecker) postJSON(ctx context.Context, u string, payload any) (int, []byte, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := n.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}

func countryName(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if v, ok := countryNames[code]; ok {
		return v
	}
	if code == "" {
		return "-"
	}
	return strings.ToUpper(code)
}

// Format menghasilkan teks balasan Telegram (plain text).
func (r *NickResult) Format() string {
	var sb strings.Builder
	sb.WriteString("VALIDASI BERHASIL\n")
	fmt.Fprintf(&sb, "Id : %s\n", r.UserID)
	fmt.Fprintf(&sb, "Server : %s\n", r.ZoneID)
	fmt.Fprintf(&sb, "Nickname : %s\n", r.Nickname)
	fmt.Fprintf(&sb, "Region : %s\n\n", r.Region)

	fmt.Fprintf(&sb, "> Weekly Pass : %s\n", r.WeeklyPass)
	fmt.Fprintf(&sb, "> Weekly Elite Bundle : %s\n", r.WeeklyElite)
	fmt.Fprintf(&sb, "> Monthly Elite Bundle : %s\n", r.MonthlyElite)
	sb.WriteString("> First TopUp :\n")
	for _, f := range r.FirstTopup {
		fmt.Fprintf(&sb, "%s : %s\n", f.Label, f.Status)
	}
	return sb.String()
}