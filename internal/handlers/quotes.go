package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

type quoteItemView struct {
	ID, UnitPriceCents, LineTotalCents int64
	Description, Detail, Unit          string
	Quantity                           float64
	IsChoice                           bool
	ChoiceLabel                        string
}
type quoteTotalView struct {
	Label, ChoiceDescription                           string
	SubtotalCents, DiscountCents, TaxCents, TotalCents int64
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
	AcceptedChoiceLabel                                                          string
	ShowUnitPrice, HasChoices                                                    bool
	ContactUserID                                                                int64
	Specifications                                                               []quoteSpecificationView
	TotalOptions                                                                 []quoteTotalView
}

func quoteItemDisplayNumber(index int) int {
	return index + 1
}

func quoteChoiceLabel(index int) string {
	if index >= 0 && index < 26 {
		return string(rune('A' + index))
	}
	return strconv.Itoa(index + 1)
}

func calculateQuoteTotal(subtotal int64, discountType string, discountValue, taxRate float64) quoteTotalView {
	result := quoteTotalView{SubtotalCents: subtotal}
	if discountType == "percent" {
		result.DiscountCents = int64(float64(subtotal) * discountValue / 100)
	} else {
		result.DiscountCents = int64(discountValue * 100)
	}
	taxable := subtotal - result.DiscountCents
	result.TaxCents = int64(float64(taxable) * taxRate / 100)
	result.TotalCents = taxable + result.TaxCents
	return result
}

func (s *Server) loadQuote(id int64) (quoteView, error) {
	var q quoteView
	var showUnitPrice int
	err := s.DB.QueryRow(`SELECT id,quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,status,version_no,COALESCE(parent_quote_id,0),COALESCE(project_id,0),quote_date,COALESCE(valid_until,''),issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,COALESCE(accepted_choice_label,''),COALESCE(contact_user_id,0) FROM quotes WHERE id=$1`, id).
		Scan(&q.ID, &q.QuoteNo, &q.Title, &q.ClientName, &q.IssuerName, &q.Currency, &q.DiscountType, &q.DiscountValue, &q.TaxRate, &q.Note, &q.Status, &q.VersionNo, &q.ParentQuoteID, &q.ProjectID, &q.QuoteDate, &q.ValidUntil, &q.IssuerContact, &q.IssuerEmail, &q.IssuerTaxID, &q.ProjectContent, &q.Terms, &q.SignatureLabel, &q.QuoteLanguage, &q.QuoteType, &showUnitPrice, &q.PersonalName, &q.PersonalContact, &q.AcceptedChoiceLabel, &q.ContactUserID)
	if err != nil {
		return q, err
	}
	q.ShowUnitPrice = showUnitPrice == 1
	rows, err := s.DB.Query(`SELECT id,description,detail,quantity,unit,unit_price_cents,is_choice FROM quote_items WHERE quote_id=$1 ORDER BY sort_order,id`, id)
	if err != nil {
		return q, err
	}
	defer rows.Close()
	var baseSubtotal int64
	var choiceItems []quoteItemView
	for rows.Next() {
		var x quoteItemView
		var isChoice int
		if err := rows.Scan(&x.ID, &x.Description, &x.Detail, &x.Quantity, &x.Unit, &x.UnitPriceCents, &isChoice); err != nil {
			return q, err
		}
		x.IsChoice = isChoice == 1
		x.LineTotalCents = int64(x.Quantity * float64(x.UnitPriceCents))
		if x.IsChoice {
			x.ChoiceLabel = quoteChoiceLabel(len(choiceItems))
			choiceItems = append(choiceItems, x)
		} else {
			baseSubtotal += x.LineTotalCents
		}
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
	q.HasChoices = len(choiceItems) >= 2
	if q.HasChoices {
		for _, item := range choiceItems {
			total := calculateQuoteTotal(baseSubtotal+item.LineTotalCents, q.DiscountType, q.DiscountValue, q.TaxRate)
			total.Label = item.ChoiceLabel
			total.ChoiceDescription = item.Description
			q.TotalOptions = append(q.TotalOptions, total)
		}
	} else {
		for _, item := range choiceItems {
			baseSubtotal += item.LineTotalCents
		}
		q.TotalOptions = append(q.TotalOptions, calculateQuoteTotal(baseSubtotal, q.DiscountType, q.DiscountValue, q.TaxRate))
	}
	primary := q.TotalOptions[0]
	q.SubtotalCents, q.DiscountCents, q.TaxCents, q.TotalCents = primary.SubtotalCents, primary.DiscountCents, primary.TaxCents, primary.TotalCents
	return q, nil
}

func (s *Server) quotesList(w http.ResponseWriter, r *http.Request) {
	query, args := `SELECT id FROM quotes`, []any{}
	if u := auth.FromContext(r.Context()); u != nil && u.Role != auth.RoleOwner {
		query += ` WHERE created_by_id=$1`
		args = append(args, u.ID)
	}
	rows, err := s.DB.Query(query+` ORDER BY id DESC`, args...)
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
	users, err := auth.ListUsers(s.DB)
	if err != nil {
		s.error500(w, err)
		return
	}
	var contacts []auth.User
	for _, u := range users {
		if u.Active {
			contacts = append(contacts, u)
		}
	}
	s.render(w, r, "quote_detail.html", map[string]any{"Title": "報價單", "Crumbs": []string{"報價單", q.QuoteNo}, "Active": "quotes", "Quote": q, "Attachments": attachments, "CompanyQuote": companyQuoteDefaults(s), "Contacts": contacts, "Saved": r.URL.Query().Get("saved") == "1"})
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
	u := auth.FromContext(r.Context())
	if u == nil {
		http.Error(w, "未登入", http.StatusUnauthorized)
		return
	}
	err := s.DB.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,created_by_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) RETURNING id`,
		r.FormValue("quote_no"), r.FormValue("title"), r.FormValue("client_name"), issuerName,
		defaultString(r.FormValue("currency"), "TWD"), defaultString(r.FormValue("discount_type"), "percent"),
		discount, tax, r.FormValue("note"), defaultString(r.FormValue("quote_date"), time.Now().Format("2006-01-02")),
		r.FormValue("valid_until"), issuerContact, issuerEmail, issuerTaxID, r.FormValue("project_content"),
		r.FormValue("terms"), defaultString(r.FormValue("signature_label"), "簽核"),
		defaultString(r.FormValue("quote_language"), "zh-TW"), quoteType, checkboxInt(r.FormValue("show_unit_price")),
		r.FormValue("personal_name"), r.FormValue("personal_contact"), u.ID).Scan(&id)
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
	_, err := s.DB.Exec(`INSERT INTO quote_items(quote_id,description,detail,quantity,unit,unit_price_cents,is_choice,sort_order) SELECT $1,$2,$3,$4,$5,$6,$7,COUNT(*) FROM quote_items WHERE quote_id=$1`, id, r.FormValue("description"), r.FormValue("detail"), qty, defaultString(r.FormValue("unit"), "式"), price, checkboxInt(r.FormValue("is_choice")))
	if err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+r.PathValue("id"), 303)
}

func (s *Server) quoteItemUpdate(w http.ResponseWriter, r *http.Request) {
	quoteID, itemID := parseInt64(r.PathValue("id")), parseInt64(r.PathValue("itemID"))
	qty, e1 := strconv.ParseFloat(defaultString(r.FormValue("quantity"), "1"), 64)
	price, e2 := money.ParseCents(r.FormValue("unit_price"))
	if strings.TrimSpace(r.FormValue("description")) == "" || e1 != nil || e2 != nil || qty <= 0 || price < 0 {
		http.Error(w, "報價項目格式錯誤", http.StatusBadRequest)
		return
	}
	result, err := s.DB.Exec(`UPDATE quote_items SET description=$1,detail=$2,quantity=$3,unit=$4,unit_price_cents=$5,is_choice=$6 WHERE id=$7 AND quote_id=$8 AND EXISTS (SELECT 1 FROM quotes WHERE id=$8 AND status='draft')`, strings.TrimSpace(r.FormValue("description")), r.FormValue("detail"), qty, defaultString(r.FormValue("unit"), "式"), price, checkboxInt(r.FormValue("is_choice")), itemID, quoteID)
	if err != nil {
		s.error500(w, err)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "報價項目不存在或報價單已鎖定", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(quoteID, 10), http.StatusSeeOther)
}

func (s *Server) quoteItemDelete(w http.ResponseWriter, r *http.Request) {
	quoteID, itemID := parseInt64(r.PathValue("id")), parseInt64(r.PathValue("itemID"))
	result, err := s.DB.Exec(`DELETE FROM quote_items WHERE id=$1 AND quote_id=$2 AND EXISTS (SELECT 1 FROM quotes WHERE id=$2 AND status='draft')`, itemID, quoteID)
	if err != nil {
		s.error500(w, err)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "報價項目不存在或報價單已鎖定", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(quoteID, 10), http.StatusSeeOther)
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
	contactUserID := parseInt64(r.FormValue("contact_user_id"))
	personalName := r.FormValue("personal_name")
	if contactUserID > 0 {
		var fullName, username string
		if err := s.DB.QueryRow(`SELECT full_name, username FROM users WHERE id=$1 AND active=1`, contactUserID).Scan(&fullName, &username); err != nil {
			http.Error(w, "所選聯絡人不存在或已停用", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(fullName) == "" {
			fullName = username
		}
		if quoteType == "personal" {
			personalName = fullName
		} else {
			issuerContact = fullName
		}
	}
	args := []any{
		r.FormValue("title"), r.FormValue("client_name"), issuerName, defaultString(r.FormValue("currency"), "TWD"),
		defaultString(r.FormValue("discount_type"), "percent"), discount, tax, r.FormValue("note"),
		defaultString(r.FormValue("quote_date"), time.Now().Format("2006-01-02")), r.FormValue("valid_until"),
		issuerContact, issuerEmail, issuerTaxID, r.FormValue("project_content"), r.FormValue("terms"),
		defaultString(r.FormValue("signature_label"), "簽核"), defaultString(r.FormValue("quote_language"), "zh-TW"),
		quoteType, checkboxInt(r.FormValue("show_unit_price")), personalName,
		r.FormValue("personal_contact"), contactUserID,
	}
	targetID := id
	var result sql.Result
	var err error
	if checkboxInt(r.FormValue("save_as_new_version")) == 1 {
		q, err := s.loadQuote(id)
		if err != nil {
			http.Error(w, "找不到報價單", http.StatusNotFound)
			return
		}
		tx, err := s.DB.Begin()
		if err != nil {
			s.error500(w, err)
			return
		}
		defer tx.Rollback()
		var copiedAttachmentKeys []string
		targetID, copiedAttachmentKeys, err = s.cloneQuoteVersion(r.Context(), tx, q)
		if err == nil {
			args = append(args, targetID)
			result, err = tx.Exec(`UPDATE quotes SET title=$1,client_name=$2,issuer_name=$3,currency=$4,discount_type=$5,discount_value=$6,tax_rate=$7,note=$8,quote_date=$9,valid_until=NULLIF($10,''),issuer_contact=$11,issuer_email=$12,issuer_tax_id=$13,project_content=$14,terms=$15,signature_label=$16,quote_language=$17,quote_type=$18,show_unit_price=$19,personal_name=$20,personal_contact=$21,contact_user_id=NULLIF($22,0),updated_at=CAST(CURRENT_TIMESTAMP AS TEXT) WHERE id=$23 AND status='draft'`, args...)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			s.cleanupQuoteAttachmentCopies(r.Context(), copiedAttachmentKeys)
			s.error500(w, err)
			return
		}
	} else {
		args = append(args, targetID)
		result, err = s.DB.Exec(`UPDATE quotes SET title=$1,client_name=$2,issuer_name=$3,currency=$4,discount_type=$5,discount_value=$6,tax_rate=$7,note=$8,quote_date=$9,valid_until=NULLIF($10,''),issuer_contact=$11,issuer_email=$12,issuer_tax_id=$13,project_content=$14,terms=$15,signature_label=$16,quote_language=$17,quote_type=$18,show_unit_price=$19,personal_name=$20,personal_contact=$21,contact_user_id=NULLIF($22,0),updated_at=CAST(CURRENT_TIMESTAMP AS TEXT) WHERE id=$23 AND status='draft'`, args...)
	}
	if err != nil {
		s.error500(w, err)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "報價單已不是可編輯的草稿，請重新整理後再試", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(targetID, 10)+"?saved=1", http.StatusSeeOther)
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
	newID, copiedAttachmentKeys, err := s.cloneQuoteVersion(r.Context(), tx, q)
	if err != nil {
		s.error500(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.cleanupQuoteAttachmentCopies(r.Context(), copiedAttachmentKeys)
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, "/quotes/"+strconv.FormatInt(newID, 10), 303)
}

func (s *Server) cloneQuoteVersion(ctx context.Context, tx *sql.Tx, q quoteView) (newID int64, copiedAttachmentKeys []string, err error) {
	defer func() {
		if err != nil {
			s.cleanupQuoteAttachmentCopies(ctx, copiedAttachmentKeys)
			copiedAttachmentKeys = nil
		}
	}()
	if q.Status != "draft" {
		err = fmt.Errorf("只有草稿報價單可以建立新版")
		return
	}
	quoteNo := fmt.Sprintf("%s-R%d", strings.Split(q.QuoteNo, "-R")[0], q.VersionNo+1)
	err = tx.QueryRow(`INSERT INTO quotes(quote_no,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,version_no,parent_quote_id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,contact_user_id,created_by_id) SELECT $1,title,client_name,issuer_name,currency,discount_type,discount_value,tax_rate,note,$2,id,quote_date,valid_until,issuer_contact,issuer_email,issuer_tax_id,project_content,terms,signature_label,quote_language,quote_type,show_unit_price,personal_name,personal_contact,contact_user_id,created_by_id FROM quotes WHERE id=$3 RETURNING id`, quoteNo, q.VersionNo+1, q.ID).Scan(&newID)
	if err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO quote_items(quote_id,description,detail,quantity,unit,unit_price_cents,is_choice,sort_order) SELECT $1,description,detail,quantity,unit,unit_price_cents,is_choice,sort_order FROM quote_items WHERE quote_id=$2`, newID, q.ID); err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO quote_specifications(quote_id,feature,use_case,capability,implementation_steps,sort_order) SELECT $1,feature,use_case,capability,implementation_steps,sort_order FROM quote_specifications WHERE quote_id=$2`, newID, q.ID); err != nil {
		return
	}

	rows, queryErr := tx.Query(`SELECT original_filename,content_type,size_bytes,uploaded_by_id,storage_key FROM quote_attachments WHERE quote_id=$1 ORDER BY id`, q.ID)
	if queryErr != nil {
		err = queryErr
		return
	}
	var attachments []models.QuoteAttachment
	for rows.Next() {
		var attachment models.QuoteAttachment
		if scanErr := rows.Scan(&attachment.OriginalFilename, &attachment.ContentType, &attachment.SizeBytes, &attachment.UploadedByID, &attachment.StorageKey); scanErr != nil {
			_ = rows.Close()
			err = scanErr
			return
		}
		attachments = append(attachments, attachment)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return
	}
	if len(attachments) > 0 && s.Attachments == nil {
		err = fmt.Errorf("附件儲存空間尚未設定")
		return
	}
	for _, attachment := range attachments {
		var source io.ReadCloser
		source, err = s.Attachments.Open(ctx, attachment.StorageKey)
		if err != nil {
			err = fmt.Errorf("讀取舊版報價附件 %s: %w", attachment.OriginalFilename, err)
			return
		}
		newKey, keyErr := quoteAttachmentKey(newID, attachment.OriginalFilename)
		if keyErr != nil {
			_ = source.Close()
			err = keyErr
			return
		}
		contentType := defaultString(attachment.ContentType, "application/pdf")
		if putErr := s.Attachments.Put(ctx, newKey, contentType, source); putErr != nil {
			_ = source.Close()
			err = fmt.Errorf("複製報價附件 %s: %w", attachment.OriginalFilename, putErr)
			return
		}
		_ = source.Close()
		copiedAttachmentKeys = append(copiedAttachmentKeys, newKey)
		_, err = tx.Exec(`INSERT INTO quote_attachments(quote_id,storage_key,original_filename,content_type,size_bytes,uploaded_by_id) VALUES($1,$2,$3,$4,$5,$6)`, newID, newKey, attachment.OriginalFilename, contentType, attachment.SizeBytes, attachment.UploadedByID)
		if err != nil {
			return
		}
	}
	result, err := tx.Exec(`UPDATE quotes SET status='sent' WHERE id=$1 AND status='draft'`, q.ID)
	if err != nil {
		return 0, copiedAttachmentKeys, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return 0, copiedAttachmentKeys, fmt.Errorf("報價單已不是可建立新版的草稿")
	}
	return newID, copiedAttachmentKeys, nil
}

func (s *Server) cleanupQuoteAttachmentCopies(ctx context.Context, keys []string) {
	if s.Attachments == nil {
		return
	}
	for _, key := range keys {
		_ = s.Attachments.Delete(ctx, key)
	}
}

func (s *Server) quoteDelete(w http.ResponseWriter, r *http.Request) {
	id := parseInt64(r.PathValue("id"))
	q, err := s.loadQuote(id)
	if err != nil {
		http.Error(w, "找不到報價單", http.StatusNotFound)
		return
	}
	if q.Status != "draft" {
		http.Error(w, "只有草稿報價單可以刪除", http.StatusConflict)
		return
	}
	attachments, err := models.ListQuoteAttachments(s.DB, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	for _, attachment := range attachments {
		if err := s.Attachments.Delete(r.Context(), attachment.StorageKey); err != nil {
			s.error500(w, fmt.Errorf("刪除報價附件: %w", err))
			return
		}
	}
	result, err := s.DB.Exec(`DELETE FROM quotes WHERE id=$1 AND status='draft'`, id)
	if err != nil {
		s.error500(w, err)
		return
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		http.Error(w, "報價單已不是可刪除的草稿，請重新整理後再試", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/quotes", http.StatusSeeOther)
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
	acceptedChoice := ""
	acceptedTotal := q.TotalCents
	if q.HasChoices {
		acceptedChoice = strings.ToUpper(strings.TrimSpace(r.FormValue("choice_label")))
		found := false
		for _, option := range q.TotalOptions {
			if option.Label == acceptedChoice {
				acceptedTotal = option.TotalCents
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "請選擇客戶同意的報價方案", http.StatusBadRequest)
			return
		}
	}
	var projectID int64
	tx, err := s.DB.Begin()
	if err == nil {
		err = tx.QueryRow(`INSERT INTO projects(name,note) VALUES($1,$2) RETURNING id`, name, "由報價 "+q.QuoteNo+" 客戶同意後建立").Scan(&projectID)
	}
	if err == nil {
		note := "由報價單自動建立"
		if acceptedChoice != "" {
			note += "（方案 " + acceptedChoice + "）"
		}
		_, err = tx.Exec(`INSERT INTO project_budgets(project_id,total_amount_cents,note) VALUES($1,$2,$3)`, projectID, acceptedTotal, note)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE quotes SET status='accepted',project_id=$1,accepted_choice_label=$2 WHERE id=$3`, projectID, acceptedChoice, id)
	}
	if err == nil {
		u := auth.FromContext(r.Context())
		if u != nil && u.Role != auth.RoleOwner {
			_, err = tx.Exec(`INSERT INTO project_permissions(project_id,user_id,access_level) VALUES($1,$2,'write')`, projectID, u.ID)
		}
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
