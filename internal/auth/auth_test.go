package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/axmishell/kpccssl/internal/store"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format %q", hash)
	}
	if !VerifyPassword(hash, "correct-horse-battery-staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Fatal("invalid password accepted")
	}
	if VerifyPassword("garbage", "whatever") {
		t.Fatal("malformed hash accepted")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := ValidatePasswordPolicy("short", "a@b.com"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := ValidatePasswordPolicy("alice-is-my-password", "alice@example.com"); err == nil {
		t.Fatal("password containing local part accepted")
	}
	if err := ValidatePasswordPolicy("a-perfectly-fine-passphrase", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestTOTP(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := time.Unix(59, 0)
	if !ValidateTOTP(secret, "287082", at) {
		t.Fatal("RFC vector rejected")
	}
	if !ValidateTOTP(secret, "287082", time.Unix(89, 0)) {
		t.Fatal("adjacent time step should be accepted")
	}
	if ValidateTOTP(secret, "000000", at) {
		t.Fatal("wrong code accepted")
	}
	if ValidateTOTP(secret, "12345", at) {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatal("duplicate recovery code")
		}
		seen[c] = true
		if len(c) != 11 || c[5] != '-' {
			t.Fatalf("unexpected code %q", c)
		}
	}
	if string(HashRecoveryCode("ABCDE-FGHIJ")) != string(HashRecoveryCode("abcde fghij")) {
		t.Fatal("hash must normalise case and separators")
	}
}

func TestStepUpToken(t *testing.T) {
	m := &Manager{}
	now := time.Unix(1_800_000_000, 0)
	p := &Principal{Session: &store.Session{ID: "s1", CSRFSecret: "secret"}}
	token := m.IssueStepUp(p, now)
	if !m.VerifyStepUp(p, token, now) {
		t.Fatal("valid step-up token rejected")
	}
	if m.VerifyStepUp(p, token, now.Add(StepUpTTL+time.Second)) {
		t.Fatal("expired step-up token accepted")
	}
	other := &Principal{Session: &store.Session{ID: "s2", CSRFSecret: "secret"}}
	if m.VerifyStepUp(other, token, now) {
		t.Fatal("token accepted for another session")
	}
	if m.VerifyStepUp(p, token+"x", now) {
		t.Fatal("tampered token accepted")
	}
}

func TestRoleAllowed(t *testing.T) {
	if !RoleAllowed("admin", "viewer") || !RoleAllowed("operator", "viewer") || !RoleAllowed("admin", "admin") {
		t.Fatal("valid role checks failed")
	}
	if RoleAllowed("viewer", "operator") || RoleAllowed("operator", "admin") {
		t.Fatal("privilege escalation allowed")
	}
}
