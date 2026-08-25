package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

type testAttachmentStore struct {
	objects map[string][]byte
}

func (s *testAttachmentStore) Put(_ context.Context, key, _ string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = data
	return nil
}

func (s *testAttachmentStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}

func (s *testAttachmentStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *testAttachmentStore) Close() error { return nil }

func TestQuoteUpdateSavesCompanyDefaultsAndIntegerCheckbox(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	_, err = d.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO settings(key,value) VALUES
			('company_name','睿藝有限公司 ReViz'),
			('company_contact','簡信昌'),
			('company_email','hcchien@gmail.com'),
			('company_tax_id','62228678');
		CREATE TABLE quotes (
			id INTEGER PRIMARY KEY,
			title TEXT, client_name TEXT, issuer_name TEXT, currency TEXT,
			discount_type TEXT, discount_value REAL, tax_rate REAL, note TEXT,
			quote_date TEXT, valid_until TEXT, issuer_contact TEXT, issuer_email TEXT,
			issuer_tax_id TEXT, project_content TEXT, terms TEXT, signature_label TEXT,
			quote_language TEXT, quote_type TEXT, show_unit_price INTEGER,
			personal_name TEXT, personal_contact TEXT, contact_user_id INTEGER, updated_at TEXT, status TEXT
		);
		INSERT INTO quotes(id,status) VALUES(7,'draft');
	`)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"title":            {"口述歷史記憶資料庫"},
		"client_name":      {"國際特赦組織"},
		"quote_type":       {"company"},
		"currency":         {"TWD"},
		"discount_type":    {"percent"},
		"discount_value":   {"0"},
		"tax_rate":         {"5"},
		"quote_date":       {"2026-05-16"},
		"valid_until":      {"2026-05-30"},
		"quote_language":   {"zh-TW"},
		"show_unit_price":  {"1"},
		"project_content":  {"專案內容"},
		"terms":            {"下方報價為含稅價（5%）。"},
		"signature_label":  {"簽核"},
		"issuer_name":      {""},
		"issuer_contact":   {""},
		"issuer_email":     {""},
		"issuer_tax_id":    {""},
		"personal_name":    {""},
		"personal_contact": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/quotes/7", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()

	(&Server{DB: d}).quoteUpdate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("quoteUpdate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/quotes/7?saved=1" {
		t.Fatalf("redirect = %q", got)
	}

	var name, contact, email, taxID string
	var showUnitPrice int
	err = d.QueryRow(`SELECT issuer_name,issuer_contact,issuer_email,issuer_tax_id,show_unit_price FROM quotes WHERE id=7`).
		Scan(&name, &contact, &email, &taxID, &showUnitPrice)
	if err != nil {
		t.Fatal(err)
	}
	if name != "睿藝有限公司 ReViz" || contact != "簡信昌" || email != "hcchien@gmail.com" || taxID != "62228678" {
		t.Fatalf("company defaults = %q, %q, %q, %q", name, contact, email, taxID)
	}
	if showUnitPrice != 1 {
		t.Fatalf("show_unit_price = %d; want 1", showUnitPrice)
	}
}

func TestCheckboxInt(t *testing.T) {
	for input, want := range map[string]int{"": 0, "0": 0, "1": 1, "on": 1, "true": 1} {
		if got := checkboxInt(input); got != want {
			t.Fatalf("checkboxInt(%q) = %d; want %d", input, got, want)
		}
	}
}

func TestQuoteAttachmentUploadAcceptsMultiplePDFs(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = d.Exec(`
		CREATE TABLE quotes (id INTEGER PRIMARY KEY, status TEXT NOT NULL);
		CREATE TABLE quote_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			quote_id INTEGER NOT NULL,
			storage_key TEXT NOT NULL UNIQUE,
			original_filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			uploaded_by_id INTEGER,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO quotes(id,status) VALUES(7,'draft');
	`)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, data := range map[string][]byte{
		"附件一.pdf": []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF"),
		"附件二.pdf": []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF"),
	} {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	store := &testAttachmentStore{objects: map[string][]byte{}}
	req := httptest.NewRequest(http.MethodPost, "/quotes/7/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	(&Server{DB: d, Attachments: store}).quoteAttachmentUpload(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/quotes/7" {
		t.Fatalf("redirect = %q", got)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM quote_attachments WHERE quote_id=7`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(store.objects) != 2 {
		t.Fatalf("saved attachments = %d rows, %d objects; want 2 and 2", count, len(store.objects))
	}
}

func TestQuoteUpdateSaveAsNewVersionCopiesAttachments(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	_, err = d.Exec(`
		CREATE TABLE quotes (
			id INTEGER PRIMARY KEY, quote_no TEXT NOT NULL, title TEXT NOT NULL,
			client_name TEXT NOT NULL, issuer_name TEXT NOT NULL, currency TEXT NOT NULL,
			discount_type TEXT NOT NULL, discount_value REAL NOT NULL, tax_rate REAL NOT NULL,
			note TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'draft', version_no INTEGER NOT NULL,
			parent_quote_id INTEGER, project_id INTEGER, quote_date TEXT NOT NULL,
			valid_until TEXT, issuer_contact TEXT NOT NULL, issuer_email TEXT NOT NULL,
			issuer_tax_id TEXT NOT NULL, project_content TEXT NOT NULL, terms TEXT NOT NULL,
			signature_label TEXT NOT NULL, quote_language TEXT NOT NULL, quote_type TEXT NOT NULL,
			show_unit_price INTEGER NOT NULL, personal_name TEXT NOT NULL,
			personal_contact TEXT NOT NULL, accepted_choice_label TEXT,
			contact_user_id INTEGER, created_by_id INTEGER, updated_at TEXT
		);
		CREATE TABLE quote_items (
			id INTEGER PRIMARY KEY, quote_id INTEGER NOT NULL, description TEXT NOT NULL,
			detail TEXT NOT NULL, quantity REAL NOT NULL, unit TEXT NOT NULL,
			unit_price_cents INTEGER NOT NULL, is_choice INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL
		);
		CREATE TABLE quote_specifications (
			id INTEGER PRIMARY KEY, quote_id INTEGER NOT NULL, feature TEXT NOT NULL,
			use_case TEXT NOT NULL, capability TEXT NOT NULL,
			implementation_steps TEXT NOT NULL, sort_order INTEGER NOT NULL
		);
		CREATE TABLE quote_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, quote_id INTEGER NOT NULL,
			storage_key TEXT NOT NULL UNIQUE, original_filename TEXT NOT NULL,
			content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL,
			uploaded_by_id INTEGER, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO quotes VALUES
			(6,'Q-2026-001-R1','第一版','客戶','ReViz','TWD','percent',0,5,'','sent',1,NULL,NULL,'2026-08-01',NULL,'','','','','','簽核','zh-TW','personal',0,'承辦人','聯絡方式',NULL,NULL,1,NULL),
			(7,'Q-2026-001-R2','第二版','客戶','ReViz','TWD','percent',0,5,'','draft',2,6,NULL,'2026-08-10',NULL,'','','','','','簽核','zh-TW','personal',0,'承辦人','聯絡方式',NULL,NULL,1,NULL);
		INSERT INTO quote_items VALUES (1,7,'顧問服務','服務內容',1,'式',10000,0,0);
		INSERT INTO quote_specifications VALUES (1,7,'附件需求','','','',0);
		INSERT INTO quote_attachments(quote_id,storage_key,original_filename,content_type,size_bytes)
		VALUES(7,'quote-attachments/7/source.pdf','需求附件.pdf','application/pdf',15);
	`)
	if err != nil {
		t.Fatal(err)
	}

	attachmentData := []byte("%PDF-1.7 copied")
	store := &testAttachmentStore{objects: map[string][]byte{
		"quote-attachments/7/source.pdf": attachmentData,
	}}
	form := url.Values{
		"title":               {"第三版"},
		"client_name":         {"客戶"},
		"quote_type":          {"personal"},
		"currency":            {"TWD"},
		"discount_type":       {"percent"},
		"discount_value":      {"0"},
		"tax_rate":            {"5"},
		"quote_date":          {"2026-08-25"},
		"quote_language":      {"zh-TW"},
		"personal_name":       {"承辦人"},
		"personal_contact":    {"聯絡方式"},
		"save_as_new_version": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/quotes/7", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	(&Server{DB: d, Attachments: store}).quoteUpdate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("quoteUpdate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/quotes/8?saved=1" {
		t.Fatalf("redirect = %q; want new quote", got)
	}
	var oldStatus, newNo, newTitle string
	var version, parentID int64
	if err := d.QueryRow(`SELECT status FROM quotes WHERE id=7`).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT quote_no,title,version_no,parent_quote_id FROM quotes WHERE id=8`).Scan(&newNo, &newTitle, &version, &parentID); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "sent" || newNo != "Q-2026-001-R3" || newTitle != "第三版" || version != 3 || parentID != 7 {
		t.Fatalf("version result = old status %q, no %q, title %q, version %d, parent %d", oldStatus, newNo, newTitle, version, parentID)
	}
	var newKey, filename string
	if err := d.QueryRow(`SELECT storage_key,original_filename FROM quote_attachments WHERE quote_id=8`).Scan(&newKey, &filename); err != nil {
		t.Fatal(err)
	}
	if newKey == "quote-attachments/7/source.pdf" || filename != "需求附件.pdf" {
		t.Fatalf("copied attachment = key %q, filename %q", newKey, filename)
	}
	if !bytes.Equal(store.objects[newKey], attachmentData) {
		t.Fatalf("new attachment data = %q; want %q", store.objects[newKey], attachmentData)
	}
	if !bytes.Equal(store.objects["quote-attachments/7/source.pdf"], attachmentData) {
		t.Fatal("copying the attachment changed the previous version's file")
	}
}

func TestQuoteItemNumbersRestartForEveryQuote(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = d.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO settings(key,value) VALUES('company_name','ReViz'),('fiscal_year','2026');
		CREATE TABLE quotes (
			id INTEGER PRIMARY KEY, quote_no TEXT, title TEXT, client_name TEXT,
			issuer_name TEXT, currency TEXT, discount_type TEXT, discount_value REAL,
			tax_rate REAL, note TEXT, status TEXT, version_no INTEGER, parent_quote_id INTEGER,
			project_id INTEGER, quote_date TEXT, valid_until TEXT, issuer_contact TEXT,
			issuer_email TEXT, issuer_tax_id TEXT, project_content TEXT, terms TEXT,
			signature_label TEXT, quote_language TEXT, quote_type TEXT, show_unit_price INTEGER,
			personal_name TEXT, personal_contact TEXT, accepted_choice_label TEXT, contact_user_id INTEGER
		);
		CREATE TABLE quote_items (
			id INTEGER PRIMARY KEY, quote_id INTEGER, description TEXT, detail TEXT,
			quantity REAL, unit TEXT, unit_price_cents INTEGER, sort_order INTEGER,
			is_choice INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE quote_specifications (
			id INTEGER PRIMARY KEY, quote_id INTEGER, feature TEXT, use_case TEXT,
			capability TEXT, implementation_steps TEXT, sort_order INTEGER
		);
		CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT);
		CREATE TABLE quote_attachments (
			id INTEGER PRIMARY KEY, quote_id INTEGER, storage_key TEXT, original_filename TEXT,
			content_type TEXT, size_bytes INTEGER, uploaded_by_id INTEGER, created_at TEXT
		);
		INSERT INTO quotes VALUES
			(1,'Q-1','第一張','客戶','ReViz','TWD','percent',0,0,'','draft',1,NULL,NULL,'2026-07-30',NULL,'','','','','','簽核','zh-TW','company',0,'','','',NULL),
			(2,'Q-2','第二張','客戶','ReViz','TWD','percent',0,0,'','draft',1,NULL,NULL,'2026-07-30',NULL,'','','','','','簽核','zh-TW','company',0,'','','',NULL);
		INSERT INTO quote_items(id,quote_id,description,detail,quantity,unit,unit_price_cents,sort_order,is_choice) VALUES
			(101,1,'第一張第一項','',1,'式',100,0,0),
			(102,1,'第一張第二項','',1,'式',100,1,0),
			(205,2,'第二張第一項','',1,'式',100,0,0),
			(220,2,'第二張第二項','',1,'式',100,1,0);
	`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(d, os.DirFS("../.."), &testAttachmentStore{objects: map[string][]byte{}})
	if err != nil {
		t.Fatal(err)
	}

	for _, quoteID := range []string{"1", "2"} {
		req := httptest.NewRequest(http.MethodGet, "/quotes/"+quoteID+"/print", nil)
		req.SetPathValue("id", quoteID)
		rec := httptest.NewRecorder()
		s.quotePrint(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("quote %s status = %d, body = %s", quoteID, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<td>1</td>") || !strings.Contains(body, "<td>2</td>") {
			t.Fatalf("quote %s does not render item numbers 1 and 2", quoteID)
		}
		if strings.Contains(body, "<td>101</td>") || strings.Contains(body, "<td>205</td>") || strings.Contains(body, "<td>220</td>") {
			t.Fatalf("quote %s leaks global item IDs into display numbering", quoteID)
		}
	}
}

func TestQuoteChoiceItemsProduceAlternativeTotals(t *testing.T) {
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, err = d.Exec(`
		CREATE TABLE quotes (
			id INTEGER PRIMARY KEY, quote_no TEXT, title TEXT, client_name TEXT,
			issuer_name TEXT, currency TEXT, discount_type TEXT, discount_value REAL,
			tax_rate REAL, note TEXT, status TEXT, version_no INTEGER, parent_quote_id INTEGER,
			project_id INTEGER, quote_date TEXT, valid_until TEXT, issuer_contact TEXT,
			issuer_email TEXT, issuer_tax_id TEXT, project_content TEXT, terms TEXT,
			signature_label TEXT, quote_language TEXT, quote_type TEXT, show_unit_price INTEGER,
			personal_name TEXT, personal_contact TEXT, accepted_choice_label TEXT, contact_user_id INTEGER
		);
		CREATE TABLE quote_items (
			id INTEGER PRIMARY KEY, quote_id INTEGER, description TEXT, detail TEXT,
			quantity REAL, unit TEXT, unit_price_cents INTEGER, sort_order INTEGER,
			is_choice INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE quote_specifications (
			id INTEGER PRIMARY KEY, quote_id INTEGER, feature TEXT, use_case TEXT,
			capability TEXT, implementation_steps TEXT, sort_order INTEGER
		);
		CREATE TABLE projects (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, note TEXT);
		CREATE TABLE project_budgets (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id INTEGER, total_amount_cents INTEGER, note TEXT);
		INSERT INTO quotes VALUES
			(1,'Q-CHOICE','方案報價','客戶','ReViz','TWD','percent',10,5,'','draft',1,NULL,NULL,'2026-08-14',NULL,'','','','','','簽核','zh-TW','company',0,'','','',NULL);
		INSERT INTO quote_items(id,quote_id,description,detail,quantity,unit,unit_price_cents,sort_order,is_choice) VALUES
			(1,1,'共同項目','',1,'式',10000,0,0),
			(2,1,'基本方案','',1,'式',20000,1,1),
			(3,1,'進階方案','',1,'式',30000,2,1);
	`)
	if err != nil {
		t.Fatal(err)
	}

	q, err := (&Server{DB: d}).loadQuote(1)
	if err != nil {
		t.Fatal(err)
	}
	if !q.HasChoices || len(q.TotalOptions) != 2 {
		t.Fatalf("choice totals = hasChoices %v, options %d; want true and 2", q.HasChoices, len(q.TotalOptions))
	}
	if got := q.TotalOptions[0]; got.Label != "A" || got.SubtotalCents != 30000 || got.DiscountCents != 3000 || got.TaxCents != 1350 || got.TotalCents != 28350 {
		t.Fatalf("option A = %+v", got)
	}
	if got := q.TotalOptions[1]; got.Label != "B" || got.SubtotalCents != 40000 || got.DiscountCents != 4000 || got.TaxCents != 1800 || got.TotalCents != 37800 {
		t.Fatalf("option B = %+v", got)
	}
	if q.Items[1].ChoiceLabel != "A" || q.Items[2].ChoiceLabel != "B" {
		t.Fatalf("choice labels = %q, %q", q.Items[1].ChoiceLabel, q.Items[2].ChoiceLabel)
	}

	form := url.Values{"project_name": {"採用進階方案"}, "choice_label": {"B"}}
	req := httptest.NewRequest(http.MethodPost, "/quotes/1/accept", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	(&Server{DB: d}).quoteAccept(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("quoteAccept status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var budget int64
	var budgetNote, acceptedChoice string
	if err := d.QueryRow(`SELECT total_amount_cents,note FROM project_budgets`).Scan(&budget, &budgetNote); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT accepted_choice_label FROM quotes WHERE id=1`).Scan(&acceptedChoice); err != nil {
		t.Fatal(err)
	}
	if budget != 37800 || acceptedChoice != "B" || !strings.Contains(budgetNote, "方案 B") {
		t.Fatalf("accepted budget = %d, choice = %q, note = %q", budget, acceptedChoice, budgetNote)
	}
}

func TestQuotePrintRendersChoiceBreakdownsAndPaginationGuards(t *testing.T) {
	s, err := NewServer(nil, os.DirFS("../.."), nil)
	if err != nil {
		t.Fatal(err)
	}
	q := quoteView{
		QuoteNo:        "Q-CHOICE",
		Title:          "方案報價",
		ClientName:     "客戶",
		IssuerName:     "ReViz",
		Currency:       "TWD",
		QuoteDate:      "2026-08-14",
		QuoteLanguage:  "zh-TW",
		QuoteType:      "company",
		SignatureLabel: "簽核",
		HasChoices:     true,
		Items: []quoteItemView{
			{Description: "共同項目", Quantity: 1, Unit: "式", UnitPriceCents: 10000, LineTotalCents: 10000},
			{Description: "方案 A", Quantity: 1, Unit: "式", UnitPriceCents: 20000, LineTotalCents: 20000, IsChoice: true, ChoiceLabel: "A"},
			{Description: "方案 B", Quantity: 1, Unit: "式", UnitPriceCents: 30000, LineTotalCents: 30000, IsChoice: true, ChoiceLabel: "B"},
		},
		TotalOptions: []quoteTotalView{
			{Label: "A", ChoiceDescription: "方案 A", SubtotalCents: 30000, TaxCents: 1500, TotalCents: 31500},
			{Label: "B", ChoiceDescription: "方案 B", SubtotalCents: 40000, TaxCents: 2000, TotalCents: 42000},
		},
	}
	rec := httptest.NewRecorder()
	s.renderStandalone(rec, "quote_print.html", map[string]any{
		"Title":       "報價單 Q-CHOICE",
		"CompanyName": "ReViz",
		"FiscalYear":  "2026",
		"Quote":       q,
		"Attachments": []any{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("render status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		"Subtotal A:", "Tax A:", "總計 A:", "Subtotal B:", "Tax B:", "總計 B:",
		"protectedCanvasRanges", "canvasBreakCandidates", "drawBlankMarker", "sign-block--new-page", "以下空白",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("rendered quote is missing %q", expected)
		}
	}
}
