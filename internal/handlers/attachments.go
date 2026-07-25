package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/hcchien/reviz-accounting/internal/auth"
	"github.com/hcchien/reviz-accounting/internal/models"
)

const maxAttachmentBytes = 20 << 20

var allowedAttachmentTypes = map[string]bool{"application/pdf": true, "image/jpeg": true, "image/png": true, "image/webp": true}

func attachmentKey(transactionID int64, filename string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if len(ext) > 12 {
		ext = ""
	}
	return fmt.Sprintf("attachments/%d/%s%s", transactionID, hex.EncodeToString(b), ext), nil
}

func (s *Server) attachmentUpload(w http.ResponseWriter, r *http.Request) {
	transactionID := parseInt64(r.PathValue("id"))
	if _, err := models.GetTransaction(s.DB, transactionID); err != nil {
		http.Error(w, "找不到交易", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+1024)
	if err := r.ParseMultipartForm(maxAttachmentBytes); err != nil {
		http.Error(w, "單據大小不可超過 20 MB", http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "請選擇單據檔案", http.StatusBadRequest)
		return
	}
	defer f.Close()
	if header.Size > maxAttachmentBytes {
		http.Error(w, "單據大小不可超過 20 MB", http.StatusBadRequest)
		return
	}
	first := make([]byte, 512)
	n, err := io.ReadFull(f, first)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		s.error500(w, err)
		return
	}
	contentType := http.DetectContentType(first[:n])
	if !allowedAttachmentTypes[contentType] {
		http.Error(w, "只接受 PDF、JPG、PNG 或 WebP 單據", http.StatusBadRequest)
		return
	}
	key, err := attachmentKey(transactionID, header.Filename)
	if err != nil {
		s.error500(w, err)
		return
	}
	if err := s.Attachments.Put(r.Context(), key, contentType, io.MultiReader(bytes.NewReader(first[:n]), io.LimitReader(f, maxAttachmentBytes+1))); err != nil {
		s.error500(w, fmt.Errorf("上傳單據: %w", err))
		return
	}
	u := auth.FromContext(r.Context())
	a := &models.Attachment{TransactionID: transactionID, StorageKey: key, OriginalFilename: filepath.Base(header.Filename), ContentType: contentType, SizeBytes: header.Size}
	if u != nil {
		a.UploadedByID = models.NullInt64From(u.ID)
	}
	if _, err := models.CreateAttachment(s.DB, a); err != nil {
		_ = s.Attachments.Delete(r.Context(), key)
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/journal/%d/edit", transactionID), http.StatusSeeOther)
}

func (s *Server) attachmentDownload(w http.ResponseWriter, r *http.Request) {
	a, err := models.GetAttachment(s.DB, parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到單據", http.StatusNotFound)
		return
	}
	f, err := s.Attachments.Open(r.Context(), a.StorageKey)
	if err != nil {
		s.error500(w, fmt.Errorf("讀取單據: %w", err))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", a.SizeBytes))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, a.OriginalFilename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, f)
}

func (s *Server) attachmentDelete(w http.ResponseWriter, r *http.Request) {
	a, err := models.GetAttachment(s.DB, parseInt64(r.PathValue("id")))
	if err != nil {
		http.Error(w, "找不到單據", http.StatusNotFound)
		return
	}
	if err := s.Attachments.Delete(r.Context(), a.StorageKey); err != nil {
		s.error500(w, fmt.Errorf("刪除 GCS 單據: %w", err))
		return
	}
	if err := models.DeleteAttachment(s.DB, a.ID); err != nil {
		s.error500(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/journal/%d/edit", a.TransactionID), http.StatusSeeOther)
}

func formatAttachmentSize(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1024)
}
