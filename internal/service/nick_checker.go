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

// firstTopupGoods = ID goods di mobapay (shelf "Diamond Ganda pada Top Up Pertama").
type firstTopupGood struct {
	Label   string
	GoodsID int
}

// ID goods di mobapay (good_list).
const (
	goodWeeklyPass   = 120991 // Weekly Diamond Pass
	goodWeeklyElite  = 131189 // Weekly Elite Bundle
	goodMonthlyElite = 131191 // di mobapay: "Monthly Epic Bundle"
)

var (
	reDigits = regexp.MustCompile(`^\d{3,15}$`)

	firstTopupGoods = []firstTopupGood{
		{"50 + 50", 126306},
		{"150 + 150", 126307},
		{"250 + 250", 126315},
		{"500 + 500", 126316},
	}

		countryNames = map[string]string{
		"ad": "Andorra", "ae": "United Arab Emirates", "af": "Afghanistan", "ag": "Antigua and Barbuda",
		"ai": "Anguilla", "al": "Albania", "am": "Armenia", "ao": "Angola", "aq": "Antarctica",
		"ar": "Argentina", "as": "American Samoa", "at": "Austria", "au": "Australia", "aw": "Aruba",
		"ax": "Aland Islands", "az": "Azerbaijan", "ba": "Bosnia and Herzegovina", "bb": "Barbados",
		"bd": "Bangladesh", "be": "Belgium", "bf": "Burkina Faso", "bg": "Bulgaria", "bh": "Bahrain",
		"bi": "Burundi", "bj": "Benin", "bl": "Saint Barthelemy", "bm": "Bermuda", "bn": "Brunei",
		"bo": "Bolivia", "bq": "Caribbean Netherlands", "br": "Brazil", "bs": "Bahamas", "bt": "Bhutan",
		"bv": "Bouvet Island", "bw": "Botswana", "by": "Belarus", "bz": "Belize", "ca": "Canada",
		"cc": "Cocos Islands", "cd": "DR Congo", "cf": "Central African Republic", "cg": "Congo",
		"ch": "Switzerland", "ci": "Cote d'Ivoire", "ck": "Cook Islands", "cl": "Chile", "cm": "Cameroon",
		"cn": "China", "co": "Colombia", "cr": "Costa Rica", "cu": "Cuba", "cv": "Cape Verde",
		"cw": "Curacao", "cx": "Christmas Island", "cy": "Cyprus", "cz": "Czechia", "de": "Germany",
		"dj": "Djibouti", "dk": "Denmark", "dm": "Dominica", "do": "Dominican Republic", "dz": "Algeria",
		"ec": "Ecuador", "ee": "Estonia", "eg": "Egypt", "eh": "Western Sahara", "er": "Eritrea",
		"es": "Spain", "et": "Ethiopia", "fi": "Finland", "fj": "Fiji", "fk": "Falkland Islands",
		"fm": "Micronesia", "fo": "Faroe Islands", "fr": "France", "ga": "Gabon", "gb": "United Kingdom",
		"gd": "Grenada", "ge": "Georgia", "gf": "French Guiana", "gg": "Guernsey", "gh": "Ghana",
		"gi": "Gibraltar", "gl": "Greenland", "gm": "Gambia", "gn": "Guinea", "gp": "Guadeloupe",
		"gq": "Equatorial Guinea", "gr": "Greece", "gs": "South Georgia", "gt": "Guatemala", "gu": "Guam",
		"gw": "Guinea-Bissau", "gy": "Guyana", "hk": "Hong Kong", "hm": "Heard Island", "hn": "Honduras",
		"hr": "Croatia", "ht": "Haiti", "hu": "Hungary", "id": "Indonesia", "ie": "Ireland",
		"il": "Israel", "im": "Isle of Man", "in": "India", "io": "British Indian Ocean Territory",
		"iq": "Iraq", "ir": "Iran", "is": "Iceland", "it": "Italy", "je": "Jersey", "jm": "Jamaica",
		"jo": "Jordan", "jp": "Japan", "ke": "Kenya", "kg": "Kyrgyzstan", "kh": "Cambodia",
		"ki": "Kiribati", "km": "Comoros", "kn": "Saint Kitts and Nevis", "kp": "North Korea",
		"kr": "South Korea", "kw": "Kuwait", "ky": "Cayman Islands", "kz": "Kazakhstan", "la": "Laos",
		"lb": "Lebanon", "lc": "Saint Lucia", "li": "Liechtenstein", "lk": "Sri Lanka", "lr": "Liberia",
		"ls": "Lesotho", "lt": "Lithuania", "lu": "Luxembourg", "lv": "Latvia", "ly": "Libya",
		"ma": "Morocco", "mc": "Monaco", "md": "Moldova", "me": "Montenegro", "mf": "Saint Martin",
		"mg": "Madagascar", "mh": "Marshall Islands", "mk": "North Macedonia", "ml": "Mali",
		"mm": "Myanmar", "mn": "Mongolia", "mo": "Macao", "mp": "Northern Mariana Islands",
		"mq": "Martinique", "mr": "Mauritania", "ms": "Montserrat", "mt": "Malta", "mu": "Mauritius",
		"mv": "Maldives", "mw": "Malawi", "mx": "Mexico", "my": "Malaysia", "mz": "Mozambique",
		"na": "Namibia", "nc": "New Caledonia", "ne": "Niger", "nf": "Norfolk Island", "ng": "Nigeria",
		"ni": "Nicaragua", "nl": "Netherlands", "no": "Norway", "np": "Nepal", "nr": "Nauru",
		"nu": "Niue", "nz": "New Zealand", "om": "Oman", "pa": "Panama", "pe": "Peru",
		"pf": "French Polynesia", "pg": "Papua New Guinea", "ph": "Philippines", "pk": "Pakistan",
		"pl": "Poland", "pm": "Saint Pierre and Miquelon", "pn": "Pitcairn Islands", "pr": "Puerto Rico",
		"ps": "Palestine", "pt": "Portugal", "pw": "Palau", "py": "Paraguay", "qa": "Qatar",
		"re": "Reunion", "ro": "Romania", "rs": "Serbia", "ru": "Russia", "rw": "Rwanda",
		"sa": "Saudi Arabia", "sb": "Solomon Islands", "sc": "Seychelles", "sd": "Sudan", "se": "Sweden",
		"sg": "Singapore", "sh": "Saint Helena", "si": "Slovenia", "sj": "Svalbard and Jan Mayen",
		"sk": "Slovakia", "sl": "Sierra Leone", "sm": "San Marino", "sn": "Senegal", "so": "Somalia",
		"sr": "Suriname", "ss": "South Sudan", "st": "Sao Tome and Principe", "sv": "El Salvador",
		"sx": "Sint Maarten", "sy": "Syria", "sz": "Eswatini", "tc": "Turks and Caicos Islands",
		"td": "Chad", "tf": "French Southern Territories", "tg": "Togo", "th": "Thailand",
		"tj": "Tajikistan", "tk": "Tokelau", "tl": "Timor-Leste", "tm": "Turkmenistan", "tn": "Tunisia",
		"to": "Tonga", "tr": "Turkey", "tt": "Trinidad and Tobago", "tv": "Tuvalu", "tw": "Taiwan",
		"tz": "Tanzania", "ua": "Ukraine", "ug": "Uganda", "um": "US Minor Outlying Islands",
		"us": "United States", "uy": "Uruguay", "uz": "Uzbekistan", "va": "Vatican City",
		"vc": "Saint Vincent and the Grenadines", "ve": "Venezuela", "vg": "British Virgin Islands",
		"vi": "US Virgin Islands", "vn": "Vietnam", "vu": "Vanuatu", "wf": "Wallis and Futuna",
		"ws": "Samoa", "xk": "Kosovo", "ye": "Yemen", "yt": "Mayotte", "za": "South Africa",
		"zm": "Zambia", "zw": "Zimbabwe",
	}
)

const (
	gopayBase      = "https://gopay.co.id/games/v1/order"
	mobapayShopURL = "https://api.mobapay.com/api/app_shop"
)

type NickResult struct {
	UserID, ZoneID, Nickname, Region      string
	WeeklyPass, WeeklyElite, MonthlyElite LimitStatus
	FirstTopup                            []struct {
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

// Check: nickname + region dari gopay, semua limit dari 1 request mobapay.
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

	// Step 2: semua limit dari mobapay.
	st := n.fetchMobapayLimits(ctx, userID, zoneID)
	get := func(id int) LimitStatus {
		if v, ok := st[id]; ok {
			return v
		}
		return LimitUnknown
	}

	res.WeeklyPass = get(goodWeeklyPass)
	res.WeeklyElite = get(goodWeeklyElite)
	res.MonthlyElite = get(goodMonthlyElite)

	for _, g := range firstTopupGoods {
		res.FirstTopup = append(res.FirstTopup, struct {
			Label  string
			Status LimitStatus
		}{g.Label, get(g.GoodsID)})
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

// ---- Step 2: limit weekly pass, bundle, first top up (mobapay) ----

type mobaGood struct {
	ID         int `json:"id"`
	GoodsLimit struct {
		ReachedLimit bool `json:"reached_limit"`
	} `json:"goods_limit"`
}

func (n *NickChecker) fetchMobapayLimits(ctx context.Context, userID, zoneID string) map[int]LimitStatus {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mobapayShopURL+"?"+q.Encode(), nil)
	if err != nil {
		log.Printf("[CekNick] mobapay request: %v", err)
		return out
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "id-ID,id;q=0.9,en;q=0.8")
	req.Header.Set("Origin", "https://www.mobapay.com")
	req.Header.Set("Referer", "https://www.mobapay.com/")

	resp, err := n.http.Do(req)
	if err != nil {
		log.Printf("[CekNick] mobapay error: %v", err)
		return out
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		log.Printf("[CekNick] mobapay HTTP %d: %s", resp.StatusCode, snippet)
		return out
	}

	var r struct {
		Data struct {
			ShopInfo struct {
				GoodList      []mobaGood `json:"good_list"`
				ShelfLocation []struct {
					Goods []mobaGood `json:"goods"`
				} `json:"shelf_location"`
			} `json:"shop_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		log.Printf("[CekNick] parse mobapay: %v", err)
		return out
	}

	set := func(g mobaGood) {
		if g.GoodsLimit.ReachedLimit {
			out[g.ID] = LimitReached
		} else {
			out[g.ID] = LimitAvailable
		}
	}
	for _, g := range r.Data.ShopInfo.GoodList {
		set(g) // weekly pass & bundle
	}
	for _, s := range r.Data.ShopInfo.ShelfLocation {
		for _, g := range s.Goods {
			set(g) // first top up
		}
	}
	log.Printf("[CekNick] mobapay ok: %d goods terbaca", len(out))
	return out
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

// escapeMD meng-escape karakter khusus Markdown Telegram (mode legacy "Markdown").
func escapeMD(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`_`, `\_`,
		`*`, `\*`,
		"`", "\\`",
		`[`, `\[`,
	)
	return r.Replace(s)
}

// Format menghasilkan teks balasan Telegram (Markdown).
func (r *NickResult) Format() string {
	var sb strings.Builder
	sb.WriteString("*VALIDASI BERHASIL*\n")
	fmt.Fprintf(&sb, "Id : %s\n", r.UserID)
	fmt.Fprintf(&sb, "Server : %s\n", r.ZoneID)
	fmt.Fprintf(&sb, "Nickname : %s\n", escapeMD(r.Nickname))
	fmt.Fprintf(&sb, "Region : %s\n\n", escapeMD(r.Region))

	fmt.Fprintf(&sb, "*> Weekly Pass : %s*\n", r.WeeklyPass)
	fmt.Fprintf(&sb, "*> Weekly Elite Bundle : %s*\n", r.WeeklyElite)
	fmt.Fprintf(&sb, "*> Monthly Elite Bundle : %s*\n", r.MonthlyElite)
	sb.WriteString("> First TopUp :\n")
	for _, f := range r.FirstTopup {
		fmt.Fprintf(&sb, "%s : %s\n", f.Label, f.Status)
	}
	return sb.String()
}