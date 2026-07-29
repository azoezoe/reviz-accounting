package models

import "testing"

func TestCalculateQuote(t *testing.T) {
	q := ProjectQuote{
		DiscountType:  "percent",
		DiscountValue: 10,
		TaxRate:       5,
		Items: []QuoteItem{
			{LineTotalCents: 100000},
			{LineTotalCents: 50000},
		},
	}
	calculateQuote(&q)
	if q.SubtotalCents != 150000 || q.DiscountCents != 15000 || q.TaxCents != 6750 || q.TotalCents != 141750 {
		t.Fatalf("unexpected totals: %+v", q)
	}
}

func TestCalculateQuoteCapsFixedDiscount(t *testing.T) {
	q := ProjectQuote{DiscountType: "amount", DiscountValue: 999, TaxRate: 5, Items: []QuoteItem{{LineTotalCents: 1000}}}
	calculateQuote(&q)
	if q.DiscountCents != 1000 || q.TotalCents != 0 {
		t.Fatalf("discount should be capped at subtotal: %+v", q)
	}
}
