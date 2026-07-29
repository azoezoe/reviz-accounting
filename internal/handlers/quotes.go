package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/money"
)

type quoteItemView struct {
	ID, UnitPriceCents, LineTotalCents int64
	Description, Unit                  string
	Quantity                           float64
}
type quoteView struct {
	ID, VersionNo, ParentQuoteID                                                 int64
	QuoteNo, Title, ClientName, IssuerName, Currency, DiscountType, Note, Status string
	DiscountValue, TaxRate                                                       float64
	SubtotalCents, DiscountCents, TaxCents, TotalCents                           int64
	ProjectID                                                                    int64
	Items                                                                        []quoteItemView
}

func (s *Server) loadQuote(id int64) (quoteView, error) {
	var q quoteView
	err := s.DB.QueryRow(`SELECT id,quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,status,version_no,COALESCE(parent_quote_id,0),COALESCE(project_id,0) FROM quotes WHERE id=$1`, id).
		Scan(&q.ID, &q.QuoteNo, &q.Title, &q.ClientName, &q.IssuerName, &q.Currency, &q.DiscountType, &q.DiscountValue, &q.TaxRate, &q.Note, &q.Status, &q.VersionNo, &q.ParentQuoteID, &q.ProjectID)
	if err != nil {
		return q, err
	}
	rows, err := s.DB.Query(`SELECT id,description,quantity,unit,unit_price_cents FROM quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, id)
	if err != nil {
		return q, err
	}
	defer rows.Close()
	for rows.Next() {
		var x quoteItemView
		if err := rows.Scan(&x.ID, &x.Description, &x.Quantity, &x.Unit, &x.UnitPriceCents); err != nil {
			return q, err
		}
		x.LineTotalCents = int64(x.Quantity * float64(x.UnitPriceCents))
		q.SubtotalCents += x.LineTotalCents
		q.Items = append(q.Items, x)
	}
	if err := rows.Err(); err != nil {
		return q, err
	}
	if q.DiscountType == "percent" {
		q.DiscountCents = int64(float64(q.SubtotalCents) * q.DiscountValue / 100)
	} else {
		q.DiscountCents = int64(q.DiscountValue * 100)
	}
	taxable := q.SubtotalCents - q.DiscountCents
	q.TaxCents = int64(float64(taxable) * q.TaxRate / 100)
	q.TotalCents = taxable + q.TaxCents
	return q, nil
}

func (s *Server) quotesList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT id FROM quotes ORDER BY id DESC`)
	if err != nil {
		s.error500(w, err)
		return
	}
	defer rows.Close()
	var quotes []quoteView
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			s.error500(w, err)
			return
		}
		q, err := s.loadQuote(id)
		if err != nil {
			s.error500(w, err)
			return
		}
		quotes = append(quotes, q)
	}
	s.render(w, r, "quotes.html", map[string]any{"Title": "報價單", "Crumbs": []string{"報價單"}, "Active": "quotes", "Quotes": quotes, "NextQuoteNo": nextStandaloneQuoteNo(s), "CompanyName": companyName(s)})
}
func companyName(s *Server) string {
	var n string
	_ = s.DB.QueryRow(`SELECT value FROM settings WHERE key='company_name'`).Scan(&n)
	return n
}
func nextStandaloneQuoteNo(s *Server) string {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*)+1 FROM quotes`).Scan(&n)
	return fmt.Sprintf("Q-%d-%03d", time.Now().Year(), n)
}
func (s *Server) quoteDetail(w http.ResponseWriter, r *http.Request) {
	q, err := s.loadQuote(parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到報價單", 404)
		return
	}
	s.render(w, r, "quote_detail.html", map[string]any{"Title": "報價單", "Crumbs": []string{"報價單", q.QuoteNo}, "Active": "quotes", "Quote": q})
}
func (s *Server) quoteCreate(w http.ResponseWriter, r *http.Request) {
	discount, e1 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("discount_value")), 64)
	tax, e2 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("tax_rate")), 64)
	if strings.TrimSpace(r.FormValue("quote_no")) == "" || e1 != nil || e2 != nil || discount < 0 || tax < 0 {
		http.Error(w, "報價單欄位格式錯誤", 400)
		return
	}
	var id int64
	err := s.DB.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, r.FormValue("quote_no"), r.FormValue("title"), r.FormValue("client_name"), r.FormValue("issuer_name"), defaultString(r.FormValue("currency"), "TWD"), defaultString(r.FormValue("discount_type"), "amount"), discount, tax, r.FormValue("note")).Scan(&id)
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(id, 10), 303)
}
func (s *Server) quoteItemCreate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	qty, e1 := strconv.ParseFloat(defaultString(r.FormValue("quantity"), "1"), 64)
	price, e2 := money.ParseCents(r.FormValue("unit_price"))
	if strings.TrimSpace(r.FormValue("description")) == "" || e1 != nil || e2 != nil || qty <= 0 || price < 0 {
		http.Error(w, "報價項目格式錯誤", 400)
		return
	}
	_, err := s.DB.Exec(`INSERT INTO quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order) SELECT $1,$2,$3,$4,$5,COUNT(*) FROM quote_items WHERE quote_id=$1`, id, r.FormValue("description"), qty, defaultString(r.FormValue("unit"), "式"), price)
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+r.PathValue("id"), 303)
}
func (s *Server) quoteRevise(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	q, err := s.loadQuote(id)
	if err != nil {
		s.error500(w, err)
		return
	}
	var newID int64
	err = s.DB.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,version_no,parent_quote_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, fmt.Sprintf("%s-R%d", strings.Split(q.QuoteNo, "-R")[0], q.VersionNo+1), q.Title, q.ClientName, q.IssuerName, q.Currency, q.DiscountType, q.DiscountValue, q.TaxRate, q.Note, q.VersionNo+1, id).Scan(&newID)
	if err == nil {
		_, err = s.DB.Exec(`INSERT INTO quote_items(quote_id,description,quantity,unit,unit_price_cents,sort_order) SELECT $1,description,quantity,unit,unit_price_cents,sort_order FROM quote_items WHERE quote_id=$2`, newID, id)
	}
	if err == nil {
		_, err = s.DB.Exec(`UPDATE quotes SET status='sent' WHERE id=$1 AND status='draft'`, id)
	}
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(newID, 10), 303)
}
func (s *Server) quoteAccept(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	q, err := s.loadQuote(id)
	if err != nil || q.ProjectID > 0 || q.Status == "accepted" {
		http.Error(w, "此報價無法建立專案", 409)
		return
	}
	name := strings.TrimSpace(r.FormValue("project_name"))
	if name == "" {
		name = q.Title
	}
	if name == "" {
		name = q.QuoteNo
	}
	var projectID int64
	tx, err := s.DB.Begin()
	if err == nil {
		err = tx.QueryRow(`INSERT INTO projects(name,note) VALUES($1,$2) RETURNING id`, name, "由報價 "+q.QuoteNo+" 客戶同意後建立").Scan(&projectID)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO project_budgets(project_id,total_amount_cents,note) VALUES($1,$2,$3)`, projectID, q.TotalCents, "由報價單自動建立")
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE quotes SET status='accepted',project_id=$1 WHERE id=$2`, projectID, id)
	}
	if err != nil {
		if tx != nil {
			_ = tx.Rollback()
		}
		http.Error(w, "建立專案失敗："+err.Error(), 409)
		return
	}
	if err = tx.Commit(); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/management", 303)
}
