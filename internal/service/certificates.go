package service

import (
	"archive/zip"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/acme/acmesh"
	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
	"software.sslmate.com/src/go-pkcs12"
)

// CertInput is the desired certificate configuration.
type CertInput struct {
	Name            string        `json:"name"`
	PrimaryDomain   string        `json:"primary_domain"`
	SAN             []string      `json:"san"`
	KeyType         string        `json:"key_type"`
	CAAccountID     string        `json:"ca_account_id"`
	ChallengeType   string        `json:"challenge_type"`
	DNSCredentialID string        `json:"dns_credential_id"`
	Webroot         string        `json:"webroot"`
	Options         store.Options `json:"options"`
	AutoRenew       bool          `json:"auto_renew"`
	Version         int           `json:"version"`
}

// CreateCertificate validates and stores a draft certificate.
func (s *Service) CreateCertificate(ctx context.Context, in CertInput) (*store.Certificate, error) {
	c, err := s.buildCertificate(ctx, in, "")
	if err != nil {
		return nil, err
	}
	now := store.Now()
	c.ID = id.New()
	c.Status = "draft"
	c.Version = 1
	c.CreatedAt, c.UpdatedAt = now, now
	if err := s.Store.CreateCertificate(ctx, c); err != nil {
		return nil, err
	}
	s.AddEvent(ctx, c.ID, "created", "certificate draft created", "")
	return c, nil
}

// UpdateCertificate applies desired-config changes with optimistic locking.
func (s *Service) UpdateCertificate(ctx context.Context, id string, in CertInput) (*store.Certificate, error) {
	existing, err := s.Store.GetCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Version != 0 && in.Version != existing.Version {
		return nil, store.ErrConflict
	}
	c, err := s.buildCertificate(ctx, in, id)
	if err != nil {
		return nil, err
	}
	c.ID = id
	c.Version = existing.Version
	if in.Version == 0 {
		c.Version = existing.Version
	}
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = store.Now()
	if err := s.Store.UpdateCertificate(ctx, c); err != nil {
		return nil, err
	}
	s.AddEvent(ctx, id, "updated", "desired configuration updated", "")
	return s.Store.GetCertificate(ctx, id)
}

func (s *Service) buildCertificate(ctx context.Context, in CertInput, certID string) (*store.Certificate, error) {
	primary, sans, err := certificate.NormalizeNames(in.PrimaryDomain, in.SAN)
	if err != nil {
		return nil, err
	}
	if err := certificate.ValidateKeyType(in.KeyType); err != nil {
		return nil, err
	}
	if err := certificate.ValidateChallenge(in.ChallengeType, sans); err != nil {
		return nil, err
	}
	ca, err := s.Store.GetCAAccount(ctx, in.CAAccountID)
	if err != nil {
		return nil, fmt.Errorf("CA account: %w", err)
	}
	if ca.Status == "disabled" {
		return nil, errors.New("selected CA account is disabled")
	}
	c := &store.Certificate{
		Name: strings.TrimSpace(in.Name), PrimaryDomain: primary, SAN: sans,
		KeyType: in.KeyType, CAAccountID: in.CAAccountID, ChallengeType: in.ChallengeType,
		Options: in.Options, AutoRenew: in.AutoRenew,
	}
	if len(c.Name) > 100 || strings.ContainsAny(c.Name, "\r\n\x00") {
		return nil, errors.New("invalid certificate name")
	}
	if c.Name == "" {
		c.Name = primary
	}
	if c.Options.DNSSleep < 0 || c.Options.DNSSleep > 3600 {
		return nil, errors.New("dns_sleep must be between 0 and 3600")
	}
	if c.Options.PreferredChain != "" && (len(c.Options.PreferredChain) > 128 || strings.ContainsAny(c.Options.PreferredChain, "\r\n\x00")) {
		return nil, errors.New("invalid preferred_chain")
	}
	if c.Options.DNSAlias != "" {
		alias, err := certificate.NormalizeDomain(c.Options.DNSAlias)
		if err != nil || strings.HasPrefix(alias, "*.") {
			return nil, errors.New("invalid dns_alias: must be a non-wildcard domain")
		}
		c.Options.DNSAlias = alias
	}
	if in.ChallengeType == "dns-01" {
		if in.DNSCredentialID == "" {
			return nil, errors.New("DNS credential is required for DNS-01")
		}
		cred, err := s.Store.GetDNSCredential(ctx, in.DNSCredentialID)
		if err != nil {
			return nil, fmt.Errorf("DNS credential: %w", err)
		}
		c.DNSCredentialID = &cred.ID
	}
	if in.ChallengeType == "http-01-webroot" {
		if in.Webroot == "" {
			return nil, errors.New("webroot is required for HTTP-01 webroot")
		}
		root, err := s.validatePath(in.Webroot)
		if err != nil {
			return nil, fmt.Errorf("webroot: %w", err)
		}
		if err := s.withinRoots(root, s.Cfg.WebrootRoots); err != nil {
			return nil, fmt.Errorf("webroot: %w", err)
		}
		c.Webroot = &root
		if st, err := os.Stat(filepath.Join(root, ".well-known")); err == nil && !st.IsDir() {
			return nil, errors.New("webroot .well-known is not a directory")
		}
	}
	return c, nil
}

// validatePath resolves symlinks and rejects control characters and traversal.
func (s *Service) validatePath(p string) (string, error) {
	if strings.ContainsAny(p, "\r\n\x00") {
		return "", errors.New("invalid path")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	clean := filepath.Clean(p)
	if clean != filepath.Clean(p) || strings.Contains(clean, "..") {
		return "", errors.New("path must not contain '..'")
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("path does not exist")
	}
	return resolved, nil
}

// withinRoots verifies a resolved path is inside one of the allowed roots.
func (s *Service) withinRoots(path string, roots []string) error {
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(r); err == nil {
			r = real
		}
		rel, err := filepath.Rel(r, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return errors.New("path is not inside an allowed root")
}

// Issue enqueues a certificate issuance job.
func (s *Service) Issue(ctx context.Context, certID, idem string, force bool) (*store.Job, error) {
	c, err := s.Store.GetCertificate(ctx, certID)
	if err != nil {
		return nil, err
	}
	if idem != "" {
		if existing, err := s.Store.GetJobByIdempotencyKey(ctx, idem); err == nil {
			if existing.CertificateID != nil && *existing.CertificateID == certID {
				return existing, nil
			}
			return nil, errors.New("idempotency key is already used for another certificate")
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if c.Status == "revoked" {
		return nil, errors.New("certificate is revoked")
	}
	if c.Status == "queued" || c.Status == "issuing" || c.Status == "renewing" {
		return nil, errors.New("another operation is in progress")
	}
	ca, err := s.Store.GetCAAccount(ctx, c.CAAccountID)
	if err != nil {
		return nil, err
	}
	if ca.Status != "registered" {
		return nil, errors.New("CA account is not registered yet")
	}
	if err := certificate.ValidateChallenge(c.ChallengeType, c.SAN); err != nil {
		return nil, err
	}
	key := certificate.RateLimitKey(c.PrimaryDomain)
	if ok, wait := s.Limiter.Allow("issue:"+key, 5, time.Hour); !ok {
		return nil, fmt.Errorf("issuance rate limit reached for %s, retry in %s", key, wait.Round(time.Minute))
	}
	j, created, err := s.Enqueue(ctx, "issue", &c.ID, &ca.ID, map[string]any{"force": force}, idem, 3)
	if err != nil {
		return nil, err
	}
	if created {
		_ = s.Store.SetCertificateStatus(ctx, c.ID, "queued", nil, nil, store.Now())
		s.AddEvent(ctx, c.ID, "queued", "issuance queued", j.ID)
	}
	return j, nil
}

// Renew enqueues a renewal job.
func (s *Service) Renew(ctx context.Context, certID, idem string, force bool) (*store.Job, error) {
	c, err := s.Store.GetCertificate(ctx, certID)
	if err != nil {
		return nil, err
	}
	if idem != "" {
		if existing, err := s.Store.GetJobByIdempotencyKey(ctx, idem); err == nil {
			if existing.CertificateID != nil && *existing.CertificateID == certID {
				return existing, nil
			}
			return nil, errors.New("idempotency key is already used for another certificate")
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if c.Status == "revoked" || c.Status == "disabled" {
		return nil, errors.New("certificate cannot be renewed in its current state")
	}
	if c.Status == "queued" || c.Status == "issuing" || c.Status == "renewing" {
		return nil, errors.New("another operation is in progress")
	}
	j, created, err := s.Enqueue(ctx, "renew", &c.ID, &c.CAAccountID, map[string]any{"force": force}, idem, 5)
	if err != nil {
		return nil, err
	}
	if created {
		_ = s.Store.SetCertificateStatus(ctx, c.ID, "renewing", nil, nil, store.Now())
		s.AddEvent(ctx, c.ID, "renew_queued", "renewal queued", j.ID)
	}
	return j, nil
}

// Revoke enqueues a revocation job.
func (s *Service) Revoke(ctx context.Context, certID, idem string, reason int) (*store.Job, error) {
	c, err := s.Store.GetCertificate(ctx, certID)
	if err != nil {
		return nil, err
	}
	if idem != "" {
		if existing, err := s.Store.GetJobByIdempotencyKey(ctx, idem); err == nil {
			if existing.CertificateID != nil && *existing.CertificateID == certID {
				return existing, nil
			}
			return nil, errors.New("idempotency key is already used for another certificate")
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if c.CurrentRevisionID == nil {
		return nil, errors.New("certificate has no issued revision to revoke")
	}
	if c.Status == "revoked" {
		return nil, errors.New("certificate is already revoked")
	}
	if reason < 0 || reason > 10 {
		return nil, errors.New("revocation reason must be 0-10")
	}
	j, created, err := s.Enqueue(ctx, "revoke", &c.ID, &c.CAAccountID, map[string]any{"reason": reason}, idem, 1)
	if err != nil {
		return nil, err
	}
	if created {
		s.AddEvent(ctx, c.ID, "revoke_queued", fmt.Sprintf("revocation queued (reason %d)", reason), j.ID)
	}
	return j, nil
}

// SoftDeleteCertificate stops renewals but keeps files and history.
func (s *Service) SoftDeleteCertificate(ctx context.Context, certID string) error {
	err := s.Store.SoftDeleteCertificate(ctx, certID, store.Now())
	if err == nil {
		s.AddEvent(ctx, certID, "deleted", "certificate soft-deleted", "")
	}
	return err
}

func (s *Service) handleIssue(ctx context.Context, j *store.Job, log io.Writer) error {
	defer os.RemoveAll(filepath.Join(s.Cfg.DataDir, "tmp", "jobs", j.ID))
	c, err := s.Store.GetCertificate(ctx, *j.CertificateID)
	if err != nil {
		return job.Permanent(err)
	}
	prevStatus := c.Status
	var payload struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal([]byte(j.PayloadJSON), &payload)
	force := payload.Force || c.CurrentRevisionID != nil
	if c.Status != "active" {
		_ = s.Store.SetCertificateStatus(ctx, c.ID, "issuing", nil, nil, store.Now())
	}
	s.AddEvent(ctx, c.ID, "issuing", "issuance started", j.ID)

	paths, err := s.runIssue(ctx, c, j, force, log)
	if err != nil {
		code, msg := errorParts(err)
		status := "failed"
		if prevStatus == "active" {
			status = "active"
		}
		_ = s.Store.SetCertificateStatus(ctx, c.ID, status, &code, &msg, store.Now())
		if c.DNSCredentialID != nil && (code == acme.CodeDNSAuth || code == acme.CodeDNSPropagation) {
			_ = s.Store.SetDNSCredentialHealth(ctx, *c.DNSCredentialID, "error", &msg, nil, store.Now())
		}
		s.AddEvent(ctx, c.ID, "issue_failed", code+": "+msg, j.ID)
		s.Notify(ctx, "certificate.failed", "cert:"+c.ID+":"+j.ID, "Certificate issuance failed", c.PrimaryDomain+"\n"+code+": "+msg)
		return err
	}
	rev, err := s.finalize(ctx, c, paths, j, log)
	if err != nil {
		code, msg := errorParts(err)
		_ = s.Store.SetCertificateStatus(ctx, c.ID, "failed", &code, &msg, store.Now())
		s.AddEvent(ctx, c.ID, "issue_failed", code+": "+msg, j.ID)
		return err
	}
	s.AddEvent(ctx, c.ID, "issued", fmt.Sprintf("certificate issued, expires %s", time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339)), j.ID)
	s.Notify(ctx, "certificate.issued", "cert:"+c.ID+":"+rev.ID, "Certificate issued", c.PrimaryDomain+"\nValid until "+time.Unix(rev.NotAfter, 0).UTC().Format("2006-01-02")+".")
	s.enqueueDeployments(ctx, c.ID, rev.ID)
	return nil
}

func (s *Service) runIssue(ctx context.Context, c *store.Certificate, j *store.Job, force bool, log io.Writer) (acme.ExportPaths, error) {
	ca, err := s.Store.GetCAAccount(ctx, c.CAAccountID)
	if err != nil {
		return acme.ExportPaths{}, job.Permanent(err)
	}
	plugin, env, err := s.certEnv(ctx, c)
	if err != nil {
		return acme.ExportPaths{}, job.Permanent(err)
	}
	jobDir := filepath.Join(s.Cfg.DataDir, "tmp", "jobs", j.ID)
	stage := filepath.Join(jobDir, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return acme.ExportPaths{}, err
	}
	email := ""
	if ca.Email != nil {
		email = *ca.Email
	}
	accountConf, err := acmesh.WriteAccountConf(jobDir, email)
	if err != nil {
		return acme.ExportPaths{}, err
	}
	defer func() {
		_ = acmesh.RemoveAccountConf(accountConf)
		if touched, err := acmesh.ScrubSavedCredentials(s.Cfg.ACMEHome); err == nil && len(touched) > 0 {
			fmt.Fprintf(log, "scrubbed SAVED_* credentials from %d acme.sh config file(s)\n", len(touched))
		}
	}()

	req := acme.IssueRequest{
		Domains: c.SAN, KeyType: c.KeyType, Challenge: c.ChallengeType,
		DNSProvider: plugin, Env: env, DirectoryURL: ca.DirectoryURL,
		DNSSleep: c.Options.DNSSleep, PreferredChain: c.Options.PreferredChain,
		DNSAlias: c.Options.DNSAlias, AlwaysNewKey: c.Options.AlwaysNewKey,
		MustStaple: c.Options.MustStaple, Force: force, AccountConf: accountConf,
		HTTPPort: s.Cfg.HTTPPort, TLSPort: s.Cfg.TLSPort,
	}
	if c.Webroot != nil {
		req.Webroot = *c.Webroot
	}
	fmt.Fprintf(log, "issuing %s via %s\n", strings.Join(c.SAN, ", "), c.ChallengeType)
	return s.Engine.Issue(ctx, req, stage, log)
}

func (s *Service) handleRenew(ctx context.Context, j *store.Job, log io.Writer) error {
	defer os.RemoveAll(filepath.Join(s.Cfg.DataDir, "tmp", "jobs", j.ID))
	c, err := s.Store.GetCertificate(ctx, *j.CertificateID)
	if err != nil {
		return job.Permanent(err)
	}
	var payload struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal([]byte(j.PayloadJSON), &payload)
	prevStatus := c.Status
	if prevStatus != "active" {
		_ = s.Store.SetCertificateStatus(ctx, c.ID, "renewing", nil, nil, store.Now())
	}
	s.AddEvent(ctx, c.ID, "renewing", "renewal started", j.ID)
	paths, err := s.runRenew(ctx, c, j, payload.Force, log)
	if errors.Is(err, acmesh.ErrSkipped) {
		fmt.Fprintf(log, "acme.sh reports the certificate is not due; scheduling another check in 24h\n")
		now := store.Now()
		_ = s.Store.SetNextRenew(ctx, c.ID, now+86400, now)
		if c.CurrentRevisionID != nil {
			_ = s.Store.SetCertificateStatus(ctx, c.ID, "active", nil, nil, now)
		}
		s.AddEvent(ctx, c.ID, "renew_skipped", "CA reports certificate is not due", j.ID)
		return nil
	}
	if err != nil {
		code, msg := errorParts(err)
		status := "failed"
		if prevStatus == "active" {
			status = "active"
		}
		_ = s.Store.SetCertificateStatus(ctx, c.ID, status, &code, &msg, store.Now())
		_ = s.Store.SetNextRenew(ctx, c.ID, store.Now()+6*3600, store.Now())
		s.AddEvent(ctx, c.ID, "renew_failed", code+": "+msg, j.ID)
		s.Notify(ctx, "certificate.renew_failed", "cert:"+c.ID+":"+j.ID, "Certificate renewal failed", c.PrimaryDomain+"\n"+code+": "+msg)
		return err
	}
	rev, err := s.finalize(ctx, c, paths, j, log)
	if err != nil {
		code, msg := errorParts(err)
		status := "failed"
		if prevStatus == "active" {
			status = "active"
		}
		_ = s.Store.SetCertificateStatus(ctx, c.ID, status, &code, &msg, store.Now())
		_ = s.Store.SetNextRenew(ctx, c.ID, store.Now()+6*3600, store.Now())
		s.AddEvent(ctx, c.ID, "renew_failed", code+": "+msg, j.ID)
		return err
	}
	s.AddEvent(ctx, c.ID, "renewed", fmt.Sprintf("renewed, expires %s", time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339)), j.ID)
	s.Notify(ctx, "certificate.renewed", "cert:"+c.ID+":"+rev.ID, "Certificate renewed", c.PrimaryDomain+"\nValid until "+time.Unix(rev.NotAfter, 0).UTC().Format("2006-01-02")+".")
	s.enqueueDeployments(ctx, c.ID, rev.ID)
	return nil
}

func (s *Service) runRenew(ctx context.Context, c *store.Certificate, j *store.Job, force bool, log io.Writer) (acme.ExportPaths, error) {
	_, env, err := s.certEnv(ctx, c)
	if err != nil {
		return acme.ExportPaths{}, job.Permanent(err)
	}
	jobDir := filepath.Join(s.Cfg.DataDir, "tmp", "jobs", j.ID)
	stage := filepath.Join(jobDir, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return acme.ExportPaths{}, err
	}
	accountConf, err := acmesh.WriteAccountConf(jobDir, "")
	if err != nil {
		return acme.ExportPaths{}, err
	}
	defer func() {
		_ = acmesh.RemoveAccountConf(accountConf)
		if touched, err := acmesh.ScrubSavedCredentials(s.Cfg.ACMEHome); err == nil && len(touched) > 0 {
			fmt.Fprintf(log, "scrubbed SAVED_* credentials from %d acme.sh config file(s)\n", len(touched))
		}
	}()
	req := acme.RenewRequest{
		Domain: c.PrimaryDomain, KeyType: c.KeyType, Env: env, Force: force,
		Challenge: c.ChallengeType, DNSSleep: c.Options.DNSSleep, AccountConf: accountConf,
		HTTPPort: s.Cfg.HTTPPort, TLSPort: s.Cfg.TLSPort,
	}
	if c.Webroot != nil {
		req.Webroot = *c.Webroot
	}
	fmt.Fprintf(log, "renewing %s\n", c.PrimaryDomain)
	return s.Engine.Renew(ctx, req, stage, log)
}

// finalize moves exported files into an immutable revision directory, parses
// the certificate and activates it.
func (s *Service) finalize(ctx context.Context, c *store.Certificate, paths acme.ExportPaths, j *store.Job, log io.Writer) (*store.Revision, error) {
	revID := id.New()
	dir := filepath.Join(s.Cfg.CertDir, c.ID, "revisions", revID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	final := acme.ExportPaths{
		KeyPath:       filepath.Join(dir, "privkey.pem"),
		CertPath:      filepath.Join(dir, "cert.pem"),
		ChainPath:     filepath.Join(dir, "chain.pem"),
		FullchainPath: filepath.Join(dir, "fullchain.pem"),
	}
	moves := []struct{ from, to string }{
		{paths.KeyPath, final.KeyPath},
		{paths.CertPath, final.CertPath},
		{paths.ChainPath, final.ChainPath},
		{paths.FullchainPath, final.FullchainPath},
	}
	for _, m := range moves {
		if err := os.Rename(m.from, m.to); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("move exported certificate: %w", err)
		}
	}
	info, parsed, err := certificate.ParsePEMCertificate(final.CertPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if !certificate.NamesMatch(c.SAN, info.DNSNames) {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("issued certificate SAN %v does not cover requested names %v", info.DNSNames, c.SAN)
	}
	if err := certificate.VerifyKeyMatches(parsed, final.KeyPath); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Chmod(final.KeyPath, 0o600); err != nil {
		return nil, err
	}
	// Switch the current symlink atomically.
	link := filepath.Join(s.Cfg.CertDir, c.ID, "current")
	tmpLink := link + ".tmp"
	_ = os.Remove(tmpLink)
	if err := os.Symlink(filepath.Join("revisions", revID), tmpLink); err == nil {
		_ = os.Rename(tmpLink, link)
	}
	now := store.Now()
	rev := &store.Revision{
		ID: revID, CertificateID: c.ID, Serial: info.Serial, FingerprintSHA256: info.FingerprintSHA256,
		NotBefore: info.NotBefore, NotAfter: info.NotAfter,
		CertPath: final.CertPath, KeyPath: final.KeyPath, ChainPath: final.ChainPath, FullchainPath: final.FullchainPath,
		IssuedAt: now, CreatedAt: now,
	}
	if err := s.Store.CreateRevision(ctx, rev); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	nextRenew := certificate.RenewalTime(info.NotAfter, time.Now())
	if err := s.Store.MarkIssued(ctx, c.ID, rev.ID, nextRenew, now); err != nil {
		return nil, err
	}
	if c.DNSCredentialID != nil {
		_ = s.Store.SetDNSCredentialHealth(ctx, *c.DNSCredentialID, "ok", nil, &now, now)
	}
	fmt.Fprintf(log, "installed revision %s, expires %s\n", rev.ID, time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339))
	return rev, nil
}

func (s *Service) handleRevoke(ctx context.Context, j *store.Job, log io.Writer) error {
	c, err := s.Store.GetCertificate(ctx, *j.CertificateID)
	if err != nil {
		return job.Permanent(err)
	}
	if c.CurrentRevisionID == nil {
		return job.Permanent(errors.New("no current revision"))
	}
	rev, err := s.Store.GetRevision(ctx, *c.CurrentRevisionID)
	if err != nil {
		return job.Permanent(err)
	}
	var payload struct {
		Reason int `json:"reason"`
	}
	_ = json.Unmarshal([]byte(j.PayloadJSON), &payload)
	env, err := s.certEnvForRevoke(ctx, c)
	if err != nil {
		return job.Permanent(err)
	}
	req := acme.RevokeRequest{Domain: c.PrimaryDomain, KeyType: c.KeyType, Reason: payload.Reason, Env: env, RevokeCertPath: rev.CertPath}
	if err := s.Engine.Revoke(ctx, req, log); err != nil {
		code, msg := errorParts(err)
		s.AddEvent(ctx, c.ID, "revoke_failed", code+": "+msg, j.ID)
		return err
	}
	now := store.Now()
	_ = s.Store.MarkRevisionRevoked(ctx, rev.ID, int64(payload.Reason), now)
	if err := s.Store.MarkRevoked(ctx, c.ID, now); err != nil {
		return err
	}
	s.AddEvent(ctx, c.ID, "revoked", fmt.Sprintf("certificate revoked (reason %d)", payload.Reason), j.ID)
	s.Notify(ctx, "certificate.revoked", "cert:"+c.ID+":"+rev.ID, "Certificate revoked", c.PrimaryDomain+" was revoked.")
	return nil
}

func (s *Service) certEnv(ctx context.Context, c *store.Certificate) (string, map[string]string, error) {
	if c.ChallengeType != "dns-01" || c.DNSCredentialID == nil {
		return "", nil, nil
	}
	cred, err := s.Store.GetDNSCredential(ctx, *c.DNSCredentialID)
	if err != nil {
		return "", nil, err
	}
	env, err := s.DecryptDNSConfig(cred)
	if err != nil {
		return "", nil, err
	}
	return cred.Provider, env, nil
}

func (s *Service) certEnvForRevoke(ctx context.Context, c *store.Certificate) (map[string]string, error) {
	_, env, err := s.certEnv(ctx, c)
	return env, err
}

// RevisionFiles returns the current (or specified) revision for downloads.
func (s *Service) RevisionFiles(ctx context.Context, certID, revisionID string) (*store.Revision, error) {
	c, err := s.Store.GetCertificate(ctx, certID)
	if err != nil {
		return nil, err
	}
	if revisionID == "" {
		if c.CurrentRevisionID == nil {
			return nil, errors.New("certificate has no issued revision")
		}
		revisionID = *c.CurrentRevisionID
	}
	rev, err := s.Store.GetRevision(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if rev.CertificateID != certID {
		return nil, errors.New("revision does not belong to certificate")
	}
	return rev, nil
}

// WriteZIP streams certificate material as a ZIP archive.
func (s *Service) WriteZIP(w io.Writer, rev *store.Revision) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	files := map[string]string{
		"privkey.pem":   rev.KeyPath,
		"cert.pem":      rev.CertPath,
		"chain.pem":     rev.ChainPath,
		"fullchain.pem": rev.FullchainPath,
	}
	for name, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// WritePFX streams a PKCS#12 bundle protected by password.
func (s *Service) WritePFX(w io.Writer, rev *store.Revision, password string) error {
	if len(password) < 8 {
		return errors.New("PFX password must be at least 8 characters")
	}
	certPEM, err := os.ReadFile(rev.CertPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(rev.KeyPath)
	if err != nil {
		return err
	}
	chainPEM, _ := os.ReadFile(rev.ChainPath)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return errors.New("invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return errors.New("invalid key PEM")
	}
	var key any
	switch keyBlock.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(keyBlock.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	default:
		err = fmt.Errorf("unsupported key type %s", keyBlock.Type)
	}
	if err != nil {
		return err
	}
	var chain []*x509.Certificate
	rest := chainPEM
	for {
		b, r := pem.Decode(rest)
		if b == nil {
			break
		}
		rest = r
		if b.Type == "CERTIFICATE" {
			if c, err := x509.ParseCertificate(b.Bytes); err == nil {
				chain = append(chain, c)
			}
		}
	}
	pfx, err := pkcs12.Modern.Encode(key, cert, chain, password)
	if err != nil {
		return err
	}
	_, err = w.Write(pfx)
	return err
}

// ReadPrivateKeyFile returns the raw key for the step-up download endpoint.
func (s *Service) ReadPrivateKeyFile(rev *store.Revision) ([]byte, error) {
	return os.ReadFile(rev.KeyPath)
}

func (s *Service) enqueueDeployments(ctx context.Context, certID, revisionID string) {
	deps, err := s.Store.ListDeployments(ctx, certID)
	if err != nil {
		return
	}
	for _, d := range deps {
		if !d.Enabled {
			continue
		}
		key := "auto-deploy:" + d.ID + ":" + revisionID
		_, _, _ = s.Enqueue(ctx, "deploy", &certID, nil, map[string]string{"deployment_id": d.ID, "revision_id": revisionID}, key, 3)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
