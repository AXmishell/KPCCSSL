package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/acme/acmesh"
	"github.com/axmishell/kpccssl/internal/dnsprovider"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

// CAInput is the admin-editable CA account payload.
type CAInput struct {
	Name         string `json:"name"`
	DirectoryURL string `json:"directory_url"`
	IsStaging    bool   `json:"is_staging"`
	Email        string `json:"email"`
	EABKid       string `json:"eab_kid"`
	EABHMAC      string `json:"eab_hmac_key"`
	TOSAccepted  bool   `json:"tos_accepted"`
}

// CAPreset is a shortcut shown in the UI.
type CAPreset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DirectoryURL string `json:"directory_url"`
	Staging      bool   `json:"staging"`
	NeedsEAB     bool   `json:"needs_eab"`
}

// CAPresets lists well-known ACME directories.
func CAPresets() []CAPreset {
	return []CAPreset{
		{ID: "letsencrypt", Name: "Let's Encrypt", DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory"},
		{ID: "letsencrypt-staging", Name: "Let's Encrypt (staging)", DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory", Staging: true},
		{ID: "zerossl", Name: "ZeroSSL", DirectoryURL: "https://acme.zerossl.com/v2/DV90", NeedsEAB: true},
		{ID: "google", Name: "Google Trust Services", DirectoryURL: "https://dv.acme-v02.api.pki.goog/directory", NeedsEAB: true},
		{ID: "sslcom", Name: "SSL.com", DirectoryURL: "https://acme.ssl.com/sslcom-dv-rsa", NeedsEAB: true},
	}
}

// CreateCA validates and stores a CA account. EAB material is encrypted.
func (s *Service) CreateCA(ctx context.Context, in CAInput) (*store.CAAccount, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if err := s.validateDirectoryURL(in.DirectoryURL); err != nil {
		return nil, err
	}
	if !in.TOSAccepted {
		return nil, errors.New("you must accept the CA terms of service")
	}
	if (in.EABKid == "") != (in.EABHMAC == "") {
		return nil, errors.New("EAB key id and HMAC must be provided together")
	}
	if in.Email != "" {
		if err := validateEmail(in.Email); err != nil {
			return nil, err
		}
	}
	now := store.Now()
	a := &store.CAAccount{
		ID: id.New(), Name: in.Name, DirectoryURL: strings.TrimSpace(in.DirectoryURL),
		IsStaging: in.IsStaging, Status: "new", KeyVersion: s.Box.Version(),
		TOSAcceptedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if in.Email != "" {
		a.Email = &in.Email
	}
	if in.EABKid != "" {
		a.EABKID = &in.EABKid
		enc, err := s.Box.Encrypt([]byte(in.EABHMAC), "ca_accounts:"+a.ID+":eab_hmac")
		if err != nil {
			return nil, err
		}
		a.EABHMACEnc = &enc
	}
	if err := s.Store.CreateCAAccount(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// UpdateCA updates mutable fields. Empty EAB fields keep the stored values.
func (s *Service) UpdateCA(ctx context.Context, id string, in CAInput) (*store.CAAccount, error) {
	a, err := s.Store.GetCAAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		a.Name = strings.TrimSpace(in.Name)
	}
	if in.DirectoryURL != "" {
		if err := s.validateDirectoryURL(in.DirectoryURL); err != nil {
			return nil, err
		}
		a.DirectoryURL = strings.TrimSpace(in.DirectoryURL)
	}
	a.IsStaging = in.IsStaging
	if in.TOSAccepted && a.TOSAcceptedAt == nil {
		now := store.Now()
		a.TOSAcceptedAt = &now
	}
	if in.Email != "" {
		if err := validateEmail(in.Email); err != nil {
			return nil, err
		}
		a.Email = &in.Email
	}
	if in.EABKid != "" || in.EABHMAC != "" {
		if in.EABKid == "" || in.EABHMAC == "" {
			return nil, errors.New("EAB key id and HMAC must be provided together")
		}
		enc, err := s.Box.Encrypt([]byte(in.EABHMAC), "ca_accounts:"+a.ID+":eab_hmac")
		if err != nil {
			return nil, err
		}
		a.EABKID, a.EABHMACEnc = &in.EABKid, &enc
		a.KeyVersion = s.Box.Version()
	}
	a.UpdatedAt = store.Now()
	if err := s.Store.UpdateCAAccount(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// RegisterCA enqueues account registration.
func (s *Service) RegisterCA(ctx context.Context, id, idem string) (*store.Job, error) {
	a, err := s.Store.GetCAAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status == "disabled" {
		return nil, errors.New("CA account is disabled")
	}
	if idem == "" {
		if existing, err := s.Store.FindActiveJobForCA(ctx, "register_account", a.ID); err == nil {
			return existing, nil
		}
	}
	j, _, err := s.Enqueue(ctx, "register_account", nil, &a.ID, map[string]string{"ca_account_id": a.ID}, idem, 3)
	return j, err
}

func (s *Service) handleRegisterAccount(ctx context.Context, j *store.Job, log io.Writer) error {
	caID := *j.CAAccountID
	a, err := s.Store.GetCAAccount(ctx, caID)
	if err != nil {
		return job.Permanent(err)
	}
	email := ""
	if a.Email != nil {
		email = *a.Email
	}
	req := acme.RegisterRequest{DirectoryURL: a.DirectoryURL, Email: email}
	if a.EABKID != nil && a.EABHMACEnc != nil {
		plain, err := s.Box.Decrypt(*a.EABHMACEnc, "ca_accounts:"+a.ID+":eab_hmac")
		if err != nil {
			return job.Permanent(fmt.Errorf("decrypt EAB: %w", err))
		}
		req.EABKid, req.EABHMAC = *a.EABKID, string(plain)
	}
	fmt.Fprintf(log, "registering ACME account %s at %s\n", a.Name, a.DirectoryURL)
	defer func() {
		if touched, err := acmesh.ScrubEAB(s.Cfg.ACMEHome); err == nil && len(touched) > 0 {
			fmt.Fprintf(log, "scrubbed EAB material from %d acme.sh config file(s)\n", len(touched))
		}
		_, _ = acmesh.ScrubSavedCredentials(s.Cfg.ACMEHome)
	}()
	if err := s.Engine.RegisterAccount(ctx, req, log); err != nil {
		_, msg := errorParts(err)
		_ = s.Store.SetCAStatus(ctx, a.ID, "error", &msg, store.Now())
		return err
	}
	_ = s.Store.SetCAStatus(ctx, a.ID, "registered", nil, store.Now())
	fmt.Fprintf(log, "account registered\n")
	s.Notify(ctx, "ca.registered", "ca:"+a.ID+":registered", "ACME account registered", a.Name+" is ready.")
	return nil
}

// DNSInput is the credential create/update payload.
type DNSInput struct {
	Name     string            `json:"name"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config"`
}

// CreateDNS validates, encrypts and stores a DNS credential.
func (s *Service) CreateDNS(ctx context.Context, in DNSInput) (*store.DNSCredential, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if err := dnsprovider.ValidateConfig(in.Provider, in.Config); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(in.Config)
	now := store.Now()
	d := &store.DNSCredential{ID: id.New(), Name: in.Name, Provider: in.Provider, Status: "unverified", KeyVersion: s.Box.Version(), CreatedAt: now, UpdatedAt: now}
	enc, err := s.Box.Encrypt(raw, "dns_credentials:"+d.ID+":config")
	if err != nil {
		return nil, err
	}
	d.ConfigEnc = enc
	if err := s.Store.CreateDNSCredential(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// UpdateDNS updates a credential. An empty Config keeps the current secret.
func (s *Service) UpdateDNS(ctx context.Context, id string, in DNSInput) (*store.DNSCredential, error) {
	d, err := s.Store.GetDNSCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		d.Name = strings.TrimSpace(in.Name)
	}
	if in.Provider != "" && in.Provider != d.Provider {
		if in.Config == nil {
			return nil, errors.New("config is required when changing provider")
		}
		d.Provider = in.Provider
	}
	if in.Config != nil {
		if err := dnsprovider.ValidateConfig(d.Provider, in.Config); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(in.Config)
		enc, err := s.Box.Encrypt(raw, "dns_credentials:"+d.ID+":config")
		if err != nil {
			return nil, err
		}
		d.ConfigEnc = enc
		d.KeyVersion = s.Box.Version()
		d.Status = "unverified"
		d.LastError = nil
	}
	d.UpdatedAt = store.Now()
	if err := s.Store.UpdateDNSCredential(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDNS refuses while a certificate references the credential.
func (s *Service) DeleteDNS(ctx context.Context, id string) error {
	n, err := s.Store.CountCertificatesUsingCredential(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("credential is used by %d certificate(s)", n)
	}
	return s.Store.DeleteDNSCredential(ctx, id)
}

// DecryptDNSConfig returns the provider environment for a credential.
func (s *Service) DecryptDNSConfig(d *store.DNSCredential) (map[string]string, error) {
	plain, err := s.Box.Decrypt(d.ConfigEnc, "dns_credentials:"+d.ID+":config")
	if err != nil {
		return nil, err
	}
	var cfg map[string]string
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// TestNotification sends a test message through a channel.
func (s *Service) TestNotification(ctx context.Context, id string) error {
	ch, err := s.Store.GetNotificationChannel(ctx, id)
	if err != nil {
		return err
	}
	return s.Notifier.Send(ctx, ch, "KPCCSSL test notification", "This is a test message sent at "+time.Now().UTC().Format(time.RFC3339)+".")
}

func (s *Service) validateDirectoryURL(raw string) error {
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return errors.New("directory URL contains invalid whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("invalid directory URL")
	}
	if u.Scheme != "https" && !(s.Cfg.Dev && u.Scheme == "http") {
		return errors.New("directory URL must use https")
	}
	return nil
}
