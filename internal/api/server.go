// Package api exposes the HTTP contract described in docs/openapi.yaml.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/config"
	"github.com/axmishell/kpccssl/internal/dnsprovider"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
)

// Server wires HTTP handlers to services.
type Server struct {
	Cfg     *config.Config
	Store   *store.Store
	Svc     *service.Service
	Auth    *auth.Manager
	Version string

	handler http.Handler
}

// New builds the router.
func New(cfg *config.Config, st *store.Store, svc *service.Service, am *auth.Manager, version string) *Server {
	s := &Server{Cfg: cfg, Store: st, Svc: svc, Auth: am, Version: version}
	s.handler = s.routes()
	return s
}

// Handler returns the composed handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.requestID)
	r.Use(s.realIP)
	r.Use(s.recoverer)
	r.Use(s.accessLog)
	r.Use(securityHeaders(s.Cfg.Dev))
	r.Use(s.loadSession)
	r.Get("/metrics", s.metrics)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/system/health", s.health)
		api.Get("/system/ready", s.ready)
		api.Post("/setup", s.setup)
		api.Post("/auth/login", s.login)
		api.Group(func(pub chi.Router) {
			pub.Use(s.csrfGuard)
			pub.Use(s.requireAuth)
			pub.Post("/auth/logout", s.logout)
		})

		api.Group(func(pr chi.Router) {
			pr.Use(s.csrfGuard)
			pr.Use(s.requireAuth)
			pr.Get("/me", s.me)
			pr.Post("/me/step-up", s.stepUp)
			pr.Post("/me/password", s.changePassword)
			pr.Post("/me/totp/setup", s.totpSetup)
			pr.Post("/me/totp/enable", s.totpEnable)
			pr.Post("/me/totp/disable", s.totpDisable)
			pr.Get("/system/info", s.info)
			pr.Get("/dashboard", s.dashboard)

			pr.Get("/ca-accounts", s.listCA)
			pr.Get("/dns-providers", s.listDNSProviders)
			pr.Get("/dns-credentials", s.listDNS)
			pr.Get("/certificates", s.listCertificates)
			pr.Get("/certificates/{id}", s.getCertificate)
			pr.Get("/certificates/{id}/revisions", s.listRevisions)
			pr.Get("/certificates/{id}/events", s.listCertEvents)
			pr.Get("/certificates/{id}/download", s.download)
			pr.Post("/certificates/{id}/download", s.download)
			pr.Get("/jobs", s.listJobs)
			pr.Get("/jobs/{id}", s.getJob)
			pr.Get("/jobs/{id}/events", s.jobEvents)
			pr.Get("/deployments", s.listDeployments)
			pr.Get("/workflows", s.listWorkflows)
			pr.Get("/workflows/{id}", s.getWorkflow)
			pr.Get("/workflows/{id}/runs", s.listWorkflowRuns)
			pr.Get("/workflow-runs/{id}", s.getWorkflowRun)
			pr.Get("/workflow-runs/{id}/log", s.workflowRunLog)
		})

		// Mutating and privileged endpoints.
		api.Group(func(admin chi.Router) {
			admin.Use(s.csrfGuard)
			admin.Use(s.requireAuth)
			admin.Use(s.requireRole("admin"))

			admin.Post("/ca-accounts", s.createCA)
			admin.Patch("/ca-accounts/{id}", s.updateCA)
			admin.Delete("/ca-accounts/{id}", s.deleteCA)
			admin.Post("/ca-accounts/{id}/register", s.registerCA)
			admin.Post("/ca-accounts/{id}/disable", s.disableCA)
			admin.Post("/ca-accounts/{id}/enable", s.enableCA)
			admin.Post("/dns-credentials", s.createDNS)
			admin.Patch("/dns-credentials/{id}", s.updateDNS)
			admin.Delete("/dns-credentials/{id}", s.deleteDNS)
			admin.Post("/dns-credentials/{id}/verify", s.verifyDNS)
			admin.Delete("/certificates/{id}", s.deleteCertificate)
			admin.Get("/notification-channels", s.listNotifications)
			admin.Post("/notification-channels", s.createNotification)
			admin.Patch("/notification-channels/{id}", s.updateNotification)
			admin.Delete("/notification-channels/{id}", s.deleteNotification)
			admin.Post("/notification-channels/{id}/test", s.testNotification)
			admin.Patch("/settings", s.patchSettings)
			admin.Get("/settings", s.getSettings)
			admin.Get("/audit-logs", s.listAudit)
			admin.Get("/jobs/{id}/log", s.downloadJobLog)
			admin.Get("/users", s.listUsers)
			admin.Post("/users", s.createUser)
			admin.Patch("/users/{id}", s.updateUser)
			admin.Post("/workflows", s.createWorkflow)
			admin.Patch("/workflows/{id}", s.updateWorkflow)
			admin.Delete("/workflows/{id}", s.deleteWorkflow)
		})

		api.Group(func(op chi.Router) {
			op.Use(s.csrfGuard)
			op.Use(s.requireAuth)
			op.Use(s.requireRole("operator"))

			op.Post("/certificates", s.createCertificate)
			op.Patch("/certificates/{id}", s.updateCertificate)
			op.Post("/certificates/{id}/issue", s.issueCertificate)
			op.Post("/certificates/{id}/renew", s.renewCertificate)
			op.Post("/certificates/{id}/revoke", s.revokeCertificate)
			op.Post("/certificates/{id}/deploy", s.deployCertificate)
			op.Post("/deployments", s.createDeployment)
			op.Patch("/deployments/{id}", s.updateDeployment)
			op.Delete("/deployments/{id}", s.deleteDeployment)
			op.Post("/deployments/test", s.testDeploymentConfig)
			op.Post("/deployments/{id}/test", s.testDeployment)
			op.Post("/jobs/{id}/cancel", s.cancelJob)
			op.Post("/jobs/{id}/retry", s.retryJob)
			op.Post("/workflows/{id}/run", s.runWorkflow)
			op.Post("/workflow-runs/{id}/cancel", s.cancelWorkflowRun)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeProblem(w, r, http.StatusNotFound, "E_NOT_FOUND", "endpoint not found", nil)
			return
		}
		s.webHandler().ServeHTTP(w, r)
	})
	r.Get("/*", s.webHandler().ServeHTTP)
	return r
}

// webHandler is set by the cmd package to avoid an import cycle with webui.
var web http.Handler

// SetWebHandler installs the SPA handler.
func SetWebHandler(h http.Handler) { web = h }

func (s *Server) webHandler() http.Handler {
	if web != nil {
		return web
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
}

// --- middleware ---------------------------------------------------------

type ctxKey string

const (
	ctxRequestID ctxKey = "request_id"
	ctxIP        ctxKey = "ip"
	ctxPrincipal ctxKey = "principal"
)

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" || len(rid) > 64 {
			rid = id.Request()
		}
		w.Header().Set("X-Request-ID", rid)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, rid)))
	})
}

func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := remoteIP(r)
		if s.Cfg.IsTrustedProxy(ip) {
			if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
				first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
				if net.ParseIP(first) != nil {
					ip = first
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxIP, ip)))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic handling request", "path", r.URL.Path, "panic", rec)
				writeProblem(w, r, http.StatusInternalServerError, "E_INTERNAL", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(start).Milliseconds(), "request_id", requestID(r))
	})
}

func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.Cfg.SessionCookieName())
		if err == nil && cookie.Value != "" {
			if p, err := s.Auth.Resolve(r.Context(), cookie.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r) == nil {
			writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "login required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principal(r)
			if p == nil {
				writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "login required", nil)
				return
			}
			if !auth.RoleAllowed(p.User.Role, role) {
				s.audit(r, "rbac."+role, "request", r.URL.Path, "denied", "")
				writeProblem(w, r, http.StatusForbidden, "E_FORBIDDEN", "insufficient permissions", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !s.originOK(r) {
			writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
			return
		}
		p := principal(r)
		if p == nil {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if !s.Auth.VerifyCSRF(p, token) {
			s.audit(r, "csrf.rejected", "request", r.URL.Path, "denied", "")
			writeProblem(w, r, http.StatusForbidden, "E_CSRF", "invalid CSRF token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if s.Cfg.BaseURL != "" {
		if b, err := url.Parse(s.Cfg.BaseURL); err == nil && strings.EqualFold(b.Host, u.Host) {
			return true
		}
	}
	return false
}

func securityHeaders(dev bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			if !dev || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- helpers ------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so SSE works through the access log
// middleware.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the original writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func requestID(r *http.Request) string {
	v, _ := r.Context().Value(ctxRequestID).(string)
	return v
}

func clientIP(r *http.Request) string {
	v, _ := r.Context().Value(ctxIP).(string)
	return v
}

func principal(r *http.Request) *auth.Principal {
	p, _ := r.Context().Value(ctxPrincipal).(*auth.Principal)
	return p
}

type problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Code      string            `json:"code"`
	Detail    string            `json:"detail,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string, fields map[string]string) {
	p := problem{
		Type:      "https://kpccssl.local/errors/" + strings.ToLower(strings.TrimPrefix(code, "E_")),
		Title:     http.StatusText(status),
		Status:    status,
		Code:      code,
		Detail:    detail,
		RequestID: requestID(r),
		Fields:    fields,
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	writeJSONBody(w, p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	writeJSONBody(w, v)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *service.ConflictError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "E_NOT_FOUND", "resource not found", nil)
	case errors.Is(err, store.ErrConflict):
		writeProblem(w, r, http.StatusConflict, "E_CONFLICT", "resource was modified, reload and retry", nil)
	case errors.As(err, &conflict):
		writeProblem(w, r, http.StatusConflict, "E_CONFLICT", conflict.Msg, nil)
	case errors.Is(err, service.ErrUnauthorized):
		writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "invalid email or password", nil)
	default:
		var ve *certificate.ValidationError
		var de *dnsprovider.ValidationError
		if errors.As(err, &ve) {
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", ve.Message, map[string]string{ve.Field: ve.Message})
			return
		}
		if errors.As(err, &de) {
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", de.Msg, nil)
			return
		}
		slog.Warn("request failed", "path", r.URL.Path, "error", job.Redact(err.Error()))
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_REQUEST", job.Redact(err.Error()), nil)
	}
}

func (s *Server) audit(r *http.Request, action, resourceType, resourceID, result, detail string) {
	p := principal(r)
	var actor *string
	if p != nil {
		actor = &p.User.ID
	}
	s.Svc.Audit(r.Context(), actor, action, resourceType, resourceID, result, clientIP(r), r.UserAgent(), requestID(r), detail)
}

// sessionCookie issues the session cookie.
func (s *Server) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name: s.Cfg.SessionCookieName(), Value: token, Path: "/",
		HttpOnly: true, Secure: s.Cfg.Secure(), SameSite: http.SameSiteLaxMode,
		MaxAge: int(auth.SessionTTL / time.Second),
	}
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.Cfg.SessionCookieName(), Value: "", Path: "/",
		HttpOnly: true, Secure: s.Cfg.Secure(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes == 0 {
		maxBytes = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := jsonDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// parseCursor accepts both the legacy integer cursor and the new
// "<created_at>:<id>" composite cursor.
func parseCursor(r *http.Request) (int64, string) {
	raw := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if raw == "" {
		return 0, ""
	}
	if at, id, ok := strings.Cut(raw, ":"); ok {
		n, _ := strconv.ParseInt(at, 10, 64)
		return n, id
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n, ""
}

func parseLimit(r *http.Request, def, max int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
