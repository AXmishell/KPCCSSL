package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/axmishell/kpccssl/internal/acme/acmesh"
	"github.com/axmishell/kpccssl/internal/store"
)

// Reconcile runs startup consistency checks and enqueues a sync job.
func (s *Service) Reconcile(ctx context.Context) error {
	now := store.Now()
	n, err := s.Store.InterruptRunning(ctx, now)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Warn("marked interrupted jobs after restart", "count", n)
	}
	recovered, err := s.Store.RecoverInterruptedCertificates(ctx, now)
	if err != nil {
		return err
	}
	for _, certID := range recovered {
		s.AddEvent(ctx, certID, "recovered", "interrupted operation repaired after restart", "")
	}
	if len(recovered) > 0 {
		slog.Warn("recovered certificates left in transitional states", "count", len(recovered))
	}
	if n, err := s.Store.RecoverWorkflowRuns(ctx, now-120); err != nil {
		slog.Warn("workflow recovery failed", "error", err)
	} else if n > 0 {
		slog.Warn("re-queued interrupted workflow runs", "count", n)
	}
	// Remove leftover per-job credential directories.
	tmp := filepath.Join(s.Cfg.DataDir, "tmp", "jobs")
	if err := os.RemoveAll(tmp); err != nil {
		slog.Warn("cleanup temp jobs failed", "error", err)
	}
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	// The primary account.conf must never contain provider credentials.
	bad, err := acmesh.InspectMainAccountConf(filepath.Join(s.Cfg.ACMEHome, "account.conf"))
	if err != nil {
		slog.Warn("cannot inspect account.conf", "error", err)
	} else if len(bad) > 0 {
		slog.Warn("account.conf contains SAVED_* credentials", "keys", bad)
	}
	if touched, err := acmesh.ScrubSavedCredentials(s.Cfg.ACMEHome); err != nil {
		slog.Warn("scrub acme.sh credentials failed", "error", err)
	} else if len(touched) > 0 {
		slog.Warn("scrubbed SAVED_* credentials from acme.sh config", "files", len(touched))
	}
	_, _, err = s.Enqueue(ctx, "sync", nil, nil, map[string]string{}, "", 3)
	return err
}

func (s *Service) handleSync(ctx context.Context, j *store.Job, log io.Writer) error {
	entries, err := s.Engine.List(ctx, log)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "acme.sh reports %d certificate(s)\n", len(entries))
	known := map[string]bool{}
	for _, e := range entries {
		known[e.MainDomain] = true
		c, err := s.Store.SearchCertificatesByDomain(ctx, e.MainDomain)
		if err != nil {
			fmt.Fprintf(log, "orphan: %s is present in acme.sh but not in the database\n", e.MainDomain)
			s.AddEvent(ctx, "", "sync_orphan", "acme.sh state without DB record: "+e.MainDomain, j.ID)
			continue
		}
		if len(e.SAN) > 0 && !sameSet(e.SAN, c.SAN) {
			fmt.Fprintf(log, "drift: %s SAN in acme.sh %v differs from desired %v\n", e.MainDomain, e.SAN, c.SAN)
			s.AddEvent(ctx, c.ID, "sync_drift", "acme.sh SAN differs from desired configuration", j.ID)
		}
		fmt.Fprintf(log, "ok: %s (CA %s)\n", e.MainDomain, e.CA)
	}
	if s.Cfg.FakeACME {
		return nil
	}
	certs, err := s.Store.ListCertificates(ctx, "", "", 200, 0, "")
	if err != nil {
		return err
	}
	for _, c := range certs {
		if c.Status == "active" && c.CurrentRevisionID != nil && !known[c.PrimaryDomain] {
			code := "E_CERT_MISSING"
			msg := "certificate present in database but missing from acme.sh state"
			fmt.Fprintf(log, "missing: %s\n", c.PrimaryDomain)
			_ = s.Store.SetCertificateStatus(ctx, c.ID, "failed", &code, &msg, store.Now())
			s.AddEvent(ctx, c.ID, "sync_missing", msg, j.ID)
			s.Notify(ctx, "certificate.failed", "cert:"+c.ID+":sync:"+j.ID, "Certificate state missing", c.PrimaryDomain+": "+msg)
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
		if m[x] < 0 {
			return false
		}
	}
	return true
}

// Scheduler periodically enqueues renewals and cleans up expired state.
type Scheduler struct {
	Service *Service
	Store   *store.Store
}

// Run blocks until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context) {
	// Startup sweep after the worker has had a chance to start.
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	s.Sweep(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	workflows := time.NewTicker(time.Minute)
	defer workflows.Stop()
	cleanup := time.NewTicker(6 * time.Hour)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
		case <-workflows.C:
			s.Service.RunScheduledWorkflows(ctx)
		case <-cleanup.C:
			if _, err := s.Store.CleanupSessions(ctx, store.Now()); err != nil {
				slog.Warn("session cleanup failed", "error", err)
			}
			if days := s.Service.SettingInt(ctx, "retention_days", s.Service.Cfg.RetentionDays); days > 0 {
				if n, err := s.Service.Logs.Cleanup(time.Now().AddDate(0, 0, -days)); err != nil {
					slog.Warn("log cleanup failed", "error", err)
				} else if n > 0 {
					slog.Info("removed old job logs", "count", n)
				}
			}
			s.Service.Limiter.Cleanup()
		}
	}
}

// Sweep enqueues renewals for certificates whose renewal window is due.
func (s *Scheduler) Sweep(ctx context.Context) {
	certs, err := s.Store.DueForRenewal(ctx, store.Now(), 200)
	if err != nil {
		slog.Error("renewal sweep failed", "error", err)
		return
	}
	for i, c := range certs {
		if c.Status != "active" || !c.AutoRenew {
			continue
		}
		if existing, err := s.Store.FindActiveJob(ctx, "renew", c.ID); err == nil && existing != nil {
			continue
		}
		// Spread the first attempt over a 10 minute window.
		jitter := time.Duration((i*37)%600) * time.Second
		certID := c.ID
		caID := c.CAAccountID
		j, created, err := s.Service.EnqueueAt(ctx, "renew", &certID, &caID, map[string]any{"force": false}, "", 5, jitter)
		if err != nil {
			slog.Error("enqueue renewal failed", "certificate", c.ID, "error", err)
			continue
		}
		if created {
			s.Service.AddEvent(ctx, c.ID, "renew_queued", "scheduled renewal queued", j.ID)
		}
	}
	s.notifyExpiring(ctx)
}

// notifyExpiring sends one "expires soon" notice per current revision. The
// notification delivery dedupe table prevents repeated messages.
func (s *Scheduler) notifyExpiring(ctx context.Context) {
	days := s.Service.SettingInt(ctx, "renewal_notice_days", 30)
	if days <= 0 {
		return
	}
	now := store.Now()
	items, err := s.Store.ListExpiringCertificates(ctx, now, now+int64(days)*86400, 200)
	if err != nil {
		slog.Warn("expiry notice scan failed", "error", err)
		return
	}
	for _, item := range items {
		if item.NotAfter <= now {
			continue
		}
		body := fmt.Sprintf("%s expires at %s", item.PrimaryDomain,
			time.Unix(item.NotAfter, 0).UTC().Format(time.RFC3339))
		s.Service.Notify(ctx, "certificate.expiring", "cert:"+item.ID+":"+item.RevisionID,
			"Certificate expires soon", body)
	}
}

// MarshalSyncPayload is a helper for tests.
func MarshalSyncPayload() string {
	b, _ := json.Marshal(map[string]string{})
	return string(b)
}
