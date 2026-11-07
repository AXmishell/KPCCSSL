package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
)

func (s *Server) listCA(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListCAAccounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "presets": s.Svc.CAPresets()})
}

func (s *Server) createCA(w http.ResponseWriter, r *http.Request) {
	var in service.CAInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	a, err := s.Svc.CreateCA(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.create", "ca_account", a.ID, "ok", "")
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) updateCA(w http.ResponseWriter, r *http.Request) {
	var in service.CAInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	a, err := s.Svc.UpdateCA(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.update", "ca_account", a.ID, "ok", "")
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteCA(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteCA(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.delete", "ca_account", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) disableCA(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := s.Svc.SetCAEnabled(r.Context(), id, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.disable", "ca_account", id, "ok", "")
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) enableCA(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := s.Svc.SetCAEnabled(r.Context(), id, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.enable", "ca_account", id, "ok", "")
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) registerCA(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := s.Svc.RegisterCA(r.Context(), id, idempotencyKey(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "ca.register", "ca_account", id, "ok", "job="+j.ID)
	accepted(w, j)
}

func (s *Server) listDNSProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.Svc.DNS.All()})
}

type dnsView struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Provider         string   `json:"provider"`
	Status           string   `json:"status"`
	LastError        *string  `json:"last_error"`
	LastUsedAt       *int64   `json:"last_used_at"`
	ConfiguredFields []string `json:"configured_fields"`
	CreatedAt        int64    `json:"created_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

func (s *Server) dnsView(d store.DNSCredential) dnsView {
	v := dnsView{ID: d.ID, Name: d.Name, Provider: d.Provider, Status: d.Status, LastError: d.LastError, LastUsedAt: d.LastUsedAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, ConfiguredFields: []string{}}
	if cfg, err := s.Svc.DecryptDNSConfig(&d); err == nil {
		for k := range cfg {
			v.ConfiguredFields = append(v.ConfiguredFields, k)
		}
	}
	return v
}

func (s *Server) listDNS(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListDNSCredentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]dnsView, 0, len(items))
	for _, d := range items {
		out = append(out, s.dnsView(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createDNS(w http.ResponseWriter, r *http.Request) {
	var in service.DNSInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	d, err := s.Svc.CreateDNS(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "dns.create", "dns_credential", d.ID, "ok", "provider="+d.Provider)
	writeJSON(w, http.StatusCreated, s.dnsView(*d))
}

func (s *Server) updateDNS(w http.ResponseWriter, r *http.Request) {
	var in service.DNSInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	d, err := s.Svc.UpdateDNS(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "dns.update", "dns_credential", d.ID, "ok", "")
	writeJSON(w, http.StatusOK, s.dnsView(*d))
}

func (s *Server) deleteDNS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteDNS(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "dns.delete", "dns_credential", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyDNS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := s.Svc.VerifyDNS(r.Context(), id)
	if err != nil {
		s.audit(r, "dns.verify", "dns_credential", id, "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "dns.verify", "dns_credential", id, "ok", "")
	writeJSON(w, http.StatusOK, s.dnsView(*d))
}

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 50, 200)
	cursor, cursorID := parseCursor(r)
	items, err := s.Store.ListCertificates(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("q"), limit, cursor, cursorID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var next any
	if len(items) == limit && len(items) > 0 {
		last := items[len(items)-1]
		next = fmt.Sprintf("%d:%s", last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := s.Store.GetCertificate(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var ca *store.CAAccount
	if a, err := s.Store.GetCAAccount(r.Context(), c.CAAccountID); err == nil {
		ca = a
	}
	var cred *dnsView
	if c.DNSCredentialID != nil {
		if d, err := s.Store.GetDNSCredential(r.Context(), *c.DNSCredentialID); err == nil {
			v := s.dnsView(*d)
			cred = &v
		}
	}
	var rev *store.Revision
	if c.CurrentRevisionID != nil {
		rev, _ = s.Store.GetRevision(r.Context(), *c.CurrentRevisionID)
	}
	deps, _ := s.Store.ListDeployments(r.Context(), c.ID)
	jobs, _ := s.Store.ListJobs(r.Context(), c.ID, "", 10)
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate": c, "ca_account": ca, "dns_credential": cred,
		"current_revision": rev, "deployments": deps, "jobs": jobs,
	})
}

func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request) {
	var in service.CertInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.CreateCertificate(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.create", "certificate", c.ID, "ok", c.PrimaryDomain)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) updateCertificate(w http.ResponseWriter, r *http.Request) {
	var in service.CertInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.UpdateCertificate(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.update", "certificate", c.ID, "ok", "")
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.SoftDeleteCertificate(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.delete", "certificate", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) issueCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Force bool `json:"force"`
	}
	_ = decodeOptional(r, &body)
	j, err := s.Svc.Issue(r.Context(), id, idempotencyKey(r), body.Force)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.issue", "certificate", id, "ok", "job="+j.ID)
	accepted(w, j)
}

func (s *Server) renewCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Force bool `json:"force"`
	}
	_ = decodeOptional(r, &body)
	j, err := s.Svc.Renew(r.Context(), id, idempotencyKey(r), body.Force)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.renew", "certificate", id, "ok", "job="+j.ID)
	accepted(w, j)
}

func (s *Server) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Reason int `json:"reason"`
	}
	_ = decodeOptional(r, &body)
	j, err := s.Svc.Revoke(r.Context(), id, idempotencyKey(r), body.Reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.revoke", "certificate", id, "ok", fmt.Sprintf("job=%s reason=%d", j.ID, body.Reason))
	accepted(w, j)
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetCertificate(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := s.Store.ListRevisions(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listCertEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	items, err := s.Store.ListEvents(r.Context(), id, parseLimit(r, 100, 500))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deployCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	jobs, err := s.Svc.DeployNow(r.Context(), id, idempotencyKey(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "certificate.deploy", "certificate", id, "ok", fmt.Sprintf("%d job(s)", len(jobs)))
	writeJSON(w, http.StatusAccepted, map[string]any{"job_ids": jobIDs(jobs)})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListJobs(r.Context(), r.URL.Query().Get("certificate_id"), r.URL.Query().Get("status"), parseLimit(r, 50, 200))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Store.GetJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.CancelJob(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "job.cancel", "job", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.RetryJob(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "job.retry", "job", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadJobLog(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetJob(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	path := s.Svc.Logs.Path(id)
	if _, err := os.Stat(path); err != nil { // #nosec G703 -- path is <log-dir>/<validated-job-id>.log
		writeProblem(w, r, http.StatusNotFound, "E_NOT_FOUND", "job log not found", nil)
		return
	}
	s.audit(r, "job.log.download", "job", id, "ok", "")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".log"))
	http.ServeFile(w, r, path) // #nosec G703 -- path is <log-dir>/<validated-job-id>.log
}

func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, r, http.StatusInternalServerError, "E_SSE", "streaming unsupported", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	last := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		last, _ = strconv.ParseInt(raw, 10, 64)
	} else if raw := r.URL.Query().Get("after"); raw != "" {
		last, _ = strconv.ParseInt(raw, 10, 64)
	}
	// Subscribe before replaying history: otherwise lines written between the
	// file read and Subscribe are lost for the rest of a long-running job.
	lines, unsubscribe := s.Svc.Logs.Subscribe(id)
	defer unsubscribe()

	fmt.Fprint(w, "retry: 3000\n\n")
	if history, err := s.Svc.Logs.Read(id, last, 5000); err == nil {
		for _, ln := range history {
			writeSSE(w, "job.log", ln.Seq, ln)
			last = ln.Seq
		}
	}
	writeSSE(w, "job.status", 0, map[string]any{"status": j.Status, "attempt": j.Attempts})
	flusher.Flush()

	if isTerminal(j.Status) {
		done := map[string]any{"status": j.Status}
		if j.ErrorCode != nil {
			done["error_code"] = *j.ErrorCode
		}
		if j.ErrorMessage != nil {
			done["error_message"] = *j.ErrorMessage
		}
		writeSSE(w, "job.done", 0, done)
		flusher.Flush()
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	status := j.Status
	for {
		select {
		case <-r.Context().Done():
			return
		case ln := <-lines:
			if ln.Seq <= last {
				continue
			}
			last = ln.Seq
			writeSSE(w, "job.log", ln.Seq, ln)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-poll.C:
			cur, err := s.Store.GetJob(r.Context(), id)
			if err != nil {
				return
			}
			if cur.Status != status {
				status = cur.Status
				writeSSE(w, "job.status", 0, map[string]any{"status": status, "attempt": cur.Attempts})
				flusher.Flush()
			}
			if isTerminal(status) {
				// Flush any remaining buffered lines first.
				if rest, _ := s.Svc.Logs.Read(id, last, 5000); len(rest) > 0 {
					for _, ln := range rest {
						writeSSE(w, "job.log", ln.Seq, ln)
					}
				}
				done := map[string]any{"status": status}
				if cur.ErrorCode != nil {
					done["error_code"] = *cur.ErrorCode
				}
				if cur.ErrorMessage != nil {
					done["error_message"] = *cur.ErrorMessage
				}
				writeSSE(w, "job.done", 0, done)
				flusher.Flush()
				return
			}
		}
	}
}

func writeSSE(w io.Writer, event string, seq int64, data any) {
	if seq > 0 {
		fmt.Fprintf(w, "event: %s\nid: %d\n", event, seq)
	} else {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	raw, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", raw)
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled", "interrupted":
		return true
	}
	return false
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListDeployments(r.Context(), r.URL.Query().Get("certificate_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var in service.DeploymentInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if in.CertificateID == "" {
		writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", "certificate_id is required", nil)
		return
	}
	d, err := s.Svc.CreateDeployment(r.Context(), in.CertificateID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment.create", "deployment", d.ID, "ok", d.Type)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDeployment(w http.ResponseWriter, r *http.Request) {
	var in service.DeploymentInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	d, err := s.Svc.UpdateDeployment(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment.update", "deployment", d.ID, "ok", "")
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteDeployment(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment.delete", "deployment", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDeploymentConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if err := s.Svc.TestDeployment(r.Context(), in.Type, in.Config); err != nil {
		s.audit(r, "deployment.test", "deployment", in.Type, "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment.test", "deployment", in.Type, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.TestDeploymentByID(r.Context(), id); err != nil {
		s.audit(r, "deployment.test", "deployment", id, "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment.test", "deployment", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListNotificationChannels(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]service.NotificationView, 0, len(items))
	for _, c := range items {
		out = append(out, s.Svc.NotificationChannelView(r.Context(), c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "events": service.SupportedEvents()})
}

func (s *Server) createNotification(w http.ResponseWriter, r *http.Request) {
	var in service.NotificationInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.CreateNotification(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "notification.create", "notification_channel", c.ID, "ok", c.Type)
	writeJSON(w, http.StatusCreated, s.Svc.NotificationChannelView(r.Context(), *c))
}

func (s *Server) updateNotification(w http.ResponseWriter, r *http.Request) {
	var in service.NotificationInput
	if err := decodeJSON(w, r, &in, 128*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.UpdateNotification(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "notification.update", "notification_channel", c.ID, "ok", "")
	writeJSON(w, http.StatusOK, s.Svc.NotificationChannelView(r.Context(), *c))
}

func (s *Server) deleteNotification(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteNotification(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "notification.delete", "notification_channel", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.TestNotification(r.Context(), id); err != nil {
		s.audit(r, "notification.test", "notification_channel", id, "error", err.Error())
		var notifyErr *service.NotificationError
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeProblem(w, r, http.StatusNotFound, "E_NOT_FOUND", "resource not found", nil)
		case errors.Is(err, service.ErrMailNotConfigured):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_MAIL_NOT_CONFIGURED", "outbound email is not configured", nil)
		case errors.As(err, &notifyErr):
			writeProblem(w, r, http.StatusBadGateway, "E_NOTIFY", "could not send test notification", nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "notification.test", "notification_channel", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	cursor, cursorID := parseCursor(r)
	limit := parseLimit(r, 50, 200)
	items, err := s.Store.ListAudit(r.Context(), limit, cursor, cursorID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var next any
	if len(items) == limit && len(items) > 0 {
		last := items[len(items)-1]
		next = fmt.Sprintf("%d:%s", last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) settingsView(ctx context.Context) (map[string]any, error) {
	view, err := s.Svc.SettingsView(ctx)
	if err != nil {
		return nil, err
	}
	_, cfgErr := s.Svc.MailConfig(ctx)
	view["mail_configured"] = cfgErr == nil
	view["mail_credentials_available"] = !errors.Is(cfgErr, service.ErrMailCredentialsUnavailable)
	return view, nil
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	view, err := s.settingsView(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

var allowedSettings = map[string]bool{
	"instance_name": true, "default_ca_id": true, "renewal_notice_days": true,
	"retention_days": true, "theme": true,
	"smtp_host": true, "smtp_port": true, "smtp_username": true,
	"smtp_password": true, "smtp_from": true, "smtp_tls": true,
}

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	for k, v := range body {
		if !allowedSettings[k] {
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", "unknown setting "+k, nil)
			return
		}
		if err := service.ValidMailSetting(k, v); err != nil {
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", err.Error(), nil)
			return
		}
	}
	for k, v := range body {
		secret := k == service.SettingSMTPPassword
		if secret {
			if pw, _ := v.(string); pw == "" || pw == "********" {
				continue
			}
		}
		if err := s.Svc.SetSetting(r.Context(), k, v, secret); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.audit(r, "settings.update", "settings", "", "ok", "")
	view, _ := s.settingsView(r.Context())
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	needsSetup, _ := s.Svc.NeedsSetup(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.Version, "setup_required": needsSetup})
}

// metrics exposes a small Prometheus-compatible surface without an external
// dependency. Certificate/job counts are enough for the common dashboards.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	total, expiring, failed, active, _ := s.Store.CertificateStats(r.Context())
	jobs, _ := s.Store.JobStatuses(r.Context())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP kpccssl_certificates_total Live certificates.\n")
	fmt.Fprintf(w, "# TYPE kpccssl_certificates_total gauge\n")
	fmt.Fprintf(w, "kpccssl_certificates_total %d\n", total)
	fmt.Fprintf(w, "kpccssl_certificates_active %d\n", active)
	fmt.Fprintf(w, "kpccssl_certificates_failed %d\n", failed)
	fmt.Fprintf(w, "kpccssl_certificates_expiring_30d %d\n", expiring)
	fmt.Fprintf(w, "# TYPE kpccssl_jobs_total gauge\n")
	for status, n := range jobs {
		fmt.Fprintf(w, "kpccssl_jobs_total{status=%q} %d\n", status, n)
	}
}

// ready is a readiness probe: database, writable data directory and (unless
// the fake engine is enabled) the acme.sh binary must be usable.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "E_NOT_READY", "database unavailable", nil)
		return
	}
	f, err := os.CreateTemp(s.Cfg.DataDir, ".ready-*")
	if err != nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "E_NOT_READY", "data directory is not writable", nil)
		return
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	if !s.Cfg.FakeACME {
		st, err := os.Stat(s.Cfg.ACMEBin)
		if err != nil || st.Mode()&0o111 == 0 {
			writeProblem(w, r, http.StatusServiceUnavailable, "E_NOT_READY", "acme.sh is not executable", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	var free uint64
	var fs syscall.Statfs_t
	if err := syscall.Statfs(s.Cfg.DataDir, &fs); err == nil {
		free = fs.Bavail * uint64(fs.Bsize)
	}
	acmeVersion := "unavailable"
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if v, err := s.Svc.ACMEVersion(ctx); err == nil {
		acmeVersion = v
	}
	needsSetup, _ := s.Svc.NeedsSetup(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "acme_version": acmeVersion, "setup_required": needsSetup,
		"data_dir": s.Cfg.DataDir, "disk_free_bytes": free,
		"server_time": store.Now(), "fake_acme": s.Cfg.FakeACME,
		"instance_name":       s.Svc.SettingString(r.Context(), "instance_name", "KPCCSSL"),
		"default_ca_id":       s.Svc.SettingString(r.Context(), "default_ca_id", ""),
		"default_theme":       s.Svc.SettingString(r.Context(), "theme", "light"),
		"renewal_notice_days": s.Svc.SettingInt(r.Context(), "renewal_notice_days", 30),
	})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	total, expiring, failed, active, err := s.Store.CertificateStats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	jobs, _ := s.Store.JobStatuses(r.Context())
	recent, _ := s.Store.RecentJobs(r.Context(), 10)
	writeJSON(w, http.StatusOK, map[string]any{
		"certificates": map[string]int{"total": total, "expiring_30d": expiring, "failed": failed, "active": active},
		"jobs":         jobs,
		"recent_jobs":  recent,
	})
}

func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request) bool {
	p := principal(r)
	if p == nil || !s.Auth.VerifyStepUp(p, r.Header.Get("X-Step-Up-Token"), time.Now()) {
		writeProblem(w, r, http.StatusPreconditionRequired, "E_STEP_UP_REQUIRED",
			"re-authenticate at POST /api/v1/me/step-up and send X-Step-Up-Token", nil)
		return false
	}
	return true
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "pem"
	}
	rev, err := s.Svc.RevisionFiles(r.Context(), id, r.URL.Query().Get("revision"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	base := "certificate"
	if c, err := s.Store.GetCertificate(r.Context(), id); err == nil {
		base = strings.NewReplacer("*", "wildcard", "/", "_", " ", "_").Replace(c.PrimaryDomain)
	}
	w.Header().Set("Cache-Control", "no-store")
	switch format {
	case "zip":
		p := principal(r)
		if !auth.RoleAllowed(p.User.Role, "operator") {
			writeProblem(w, r, http.StatusForbidden, "E_FORBIDDEN", "operator role required for ZIP bundles containing private keys", nil)
			return
		}
		if !s.requireStepUp(w, r) {
			return
		}
		var buf bytes.Buffer
		if err := s.Svc.WriteZIP(&buf, rev); err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+"-certs.zip"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
		s.audit(r, "certificate.download.zip", "certificate", id, "ok", "")
	case "pfx", "p12":
		if r.Method != http.MethodPost {
			writeProblem(w, r, http.StatusMethodNotAllowed, "E_METHOD", "PFX export requires POST with a password body", nil)
			return
		}
		p := principal(r)
		if !auth.RoleAllowed(p.User.Role, "operator") {
			writeProblem(w, r, http.StatusForbidden, "E_FORBIDDEN", "operator role required for PFX export", nil)
			return
		}
		if !s.requireStepUp(w, r) {
			return
		}
		var body struct {
			Password string `json:"password"`
			Revision string `json:"revision"`
		}
		if err := decodeJSON(w, r, &body, 16*1024); err != nil {
			writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
			return
		}
		if body.Revision != "" && body.Revision != rev.ID {
			rev, err = s.Svc.RevisionFiles(r.Context(), id, body.Revision)
			if err != nil {
				s.fail(w, r, err)
				return
			}
		}
		var buf bytes.Buffer
		if err := s.Svc.WritePFX(&buf, rev, body.Password); err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/x-pkcs12")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".pfx"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
		s.audit(r, "certificate.download.pfx", "certificate", id, "ok", "revision="+rev.ID)
	case "pem":
		includeKey := r.URL.Query().Get("key") == "true"
		var buf bytes.Buffer
		if includeKey {
			p := principal(r)
			if !auth.RoleAllowed(p.User.Role, "operator") {
				writeProblem(w, r, http.StatusForbidden, "E_FORBIDDEN", "operator role required to download private keys", nil)
				return
			}
			if !s.requireStepUp(w, r) {
				return
			}
			key, err := s.Svc.ReadPrivateKeyFile(rev)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			buf.Write(key)
			s.audit(r, "certificate.private_key.download", "certificate", id, "ok", "revision="+rev.ID)
		}
		full, err := os.ReadFile(rev.FullchainPath) // #nosec G703 -- path comes from the certificate revision row
		if err != nil {
			s.fail(w, r, err)
			return
		}
		buf.Write(full)
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".pem"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
		if !includeKey {
			s.audit(r, "certificate.download.pem", "certificate", id, "ok", "revision="+rev.ID)
		}
	default:
		writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", "format must be pem, zip or pfx", nil)
	}
}

func accepted(w http.ResponseWriter, j *store.Job) {
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": j.ID})
}

func idempotencyKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 128 {
		key = key[:128]
	}
	return key
}

func jobIDs(jobs []*store.Job) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ID)
	}
	return out
}

func decodeOptional(r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
