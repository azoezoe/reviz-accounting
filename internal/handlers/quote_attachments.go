package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
)

const maxQuoteAttachmentFiles = 10

type pendingQuoteAttachment struct {
	filename string
	data     []byte
}

func quoteAttachmentKey(quoteID int64, filename string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".pdf" {
		ext = ".pdf"
	}
	return fmt.Sprintf("quote-attachments/%d/%s%s", quoteID, hex.EncodeToString(b), ext), nil
}

func readQuoteAttachment(header *multipart.FileHeader) (pendingQuoteAttachment, error) {
	if header.Size > maxAttachmentBytes {
		return pendingQuoteAttachment{}, fmt.Errorf("%s 超過 20 MB", filepath.Base(header.Filename))
	}
	f, err := header.Open()
	if err != nil {
		return pendingQuoteAttachment{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil {
		return pendingQuoteAttachment{}, err
	}
	if len(data) > maxAttachmentBytes {
		return pendingQuoteAttachment{}, fmt.Errorf("%s 超過 20 MB", filepath.Base(header.Filename))
	}
	if http.DetectContentType(data) != "application/pdf" || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return pendingQuoteAttachment{}, fmt.Errorf("%s 不是有效的 PDF", filepath.Base(header.Filename))
	}
	return pendingQuoteAttachment{filename: filepath.Base(header.Filename), data: data}, nil
}

func (s *Server) quoteAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	quoteID := parseInt64(r.PathValue("id"))
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM quotes WHERE id=$1`, quoteID).Scan(&status); err != nil {
		http.Error(w, "找不到報價單", http.StatusNotFound)
		return
	}
	if status != "draft" {
		http.Error(w, "只有草稿報價單可以變更附件", http.StatusConflict)
		return
	}

	maxRequestBytes := int64(maxQuoteAttachmentFiles*maxAttachmentBytes + 1<<20)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "附件上傳失敗；每份 PDF 不可超過 20 MB", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		http.Error(w, "請選擇 PDF 附件", http.StatusBadRequest)
		return
	}
	if len(headers) > maxQuoteAttachmentFiles {
		http.Error(w, "一次最多上傳 10 份 PDF", http.StatusBadRequest)
		return
	}

	pending := make([]pendingQuoteAttachment, 0, len(headers))
	for _, header := range headers {
		file, err := readQuoteAttachment(header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pending = append(pending, file)
	}

	type createdAttachment struct {
		id  int64
		key string
	}
	var created []createdAttachment
	cleanup := func() {
		for _, item := range created {
			_ = models.DeleteQuoteAttachment(s.DB, item.id)
			_ = s.Attachments.Delete(r.Context(), item.key)
		}
	}
	for _, file := range pending {
		key, err := quoteAttachmentKey(quoteID, file.filename)
		if err != nil {
			cleanup()
			s.error500(w, err)
			return
		}
		if err := s.Attachments.Put(r.Context(), key, "application/pdf", bytes.NewReader(file.data)); err != nil {
			cleanup()
			s.error500(w, fmt.Errorf("上傳報價附件: %w", err))
			return
		}
		a := &models.QuoteAttachment{
			QuoteID:          quoteID,
			StorageKey:       key,
			OriginalFilename: file.filename,
			ContentType:      "application/pdf",
			SizeBytes:        int64(len(file.data)),
		}
		if u := auth.FromContext(r.Context()); u != nil {
			a.UploadedByID = models.NullInt64From(u.ID)
		}
		id, err := models.CreateQuoteAttachment(s.DB, a)
		if err != nil {
			_ = s.Attachments.Delete(r.Context(), key)
			cleanup()
			s.error500(w, err)
			return
		}
		created = append(created, createdAttachment{id: id, key: key})
	}
	http.Redirect(w, r, fmt.Sprintf("/quotes/%d", quoteID), http.StatusSeeOther)
}

func (s *Server) quoteAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	a, err := models.GetQuoteAttachment(s.DB, parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到報價附件", http.StatusNotFound)
		return
	}
	f, err := s.Attachments.Open(r.Context(), a.StorageKey)
	if err != nil {
		s.error500(w, fmt.Errorf("讀取報價附件: %w", err))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", a.SizeBytes))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, a.OriginalFilename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}

func (s *Server) quoteAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	a, err := models.GetQuoteAttachment(s.DB, parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到報價附件", http.StatusNotFound)
		return
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM quotes WHERE id=$1`, a.QuoteID).Scan(&status); err != nil {
		http.Error(w, "找不到報價單", http.StatusNotFound)
		return
	}
	if status != "draft" {
		http.Error(w, "只有草稿報價單可以變更附件", http.StatusConflict)
		return
	}
	if err := s.Attachments.Delete(r.Context(), a.StorageKey); err != nil {
		s.error500(w, fmt.Errorf("刪除報價附件: %w", err))
		return
	}
	if err := models.DeleteQuoteAttachment(s.DB, a.ID); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/quotes/%d", a.QuoteID), http.StatusSeeOther)
}
