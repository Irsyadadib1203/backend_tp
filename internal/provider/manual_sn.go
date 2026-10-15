package provider

import (
	"fmt"
	"strings"
)

// BuildManualSN membuat SN dengan format:
// <nickname atau ID pelanggan>. RefId : <invoice_number>
func BuildManualSN(nickname, customerID, invoiceNumber string) string {
	target := strings.TrimSpace(nickname)
	if target == "" {
		target = strings.TrimSpace(customerID)
	}

	return fmt.Sprintf("%s. RefId : %s", target, invoiceNumber)
}
