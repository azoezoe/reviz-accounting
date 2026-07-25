package handlers

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/models"
	"github.com/hcchien/reviz-accounting/internal/money"
)

var csvHeader = []string{
	"code", "date", "counterparty", "description", "category", "amount",
	"from_account", "to_account", "project", "note",
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	txs, _, err := models.ListTransactions(s.DB, models.TxFilter{})
	if err != nil {
		s.error500(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=transactions-%s.csv", time.Now().Format("20060102-150405")))
	// BOM so Excel opens UTF-8 correctly
	w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write(csvHeader)
	for _, t := range txs {
		cw.Write([]string{
			t.Code,
			t.Date,
			t.CounterpartyName,
			t.Description,
			t.CategoryName,
			money.FormatCents(t.AmountCents),
			t.FromAccountName,
			t.ToAccountName,
			t.ProjectName,
			t.Note,
		})
	}
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "import.html", map[string]any{
		"Title":  "匯入 CSV",
		"Crumbs": []string{"設定", "匯入 CSV"},
		"Active": "settings",
	})
}

type importResult struct {
	Imported int
	Skipped  int
	Errors   []string
}

func (s *Server) importCSV(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.error500(w, err)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "請選擇 CSV 檔案", http.StatusBadRequest)
		return
	}
	defer f.Close()

	res, err := s.importTransactions(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.render(w, r, "import.html", map[string]any{
		"Title":  "匯入 CSV",
		"Crumbs": []string{"設定", "匯入 CSV"},
		"Active": "settings",
		"Result": res,
	})
}

func (s *Server) importTransactions(r io.Reader) (*importResult, error) {
	// strip BOM
	br := newBOMStripper(r)
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(strings.ToLower(h))] = i
	}
	required := []string{"date", "description", "amount"}
	for _, k := range required {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("CSV 缺少必要欄位：%s", k)
		}
	}

	// preload lookups
	cats, _ := models.ListCategories(s.DB)
	catByName := map[string]int64{}
	for _, c := range cats {
		catByName[c.Name] = c.ID
	}
	accs, _ := models.ListAccounts(s.DB, false)
	accByName := map[string]int64{}
	for _, a := range accs {
		accByName[a.Name] = a.ID
	}
	projs, _ := models.ListProjects(s.DB)
	projByName := map[string]int64{}
	for _, p := range projs {
		projByName[p.Name] = p.ID
	}

	get := func(row []string, key string) string {
		i, ok := idx[key]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	res := &importResult{}
	lineNum := 1
	for {
		row, err := cr.Read()
		lineNum++
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：%s", lineNum, err.Error()))
			continue
		}
		date := get(row, "date")
		desc := get(row, "description")
		amtStr := get(row, "amount")
		if date == "" || amtStr == "" {
			res.Skipped++
			continue
		}
		if _, err := time.Parse("2006-01-02", date); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：日期格式 %q 不是 YYYY-MM-DD", lineNum, date))
			continue
		}
		amt, err := money.ParseCents(amtStr)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：金額 %q 無法解析", lineNum, amtStr))
			continue
		}
		if amt < 0 {
			amt = -amt
		}
		if amt == 0 {
			res.Skipped++
			continue
		}

		from := accByName[get(row, "from_account")]
		to := accByName[get(row, "to_account")]
		if from == 0 && to == 0 {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：from_account 與 to_account 都是空的或找不到", lineNum))
			continue
		}

		t := &models.Transaction{
			Date:           date,
			Description:    desc,
			CounterpartyID: models.NullInt64From(0),
			AmountCents:    amt,
			FromAccountID:  models.NullInt64From(from),
			ToAccountID:    models.NullInt64From(to),
			CategoryID:     models.NullInt64From(catByName[get(row, "category")]),
			ProjectID:      models.NullInt64From(projByName[get(row, "project")]),
			Note:           get(row, "note"),
		}
		if cp, err := models.GetOrCreateCounterparty(s.DB, get(row, "counterparty")); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：交易對象 %s", lineNum, err))
			continue
		} else {
			t.CounterpartyID = cp
		}
		if code := get(row, "code"); code != "" {
			t.Code = code
		} else {
			c, err := models.GenerateCode(s.DB)
			if err != nil {
				return nil, err
			}
			t.Code = c
		}
		if _, err := models.CreateTransaction(s.DB, t); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("第 %d 行：%s", lineNum, err.Error()))
			continue
		}
		res.Imported++
	}
	return res, nil
}

// bomStripper consumes the optional UTF-8 BOM at the start of r.
type bomStripper struct {
	r       io.Reader
	checked bool
	buf     []byte
}

func newBOMStripper(r io.Reader) io.Reader { return &bomStripper{r: r} }

func (b *bomStripper) Read(p []byte) (int, error) {
	if !b.checked {
		b.checked = true
		var prefix [3]byte
		n, err := io.ReadFull(b.r, prefix[:])
		if n > 0 && prefix[0] == 0xEF && prefix[1] == 0xBB && prefix[2] == 0xBF {
			// drop BOM
		} else if n > 0 {
			b.buf = append(b.buf, prefix[:n]...)
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, err
		}
	}
	if len(b.buf) > 0 {
		n := copy(p, b.buf)
		b.buf = b.buf[n:]
		return n, nil
	}
	return b.r.Read(p)
}

// silence unused import for sql when not directly referenced
var _ = sql.ErrNoRows
