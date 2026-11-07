// Package service implements the application's business operations. API
// handlers validate input and call these methods; job handlers share the same
// logic.
package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/config"
	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/dnsprovider"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/notify"
	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/store"
)

// ErrUnauthorized is returned for invalid credentials.
var ErrUnauthorized = errors.New("invalid email or password")
var ErrForbidden = errors.New("forbidden")

// ConflictError is a business conflict that API handlers map to HTTP 409.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// Service is the application service container.
type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	Box      *secret.Box
	Engine   acme.Engine
	Logs     *job.Logs
	Runner   *job.Runner
	Deployer *deploy.Deployer
	Notifier *notify.Notifier
	Limiter  *auth.Limiter
	DNS      *dnsprovider.Catalog

	// MailSender delivers transactional authentication mail. It defaults to
	// notify.SendSMTP and is replaceable in tests.
	MailSender func(ctx context.Context, cfg notify.SMTPConfig, subject, body string) error

	SetupToken string

	caPresetsOnce sync.Once
	caPresets     []CAPreset
	versionMu     sync.Mutex
	acmeVersion   string
}

// New wires the service and registers job handlers.
func New(cfg *config.Config, st *store.Store, box *secret.Box, engine acme.Engine, logs *job.Logs, runner *job.Runner, dep *deploy.Deployer, notifier *notify.Notifier) (*Service, error) {
	s := &Service{
		Cfg: cfg, Store: st, Box: box, Engine: engine, Logs: logs,
		Runner: runner, Deployer: dep, Notifier: notifier,
		Limiter: auth.NewLimiter(), DNS: dnsprovider.Load(cfg.ACMEBin),
		MailSender: notify.SendSMTP,
	}
	if notifier != nil {
		notifier.SystemMail = s.MailConfig
		// Resolve the current MailSender on every call so tests can replace
		// Service.MailSender after construction.
		notifier.MailSender = func(ctx context.Context, cfg notify.SMTPConfig, subject, body string) error {
			sender := s.MailSender
			if sender == nil {
				sender = notify.SendSMTP
			}
			return sender(ctx, cfg, subject, body)
		}
	}
	token := cfg.SetupToken
	if token == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		token = base64.RawURLEncoding.EncodeToString(b)
	}
	s.SetupToken = token

	runner.Handle("register_account", s.handleRegisterAccount)
	runner.Handle("issue", s.handleIssue)
	runner.Handle("renew", s.handleRenew)
	runner.Handle("revoke", s.handleRevoke)
	runner.Handle("deploy", s.handleDeploy)
	runner.Handle("sync", s.handleSync)
	return s, nil
}

// SetupTokenLogValue returns the token for the startup log.
func (s *Service) SetupTokenLogValue() string { return s.SetupToken }

// ACMEVersion returns the acme.sh version, caching a successful result. A
// failed probe is retried on the next call.
func (s *Service) ACMEVersion(ctx context.Context) (string, error) {
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if s.acmeVersion != "" {
		return s.acmeVersion, nil
	}
	v, err := s.Engine.Version(ctx)
	if err == nil {
		s.acmeVersion = v
	}
	return v, err
}

// NeedsSetup reports whether no user exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.Store.CountUsers(ctx)
	return n == 0, err
}

// Setup creates the first administrator. It is only allowed while the users
// table is empty and requires the setup token.
func (s *Service) Setup(ctx context.Context, token, email, password string) (*store.User, error) {
	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}
	if !needs {
		return nil, errors.New("instance already initialized")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.SetupToken)) != 1 {
		return nil, errors.New("invalid setup token")
	}
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if err := auth.ValidatePasswordPolicy(password, email); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := store.Now()
	u := &store.User{ID: id.New(), Email: normalizeEmail(email), PasswordHash: hash, Role: "admin", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(s.Cfg.DataDir, "setup-token"))
	return u, nil
}

// Authenticate verifies credentials and applies per-IP / per-account limits.
func (s *Service) Authenticate(ctx context.Context, email, password, ip string) (*store.User, error) {
	email = normalizeEmail(email)
	if ok, wait := s.Limiter.Allow("login:ip:"+ip, 10, 15*time.Minute); !ok {
		return nil, fmt.Errorf("too many attempts, retry in %s", wait.Round(time.Second))
	}
	if ok, wait := s.Limiter.Allow("login:acct:"+email, 5, 15*time.Minute); !ok {
		return nil, fmt.Errorf("too many attempts, retry in %s", wait.Round(time.Second))
	}
	u, err := s.Store.GetUserByEmail(ctx, email)
	if err != nil {
		// Spend comparable time to avoid user enumeration.
		auth.VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return nil, ErrUnauthorized
	}
	if u.Disabled || !auth.VerifyPassword(u.PasswordHash, password) {
		return nil, ErrUnauthorized
	}
	return u, nil
}

// ResetLoginLimits clears throttling after a fully successful authentication
// (including the second factor).
func (s *Service) ResetLoginLimits(email, ip string) {
	s.Limiter.Reset("login:ip:" + ip)
	s.Limiter.Reset("login:acct:" + normalizeEmail(email))
}

// Audit appends an audit entry.
func (s *Service) Audit(ctx context.Context, actorID *string, action, resourceType, resourceID, result, ip, ua, requestID, detail string) {
	entry := &store.AuditLog{
		ID: id.New(), ActorID: actorID, Action: action, ResourceType: resourceType,
		Result: result, CreatedAt: store.Now(),
	}
	if resourceID != "" {
		entry.ResourceID = &resourceID
	}
	if ip != "" {
		entry.IP = &ip
	}
	if ua != "" {
		entry.UserAgent = &ua
	}
	if requestID != "" {
		entry.RequestID = &requestID
	}
	if detail != "" {
		detail = job.Redact(detail)
		entry.DetailJSON = &detail
	}
	if err := s.Store.InsertAudit(ctx, entry); err != nil {
		slog.Error("audit insert failed", "error", err)
	}
}

// Enqueue creates a durable job and records its log path.
func (s *Service) Enqueue(ctx context.Context, jobType string, certID, caID *string, payload any, idempotencyKey string, maxAttempts int) (*store.Job, bool, error) {
	return s.EnqueueAt(ctx, jobType, certID, caID, payload, idempotencyKey, maxAttempts, 0)
}

// EnqueueAt schedules a job, optionally with a delay for jitter/backoff.
func (s *Service) EnqueueAt(ctx context.Context, jobType string, certID, caID *string, payload any, idempotencyKey string, maxAttempts int, delay time.Duration) (*store.Job, bool, error) {
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts(jobType)
	}
	// Collapse duplicate in-flight jobs for the same certificate. Deploy jobs
	// are per-target and must not collapse together.
	if certID != nil && idempotencyKey == "" && jobType != "deploy" {
		if existing, err := s.Store.FindActiveJob(ctx, jobType, *certID); err == nil {
			return existing, false, nil
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	now := store.Now()
	j := &store.Job{
		ID: id.New(), Type: jobType, CertificateID: certID, CAAccountID: caID,
		Status: "queued", MaxAttempts: maxAttempts, PayloadJSON: string(raw),
		CreatedAt: now, AvailableAt: now + int64(delay/time.Second),
	}
	if idempotencyKey != "" {
		j.IdempotencyKey = &idempotencyKey
	}
	created, err := s.Store.CreateJob(ctx, j)
	if err != nil {
		return nil, false, err
	}
	if created {
		_ = s.Store.SetJobLogPath(ctx, j.ID, s.Logs.Path(j.ID))
	}
	return j, created, nil
}

func defaultMaxAttempts(jobType string) int {
	switch jobType {
	case "renew":
		return 5
	case "revoke":
		return 1
	default:
		return 3
	}
}

// CancelJob cancels a queued job and repairs the certificate state if it was
// waiting on that job.
func (s *Service) CancelJob(ctx context.Context, id string) error {
	if err := s.Store.CancelJob(ctx, id, store.Now()); err != nil {
		return err
	}
	_, _ = s.Store.RecoverInterruptedCertificates(ctx, store.Now())
	return nil
}

// RetryJob requeues a terminal job.
func (s *Service) RetryJob(ctx context.Context, id string) error {
	return s.Store.RetryJob(ctx, id, store.Now())
}

// AddEvent writes a certificate timeline entry.
func (s *Service) AddEvent(ctx context.Context, certID, typ, msg, jobID string) {
	if certID == "" {
		slog.Info("certificate event without certificate", "type", typ, "message", msg)
		return
	}
	e := &store.Event{CertificateID: certID, Type: typ, CreatedAt: store.Now()}
	if msg != "" {
		e.Message = &msg
	}
	if jobID != "" {
		e.JobID = &jobID
	}
	if err := s.Store.AddEvent(ctx, e); err != nil {
		slog.Error("certificate event insert failed", "error", err)
	}
}

// Notify sends a lifecycle notification. Callers that treat notifications as
// best-effort may ignore the returned error; workflow notify steps surface it so
// continue_on_error can decide the flow.
func (s *Service) Notify(ctx context.Context, eventType, key, title, body string) error {
	if s.Notifier == nil {
		return nil
	}
	if err := s.Notifier.Dispatch(ctx, notify.Event{Type: eventType, Key: key, Title: title, Body: body}); err != nil {
		slog.Warn("notification failed", "event", eventType, "error", err)
		return err
	}
	return nil
}

func validateEmail(email string) error {
	at := len(email) / 2
	for i, r := range email {
		if r == '@' {
			at = i
			break
		}
	}
	if at <= 0 || at == len(email)-1 || len(email) > 254 {
		return errors.New("invalid email address")
	}
	for _, r := range email {
		if r <= ' ' || r == '<' || r == '>' || r == '"' {
			return errors.New("invalid email address")
		}
	}
	return nil
}

func normalizeEmail(email string) string {
	out := make([]rune, 0, len(email))
	for _, r := range email {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

// errorParts extracts the stable code and human message from an error.
func errorParts(err error) (string, string) {
	var ae *acme.Error
	if errors.As(err, &ae) {
		return ae.Code, job.Redact(ae.Message)
	}
	return acme.CodeUnknown, job.Redact(err.Error())
}
