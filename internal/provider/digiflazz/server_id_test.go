package digiflazz

import (
	"context"
	"testing"

	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type customerNoRecorder struct {
	fakeBuyer
	checkedCustomerNo string
}

func (r *customerNoRecorder) CheckTransactionStatus(_ string, _ string, customerNo string) (*service.DigiflazzTransactionResponse, error) {
	r.checkedCustomerNo = customerNo
	return response("Sukses", "00", "ok"), nil
}

// Regresi: order yang sudah punya ProviderOrderID dulu dikirim ulang ke Digiflazz
// tanpa ServerID, sehingga customer_no hanya berisi customer_id.
func TestPurchaseExistingOrderKeepsServerIDInCustomerNo(t *testing.T) {
	buyer := &customerNoRecorder{}
	_, err := New(buyer).Purchase(context.Background(), provider.PurchaseRequest{
		RefID: "REF-1", ProductCode: "ML86", CustomerID: "447512607", ServerID: "2001",
		ExistingProviderOrderID: "REF-1",
	})
	if err != nil {
		t.Fatalf("Purchase returned error: %v", err)
	}
	if buyer.checkedCustomerNo != "4475126072001" {
		t.Fatalf("customer_no = %q, want %q", buyer.checkedCustomerNo, "4475126072001")
	}
}
