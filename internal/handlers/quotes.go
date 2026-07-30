package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

type quoteItemView struct {
	ID, UnitPriceCents, LineTotalCents int64
	Description, Detail, Unit          string
	Quantity                           float64
}
type quoteSpecificationView struct {
	ID                                                int64
	Feature, UseCase, Capability, ImplementationSteps string
}
type quoteView struct {
	ID, VersionNo, ParentQuoteID                                                 int64
	QuoteNo, Title, ClientName, IssuerName, Currency, DiscountType, Note, Status string
	DiscountValue, TaxRate                                                       float64
	SubtotalCents, DiscountCents, TaxCents, TotalCents                           int64
	ProjectID                                                                    int64
	Items                                                                        []quoteItemView
	QuoteDate, ValidUntil, IssuerContact, IssuerEmail, IssuerTaxID               string
	ProjectContent, Terms, SignatureLabel                                        string
	QuoteLanguage, QuoteType, PersonalName, PersonalContact                      string
	ShowUnitPrice                                                                bool
	Specifications                                                               []quoteSpecificationView
}

func quoteItemDisplayNumber(index int) int {
	return index + 1
}

func (s *Server) loadQuote(id int64) (quoteView, error) {
	var q quoteView
	var showUnitPrice int
	err := s.DB.QueryRow(`SELECT id,quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,status,version_no,COALESCE(parent_quote_id,0),COALESCE(project_id,0),quote_date,COALESCE(valid_until,''),issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact FROM quotes WHERE id=$1`, id).
		Scan(&q.ID, &q.QuoteNo, &q.Title, &q.ClientName, &q.IssuerName, &q.Currency, &q.DiscountType, &q.DiscountValue, &q.TaxRate, &q.Note, &q.Status, &q.VersionNo, &q.ParentQuoteID, &q.ProjectID, &q.QuoteDate, &q.ValidUntil, &q.IssuerContact, &q.IssuerEmail, &q.IssuerTaxID, &q.ProjectContent, &q.Terms, &q.SignatureLabel, &q.QuoteLanguage, &q.QuoteType, &showUnitPrice, &q.PersonalName, &q.PersonalContact)
	if err != nil {
		return q, err
	}
	q.ShowUnitPrice = showUnitPrice == 1
	rows, err := s.DB.Query(`SELECT id,description,detail,quantity,unit,unit_price_cents FROM quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, id)
	if err != nil {
		return q, err
	}
	defer rows.Close()
	for rows.Next() {
		var x quoteItemView
		if err := rows.Scan(&x.ID, &x.Description, &x.Detail, &x.Quantity, &x.Unit, &x.UnitPriceCents); err != nil {
			return q, err
		}
		x.LineTotalCents = int64(x.Quantity * float64(x.UnitPriceCents))
		q.SubtotalCents += x.LineTotalCents
		q.Items = append(q.Items, x)
	}
	specRows, err := s.DB.Query(`SELECT id,feature,use_case,capability,implementation_steps FROM quote_specifications WHERE quote_id=$1 ORDER BY sort_order,id`, id)
	if err != nil {
		return q, err
	}
	defer specRows.Close()
	for specRows.Next() {
		var x quoteSpecificationView
		if err := specRows.Scan(&x.ID, &x.Feature, &x.UseCase, &x.Capability, &x.ImplementationSteps); err != nil {
			return q, err
		}
		q.Specifications = append(q.Specifications, x)
	}
	if err := specRows.Err(); err != nil {
		return q, err
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
	company := companyQuoteDefaults(s)
	s.render(w, r, "quotes.html", map[string]any{"Title": "報價單", "Crumbs": []string{"報價單"}, "Active": "quotes", "Quotes": quotes, "NextQuoteNo": nextStandaloneQuoteNo(s), "CompanyName": company.Name, "CompanyQuote": company})
}

type quoteCompanyView struct {
	Name, Contact, Email, TaxID string
}

func companyQuoteDefaults(s *Server) quoteCompanyView {
	get := func(key, fallback string) string {
		value, _ := models.GetSetting(s.DB, key)
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback
		}
		return value
	}
	return quoteCompanyView{
		Name:    get("company_name", "睿藝有限公司 ReViz"),
		Contact: get("company_contact", "簡信昌"),
		Email:   get("company_email", "hcchien@gmail.com"),
		TaxID:   get("company_tax_id", "62228678"),
	}
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
	attachments, err := models.ListQuoteAttachments(s.DB, q.ID)
	if err != nil {
		s.error500(w, err)
		return
	}
	s.render(w, r, "quote_detail.html", map[string]any{"Title": "報價單", "Crumbs": []string{"報價單", q.QuoteNo}, "Active": "quotes", "Quote": q, "Attachments": attachments, "CompanyQuote": companyQuoteDefaults(s), "Saved": r.URL.Query().Get("saved") == "1"})
}
func (s *Server) quotePrint(w http.ResponseWriter, r *http.Request) {
	q, err := s.loadQuote(parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到報價單", http.StatusNotFound)
		return
	}
	attachments, err := models.ListQuoteAttachments(s.DB, q.ID)
	if err != nil {
		s.error500(w, err)
		return
	}
	s.renderStandalone(w, "quote_print.html", map[string]any{"Title": "報價單 " + q.QuoteNo, "Quote": q, "Attachments": attachments})
}
func (s *Server) quoteCreate(w http.ResponseWriter, r *http.Request) {
	discount, e1 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("discount_value")), 64)
	tax, e2 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("tax_rate")), 64)
	if strings.TrimSpace(r.FormValue("quote_no")) == "" || e1 != nil || e2 != nil || discount < 0 || tax < 0 {
		http.Error(w, "報價單欄位格式錯誤", 400)
		return
	}
	quoteType := defaultString(r.FormValue("quote_type"), "company")
	issuerName := strings.TrimSpace(r.FormValue("issuer_name"))
	issuerContact := strings.TrimSpace(r.FormValue("issuer_contact"))
	issuerEmail := strings.TrimSpace(r.FormValue("issuer_email"))
	issuerTaxID := strings.TrimSpace(r.FormValue("issuer_tax_id"))
	if quoteType == "company" {
		company := companyQuoteDefaults(s)
		issuerName = defaultString(issuerName, company.Name)
		issuerContact = defaultString(issuerContact, company.Contact)
		issuerEmail = defaultString(issuerEmail, company.Email)
		issuerTaxID = defaultString(issuerTaxID, company.TaxID)
	}
	var id int64
	err := s.DB.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22) RETURNING id`,
		r.FormValue("quote_no"), r.FormValue("title"), r.FormValue("client_name"), issuerName,
		defaultString(r.FormValue("currency"), "TWD"), defaultString(r.FormValue("discount_type"), "percent"),
		discount, tax, r.FormValue("note"), defaultString(r.FormValue("quote_date"), time.Now().Format("2006-01-02")),
		r.FormValue("valid_until"), issuerContact, issuerEmail, issuerTaxID, r.FormValue("project_content"),
		r.FormValue("terms"), defaultString(r.FormValue("signature_label"), "簽核"),
		defaultString(r.FormValue("quote_language"), "zh-TW"), quoteType, checkboxInt(r.FormValue("show_unit_price")),
		r.FormValue("personal_name"), r.FormValue("personal_contact")).Scan(&id)
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
	_, err := s.DB.Exec(`INSERT INTO quote_items(quote_id,description,detail,quantity,unit,unit_price_cents,sort_order) SELECT $1,$2,$3,$4,$5,$6,COUNT(*) FROM quote_items WHERE quote_id=$1`, id, r.FormValue("description"), r.FormValue("detail"), qty, defaultString(r.FormValue("unit"), "式"), price)
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+r.PathValue("id"), 303)
}
func (s *Server) quoteUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	discount, e1 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("discount_value")), 64)
	tax, e2 := strconv.ParseFloat(zeroIfEmpty(r.FormValue("tax_rate")), 64)
	if e1 != nil || e2 != nil || discount < 0 || tax < 0 {
		http.Error(w, "報價單欄位格式錯誤", http.StatusBadRequest)
		return
	}
	quoteType := defaultString(r.FormValue("quote_type"), "company")
	issuerName := strings.TrimSpace(r.FormValue("issuer_name"))
	issuerContact := strings.TrimSpace(r.FormValue("issuer_contact"))
	issuerEmail := strings.TrimSpace(r.FormValue("issuer_email"))
	issuerTaxID := strings.TrimSpace(r.FormValue("issuer_tax_id"))
	if quoteType == "company" {
		company := companyQuoteDefaults(s)
		issuerName = defaultString(issuerName, company.Name)
		issuerContact = defaultString(issuerContact, company.Contact)
		issuerEmail = defaultString(issuerEmail, company.Email)
		issuerTaxID = defaultString(issuerTaxID, company.TaxID)
	}
	result, err := s.DB.Exec(`UPDATE quotes SET title=$1,client_name=$2,issuer_name=$3,currency=$4,discount_type=$5,discount_value=$6,tax_rate=$7,note=$8,quote_date=$9,valid_until=NULLIF($10,''),issuer_contact=$11,issuer_email=$12,issuer_tax_id=$13,project_content=$14,terms=$15,signature_label=$16,quote_language=$17,quote_type=$18,show_unit_price=$19,personal_name=$20,personal_contact=$21,updated_at=CAST(CURRENT_TIMESTAMP AS TEXT) WHERE id=$22 AND status='draft'`,
		r.FormValue("title"), r.FormValue("client_name"), issuerName, defaultString(r.FormValue("currency"), "TWD"),
		defaultString(r.FormValue("discount_type"), "percent"), discount, tax, r.FormValue("note"),
		defaultString(r.FormValue("quote_date"), time.Now().Format("2006-01-02")), r.FormValue("valid_until"),
		issuerContact, issuerEmail, issuerTaxID, r.FormValue("project_content"), r.FormValue("terms"),
		defaultString(r.FormValue("signature_label"), "簽核"), defaultString(r.FormValue("quote_language"), "zh-TW"),
		quoteType, checkboxInt(r.FormValue("show_unit_price")), r.FormValue("personal_name"),
		r.FormValue("personal_contact"), id)
	if err != nil {
		s.error500(w, err)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "報價單已不是可編輯的草稿，請重新整理後再試", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(id, 10)+"?saved=1", http.StatusSeeOther)
}

func checkboxInt(value string) int {
	if value == "1" || value == "true" || value == "on" {
		return 1
	}
	return 0
}
func (s *Server) quoteSpecificationCreate(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	if strings.TrimSpace(r.FormValue("feature")) == "" {
		http.Error(w, "規格功能必填", http.StatusBadRequest)
		return
	}
	_, err := s.DB.Exec(`INSERT INTO quote_specifications(quote_id,feature,use_case,capability,implementation_steps,sort_order) SELECT $1,$2,$3,$4,$5,COUNT(*) FROM quote_specifications WHERE quote_id=$1`, id, r.FormValue("feature"), r.FormValue("use_case"), r.FormValue("capability"), r.FormValue("implementation_steps"))
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
func (s *Server) quoteRevise(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	q, err := s.loadQuote(id)
	if err != nil {
		s.error500(w, err)
		return
	}
	tx, err := s.DB.Begin()
	if err != nil {
		s.error500(w, err)
		return
	}
	defer tx.Rollback()
	var newID int64
	err = tx.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,version_no,parent_quote_id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact) SELECT $1,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,$2,id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact FROM quotes WHERE id=$3 RETURNING id`, fmt.Sprintf("%s-R%d", strings.Split(q.QuoteNo, "-R")[0], q.VersionNo+1), q.VersionNo+1, id).Scan(&newID)
	if err == nil {
		_, err = tx.Exec(`INSERT INTO quote_items(quote_id,description,detail,quantity,unit,unit_price_cents,sort_order) SELECT $1,description,detail,quantity,unit,unit_price_cents,sort_order FROM quote_items WHERE quote_id=$2`, newID, id)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO quote_specifications(quote_id,feature,use_case,capability,implementation_steps,sort_order) SELECT $1,feature,use_case,capability,implementation_steps,sort_order FROM quote_specifications WHERE quote_id=$2`, newID, id)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE quotes SET status='sent' WHERE id=$1 AND status='draft'`, id)
	}
	if err != nil {
		s.error500(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
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
