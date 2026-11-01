package store

import (
	"context"
	"path/filepath"
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
	logs, err := st.ListAudit(ctx, 10, 0)
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
