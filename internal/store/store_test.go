package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st, context.Background()
}

func TestUsersAndAudit(t *testing.T) {
	st, ctx := testStore(t)
	n, err := st.CountUsers(ctx)
	if err != nil || n != 0 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	now := Now()
	u := &User{ID: "u1", Email: "a@example.com", PasswordHash: "hash", Role: "admin", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUserByEmail(ctx, "a@example.com")
	if err != nil || got.ID != "u1" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, err := st.GetUser(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := st.InsertAudit(ctx, &AuditLog{ID: "a1", Action: "test", ResourceType: "x", Result: "ok", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	logs, err := st.ListAudit(ctx, 10, 0, "")
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs=%v err=%v", logs, err)
	}
}

func TestCertificateOptimisticLock(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	ca := &CAAccount{ID: "ca1", Name: "CA", DirectoryURL: "https://ca/dir", Status: "registered", KeyVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCAAccount(ctx, ca); err != nil {
		t.Fatal(err)
	}
	c := &Certificate{
		ID: "c1", Name: "cert", PrimaryDomain: "example.com", SAN: []string{"example.com"},
		KeyType: "ec-256", CAAccountID: "ca1", ChallengeType: "dns-01", AutoRenew: true,
		Status: "draft", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateCertificate(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.PrimaryDomain = "changed.example.com"
	c.UpdatedAt = now + 1
	if err := st.UpdateCertificate(ctx, c); err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 {
		t.Fatalf("version = %d", c.Version)
	}
	stale := *c
	stale.Version = 1
	stale.PrimaryDomain = "stale.example.com"
	if err := st.UpdateCertificate(ctx, &stale); err != ErrConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestJobClaimAndRetry(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	key := "idem-1"
	j := &Job{ID: "j1", Type: "issue", Status: "queued", MaxAttempts: 3, PayloadJSON: "{}", CreatedAt: now, AvailableAt: now, IdempotencyKey: &key}
	created, err := st.CreateJob(ctx, j)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	// Idempotency: same key returns the existing job.
	j2 := &Job{ID: "j2", Type: "issue", Status: "queued", MaxAttempts: 3, CreatedAt: now, AvailableAt: now, IdempotencyKey: &key}
	created, err = st.CreateJob(ctx, j2)
	if err != nil || created || j2.ID != "j1" {
		t.Fatalf("created=%v err=%v id=%s", created, err, j2.ID)
	}
	claimed, err := st.ClaimNext(ctx, now)
	if err != nil || claimed == nil || claimed.ID != "j1" {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if claimed.Attempts != 1 {
		t.Fatalf("attempts=%d", claimed.Attempts)
	}
	next, err := st.ClaimNext(ctx, now)
	if err != nil || next != nil {
		t.Fatalf("second claim = %v err=%v", next, err)
	}
	if err := st.RequeueJob(ctx, "j1", now+3600, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if next, _ := st.ClaimNext(ctx, now); next != nil {
		t.Fatal("job claimed before available_at")
	}
	next, err = st.ClaimNext(ctx, now+3601)
	if err != nil || next == nil || next.ID != "j1" || next.Attempts != 2 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	n, err := st.InterruptRunning(ctx, now+7200)
	if err != nil || n != 1 {
		t.Fatalf("interrupted=%d err=%v", n, err)
	}
	got, _ := st.GetJob(ctx, "j1")
	if got.Status != "interrupted" {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestCertificatePrimaryDomainUnique(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	ca := &CAAccount{ID: "ca1", Name: "CA", DirectoryURL: "https://ca/dir", Status: "registered", KeyVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCAAccount(ctx, ca); err != nil {
		t.Fatal(err)
	}
	base := Certificate{Name: "one", PrimaryDomain: "example.com", SAN: []string{"example.com"}, KeyType: "ec-256", CAAccountID: "ca1", ChallengeType: "dns-01", Status: "draft", Version: 1, CreatedAt: now, UpdatedAt: now}
	first := base
	first.ID = "c1"
	if err := st.CreateCertificate(ctx, &first); err != nil {
		t.Fatal(err)
	}
	second := base
	second.ID = "c2"
	if err := st.CreateCertificate(ctx, &second); err == nil {
		t.Fatal("duplicate primary domain was accepted")
	} else if !strings.Contains(err.Error(), "primary domain already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecoverInterruptedCertificates(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	ca := &CAAccount{ID: "ca1", Name: "CA", DirectoryURL: "https://ca/dir", Status: "registered", KeyVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCAAccount(ctx, ca); err != nil {
		t.Fatal(err)
	}
	mk := func(id, domain, status string) *Certificate {
		c := &Certificate{ID: id, Name: id, PrimaryDomain: domain, SAN: []string{domain}, KeyType: "ec-256", CAAccountID: "ca1", ChallengeType: "dns-01", Status: status, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := st.CreateCertificate(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	broken := mk("c1", "broken.example.com", "issuing")
	active := mk("c2", "active.example.com", "renewing")
	queued := mk("c3", "queued.example.com", "queued")

	rev := &Revision{ID: "r1", CertificateID: active.ID, Serial: "aa", FingerprintSHA256: "bb", NotBefore: now, NotAfter: now + 86400,
		CertPath: "/tmp/cert", KeyPath: "/tmp/key", ChainPath: "/tmp/chain", FullchainPath: "/tmp/full", IssuedAt: now, CreatedAt: now}
	if err := st.ActivateRevision(ctx, active.ID, rev, now+3600, now); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCertificateStatus(ctx, active.ID, "renewing", nil, nil, now); err != nil {
		t.Fatal(err)
	}
	// A queued job means the worker still owns the transition.
	if _, err := st.CreateJob(ctx, &Job{ID: "j1", Type: "issue", CertificateID: &queued.ID, Status: "queued", MaxAttempts: 3, PayloadJSON: "{}", CreatedAt: now, AvailableAt: now}); err != nil {
		t.Fatal(err)
	}
	ids, err := st.RecoverInterruptedCertificates(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("recovered %v", ids)
	}
	gotBroken, _ := st.GetCertificate(ctx, broken.ID)
	if gotBroken.Status != "failed" {
		t.Fatalf("broken status = %s", gotBroken.Status)
	}
	gotActive, _ := st.GetCertificate(ctx, active.ID)
	if gotActive.Status != "active" {
		t.Fatalf("active status = %s", gotActive.Status)
	}
	gotQueued, _ := st.GetCertificate(ctx, queued.ID)
	if gotQueued.Status != "queued" {
		t.Fatalf("queued status = %s", gotQueued.Status)
	}
}

func TestActiveJobUniqueIndex(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	ca := &CAAccount{ID: "ca1", Name: "CA", DirectoryURL: "https://ca/dir", Status: "registered", KeyVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCAAccount(ctx, ca); err != nil {
		t.Fatal(err)
	}
	c := &Certificate{ID: "c1", Name: "cert", PrimaryDomain: "example.com", SAN: []string{"example.com"}, KeyType: "ec-256", CAAccountID: "ca1", ChallengeType: "dns-01", Status: "draft", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCertificate(ctx, c); err != nil {
		t.Fatal(err)
	}
	j1 := &Job{ID: "j1", Type: "issue", CertificateID: &c.ID, Status: "queued", MaxAttempts: 3, PayloadJSON: "{}", CreatedAt: now, AvailableAt: now}
	created, err := st.CreateJob(ctx, j1)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	j2 := &Job{ID: "j2", Type: "issue", CertificateID: &c.ID, Status: "queued", MaxAttempts: 3, PayloadJSON: "{}", CreatedAt: now, AvailableAt: now}
	created, err = st.CreateJob(ctx, j2)
	if err != nil || created || j2.ID != "j1" {
		t.Fatalf("created=%v err=%v id=%s", created, err, j2.ID)
	}
}

func TestDNSReferenceCheck(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()
	ca := &CAAccount{ID: "ca1", Name: "CA", DirectoryURL: "https://ca/dir", Status: "registered", KeyVersion: 1, CreatedAt: now, UpdatedAt: now}
	_ = st.CreateCAAccount(ctx, ca)
	d := &DNSCredential{ID: "d1", Name: "cf", Provider: "dns_cf", ConfigEnc: "v1:x", KeyVersion: 1, Status: "ok", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateDNSCredential(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := &Certificate{ID: "c1", Name: "x", PrimaryDomain: "example.com", SAN: []string{"example.com"}, KeyType: "ec-256", CAAccountID: "ca1", ChallengeType: "dns-01", DNSCredentialID: &d.ID, Status: "draft", Version: 1, AutoRenew: true, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCertificate(ctx, c); err != nil {
		t.Fatal(err)
	}
	n, err := st.CountCertificatesUsingCredential(ctx, "d1")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
