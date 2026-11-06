package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/acme/acmesh"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

// CAInput is the admin-editable CA account payload.
type CAInput struct {
	Name         string `json:"name"`
	DirectoryURL string `json:"directory_url"`
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
	NeedsEAB     bool   `json:"needs_eab"`
}

// caPresetOrder keeps the UI stable even when acme.sh reorders its list.
var caPresetOrder = map[string]int{
	"letsencrypt": 0,
	"zerossl":     1,
	"sslcom":      2,
	"sslcom-ecc":  3,
	"google":      4,
	"actalis":     5,
}

// CAPresets lists ACME directories. The bundled acme.sh script is the source
// of truth; the built-in list keeps the UI usable when acme.sh is absent
// (for example with the fake engine during development).
func (s *Service) CAPresets() []CAPreset {
	s.caPresetsOnce.Do(func() {
		s.caPresets = fallbackCAPresets()
		discovered, err := acmesh.DiscoverCAPresets(s.Cfg.ACMEBin)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("cannot read CA presets from acme.sh; using built-in list", "path", s.Cfg.ACMEBin, "error", err)
			}
			return
		}
		if len(discovered) == 0 {
			return
		}
		s.caPresets = make([]CAPreset, 0, len(discovered))
		for _, preset := range discovered {
			s.caPresets = append(s.caPresets, CAPreset{
				ID: preset.ID, Name: preset.Name, DirectoryURL: preset.DirectoryURL,
				NeedsEAB: preset.NeedsEAB,
			})
		}
		sortCAPresets(s.caPresets)
	})
	return s.caPresets
}

func fallbackCAPresets() []CAPreset {
	return []CAPreset{
		{ID: "letsencrypt", Name: "Let's Encrypt", DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory"},
		{ID: "zerossl", Name: "ZeroSSL", DirectoryURL: "https://acme.zerossl.com/v2/DV90", NeedsEAB: true},
		{ID: "sslcom", Name: "SSL.com (RSA)", DirectoryURL: "https://acme.ssl.com/sslcom-dv-rsa", NeedsEAB: true},
		{ID: "sslcom-ecc", Name: "SSL.com (ECC)", DirectoryURL: "https://acme.ssl.com/sslcom-dv-ecc", NeedsEAB: true},
		{ID: "google", Name: "Google Trust Services", DirectoryURL: "https://dv.acme-v02.api.pki.goog/directory", NeedsEAB: true},
		{ID: "actalis", Name: "Actalis", DirectoryURL: "https://acme-api.actalis.com/acme/directory", NeedsEAB: true},
	}
}

func sortCAPresets(presets []CAPreset) {
	rank := func(id string) int {
		if r, ok := caPresetOrder[id]; ok {
			return r
		}
		return len(caPresetOrder)
	}
	sort.SliceStable(presets, func(i, j int) bool {
		ri, rj := rank(presets[i].ID), rank(presets[j].ID)
		if ri != rj {
			return ri < rj
		}
		return presets[i].Name < presets[j].Name
	})
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
		Status: "new", KeyVersion: s.Box.Version(),
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
	directoryChanged := false
	if in.DirectoryURL != "" {
		if err := s.validateDirectoryURL(in.DirectoryURL); err != nil {
			return nil, err
		}
		next := strings.TrimSpace(in.DirectoryURL)
		directoryChanged = next != a.DirectoryURL
		a.DirectoryURL = next
	}
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
	if directoryChanged && a.Status != "disabled" {
		if err := s.Store.SetCAStatus(ctx, a.ID, "new", nil, store.Now()); err != nil {
			return nil, err
		}
		a.Status = "new"
	}
	return a, nil
}

// DeleteCA removes a CA account that is not referenced by a live certificate.
func (s *Service) DeleteCA(ctx context.Context, id string) error {
	n, err := s.Store.CountCertificatesUsingCA(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return &ConflictError{Msg: fmt.Sprintf("CA account is used by %d certificate(s)", n)}
	}
	return s.Store.DeleteCAAccount(ctx, id)
}

// SetCAEnabled disables a CA account or puts a disabled account back into the
// "new" state so it must be registered again.
func (s *Service) SetCAEnabled(ctx context.Context, id string, enabled bool) (*store.CAAccount, error) {
	a, err := s.Store.GetCAAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	status := a.Status
	if enabled {
		if status == "disabled" {
			status = "new"
		}
	} else {
		status = "disabled"
	}
	if status != a.Status {
		if err := s.Store.SetCAStatus(ctx, id, status, nil, store.Now()); err != nil {
			return nil, err
		}
	}
	return s.Store.GetCAAccount(ctx, id)
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
	if err := s.DNS.ValidateConfig(in.Provider, in.Config); err != nil {
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
		if err := s.DNS.ValidateConfig(d.Provider, in.Config); err != nil {
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
		return &ConflictError{Msg: fmt.Sprintf("credential is used by %d certificate(s)", n)}
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

// VerifyDNS checks that a stored credential can be decrypted and still matches
// the provider catalog. It is a local validation, not a live provider API call.
func (s *Service) VerifyDNS(ctx context.Context, id string) (*store.DNSCredential, error) {
	d, err := s.Store.GetDNSCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.DecryptDNSConfig(d)
	if err != nil {
		msg := "cannot decrypt credential"
		_ = s.Store.SetDNSCredentialHealth(ctx, d.ID, "error", &msg, nil, store.Now())
		return nil, err
	}
	if err := s.DNS.ValidateConfig(d.Provider, cfg); err != nil {
		msg := job.Redact(err.Error())
		_ = s.Store.SetDNSCredentialHealth(ctx, d.ID, "error", &msg, nil, store.Now())
		return nil, err
	}
	if err := s.Store.SetDNSCredentialHealth(ctx, d.ID, "ok", nil, nil, store.Now()); err != nil {
		return nil, err
	}
	return s.Store.GetDNSCredential(ctx, id)
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
