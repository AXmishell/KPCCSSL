package service

import (
	"errors"
	"testing"

	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/store"
)

func TestFailedOrActive(t *testing.T) {
	rev := "r1"
	if got := failedOrActive(&store.Certificate{CurrentRevisionID: &rev}); got != "active" {
		t.Fatalf("with revision = %s", got)
	}
	if got := failedOrActive(&store.Certificate{}); got != "failed" {
		t.Fatalf("without revision = %s", got)
	}
}

func TestMapCertDomainErr(t *testing.T) {
	err := mapCertDomainErr(errors.New("constraint failed: certificate primary domain already exists"))
	var ve *certificate.ValidationError
	if !errors.As(err, &ve) || ve.Field != "primary_domain" {
		t.Fatalf("unexpected error: %v", err)
	}
	plain := errors.New("other")
	if mapCertDomainErr(plain) != plain {
		t.Fatal("unrelated error was rewritten")
	}
}

func TestValidateRole(t *testing.T) {
	for _, role := range []string{"admin", "operator", "viewer"} {
		if err := validateRole(role); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
	}
	if err := validateRole("root"); err == nil {
		t.Fatal("invalid role accepted")
	}
}
