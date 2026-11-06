package store

import (
	"errors"
	"testing"
)

func TestDeploymentCredentialCRUDAndUsage(t *testing.T) {
	st, ctx := testStore(t)
	now := Now()

	cred := &DeploymentCredential{
		ID: "c1", Name: "Tencent", Type: "tencent",
		ConfigEnc: "v1:test", KeyVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateDeploymentCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDeploymentCredential(ctx, "c1")
	if err != nil || got.Name != "Tencent" || got.Type != "tencent" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	cred.Name = "Tencent prod"
	if err := st.UpdateDeploymentCredential(ctx, cred); err != nil {
		t.Fatal(err)
	}

	ca := &CAAccount{ID: "ca1", Name: "ca", DirectoryURL: "https://example.com/directory", KeyVersion: 1, Status: "registered", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateCAAccount(ctx, ca); err != nil {
		t.Fatal(err)
	}
	cert := &Certificate{
		ID: "cert1", Name: "cert", PrimaryDomain: "example.com", KeyType: "ec-256",
		CAAccountID: ca.ID, ChallengeType: "dns-01", AutoRenew: true, Status: "active",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateCertificate(ctx, cert); err != nil {
		t.Fatal(err)
	}
	dep := &Deployment{
		ID: "d1", CertificateID: cert.ID, Name: "dep", Type: "tencent_cdn",
		CredentialID: &cred.ID, ConfigEnc: "v1:test", KeyVersion: 1,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateDeployment(ctx, dep); err != nil {
		t.Fatal(err)
	}
	read, err := st.GetDeployment(ctx, dep.ID)
	if err != nil || read.CredentialID == nil || *read.CredentialID != cred.ID {
		t.Fatalf("deployment credential not persisted: %v err=%v", read.CredentialID, err)
	}
	if n, err := st.DeploymentCredentialUsage(ctx, cred.ID); err != nil || n != 1 {
		t.Fatalf("usage=%d err=%v", n, err)
	}
	if err := st.DeleteDeploymentCredential(ctx, cred.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	if err := st.DeleteDeployment(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDeploymentCredential(ctx, cred.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDeploymentCredential(ctx, cred.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
