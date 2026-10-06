package otomaxhttp

import "testing"

func TestParseUser(t *testing.T) {
	info, err := ParseUser([]byte("TopupKuy. topupkuy@topupkuy.id. plan Platinum Member. balance 1234344"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "TopupKuy" || info.Email != "topupkuy@topupkuy.id" || info.Plan != "Platinum Member" || info.Balance != 1234344 {
		t.Fatalf("unexpected user info: %+v", info)
	}
}

func TestParseResponse(t *testing.T) {
	cases := []struct {
		name, raw, status, orderID, sn string
	}{
		{
			name:   "order pending",
			raw:    "R#123424324 S1_1187.123456789|1234, status PENDING. RefId : ML_1680405885_1234 . Sisa saldo 12345",
			status: "PENDING", orderID: "ML_1680405885_1234",
		},
		{
			name:   "status success",
			raw:    "R#1234243244 APIKUY_XX_1679528285_4321 S1_1187.123456789(1234), status SUCCESS. Nickname - 123456789(1234) . RefId: XX_1679528285_4321 . Sisa saldo 12343",
			status: "SUCCESS", orderID: "XX_1679528285_4321", sn: "Nickname - 123456789(1234)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseResponse([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Status != tc.status || parsed.ProviderOrderID != tc.orderID || parsed.SerialNumber != tc.sn || parsed.Balance == 0 {
				t.Fatalf("unexpected parsed response: %+v", parsed)
			}
		})
	}
}

func TestParseResponseRejectsMalformedPayload(t *testing.T) {
	if _, err := ParseResponse([]byte("provider says hello")); err == nil {
		t.Fatal("expected malformed response error")
	}
}
