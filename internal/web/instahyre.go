package web

import (
	"bytes"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"

	"jobwatch/internal/store"
)

const maxApplicationPDF = 2 << 20

func (s *Server) handleAPIInstahyreApplication(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxApplicationPDF+(128<<10))
	if err := r.ParseMultipartForm(maxApplicationPDF + (128 << 10)); err != nil {
		http.Error(w, "Invalid application form or résumé exceeds 2 MB", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	field := func(key string) string { return strings.TrimSpace(r.FormValue(key)) }

	posting, err := url.Parse(field("url"))
	if err != nil || posting.Scheme != "https" || posting.User != nil || posting.Port() != "" ||
		(!strings.EqualFold(posting.Hostname(), "instahyre.com") && !strings.EqualFold(posting.Hostname(), "www.instahyre.com")) ||
		strings.Trim(posting.Path, "/") == "" {
		http.Error(w, "Use an HTTPS Instahyre job URL", http.StatusBadRequest)
		return
	}
	posting.Host = "www.instahyre.com"
	posting.Path = strings.TrimRight(posting.Path, "/") + "/"
	posting.RawPath, posting.RawQuery, posting.Fragment, posting.RawFragment = "", "", "", ""
	posting.ForceQuery = false

	a := store.InstahyreApplication{URL: posting.String(), Company: field("company"), Title: field("title"),
		Description: field("jd"), RecipientEmail: field("recipientEmail"), RecipientName: field("recipientName"),
		WorkLink: field("workLink")}
	if a.Company == "" || a.Title == "" || a.Description == "" ||
		len(a.Company) > 200 || len(a.Title) > 200 || len(a.Description) > 60_000 || len(a.RecipientName) > 200 {
		http.Error(w, "Enter a company, role and job description within the form limits", http.StatusBadRequest)
		return
	}
	if a.RecipientEmail != "" {
		address, err := mail.ParseAddress(a.RecipientEmail)
		if err != nil || address.Address != a.RecipientEmail {
			http.Error(w, "Enter one verified work email address", http.StatusBadRequest)
			return
		}
	}
	if a.WorkLink != "" {
		link, err := url.Parse(a.WorkLink)
		if err != nil || link.Scheme != "https" || link.Hostname() == "" || link.User != nil || len(a.WorkLink) > 2048 {
			http.Error(w, "Enter a public HTTPS work sample link", http.StatusBadRequest)
			return
		}
	}

	file, header, err := r.FormFile("resume")
	if err != nil {
		http.Error(w, "Attach the exact PDF used to apply", http.StatusBadRequest)
		return
	}
	defer file.Close()
	a.ResumeName = filepath.Base(header.Filename)
	a.ResumePDF, err = io.ReadAll(io.LimitReader(file, maxApplicationPDF+1))
	if err != nil || len(a.ResumePDF) > maxApplicationPDF || !bytes.HasPrefix(a.ResumePDF, []byte("%PDF-")) ||
		!strings.EqualFold(filepath.Ext(a.ResumeName), ".pdf") || strings.ContainsAny(a.ResumeName, "\r\n") {
		http.Error(w, "Attach a valid PDF under 2 MB", http.StatusBadRequest)
		return
	}
	id, existed, err := s.store.LogInstahyreApplication(r.Context(), a)
	if err != nil {
		httpError(w, "logging Instahyre application", err)
		return
	}
	if !existed {
		w.WriteHeader(http.StatusAccepted)
	}
	writeJSON(w, apiManualJobResponse{ID: id, AlreadyExisted: existed, Company: a.Company, Title: a.Title})
}
