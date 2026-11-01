package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
)

func jsonDecoder(r io.Reader) *json.Decoder {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec
}

func writeJSONBody(w io.Writer, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// userView is the safe public representation of a user.
type userView struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	TOTPEnabled bool   `json:"totp_enabled"`
	CreatedAt   int64  `json:"created_at"`
}

func viewUser(u *store.User) userView {
	return userView{ID: u.ID, Email: u.Email, Role: u.Role, Disabled: u.Disabled, TOTPEnabled: u.TOTPSecretEnc != nil, CreatedAt: u.CreatedAt}
}

type setupRequest struct {
	Token    string `json:"token"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !s.originOK(r) {
		writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("setup:"+clientIP(r), 10, 900e9); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry later: "+wait.String(), nil)
		return
	}
	u, err := s.Svc.Setup(r.Context(), req.Token, req.Email, req.Password)
	if err != nil {
		s.audit(r, "setup", "user", "", "denied", err.Error())
		writeProblem(w, r, http.StatusBadRequest, "E_SETUP", err.Error(), nil)
		return
	}
	token, csrf, _, err := s.Auth.Create(r.Context(), u, clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.sessionCookie(token))
	s.audit(r, "setup", "user", u.ID, "ok", "")
	writeJSON(w, http.StatusCreated, map[string]any{"user": viewUser(u), "csrf_token": csrf})
}

type loginRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	TOTPCode     string `json:"totp_code"`
	RecoveryCode string `json:"recovery_code"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !s.originOK(r) {
		writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
		return
	}
	u, err := s.Svc.Authenticate(r.Context(), req.Email, req.Password, clientIP(r))
	if err != nil {
		if errors.Is(err, serviceErrUnauthorized()) {
			s.audit(r, "auth.login", "user", "", "denied", "invalid credentials")
			writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "invalid email or password", nil)
			return
		}
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", err.Error(), nil)
		return
	}
	if u.TOTPSecretEnc != nil {
		ok, code, err := s.checkSecondFactor(r, u, req.TOTPCode, req.RecoveryCode)
		if err != nil {
			s.audit(r, "auth.login", "user", u.ID, "error", "2fa error")
			writeProblem(w, r, http.StatusInternalServerError, "E_INTERNAL", "cannot verify second factor", nil)
			return
		}
		if req.TOTPCode == "" && req.RecoveryCode == "" {
			writeProblem(w, r, http.StatusUnauthorized, "E_TOTP_REQUIRED", "two-factor code required", nil)
			return
		}
		if !ok {
			s.audit(r, "auth.login", "user", u.ID, "denied", "invalid 2fa code")
			writeProblem(w, r, http.StatusUnauthorized, "E_TOTP_INVALID", "invalid two-factor code", nil)
			return
		}
		_ = code
	}
	s.Svc.ResetLoginLimits(u.Email, clientIP(r))
	if old := principal(r); old != nil {
		_ = s.Auth.Destroy(r.Context(), old.Session.ID)
	}
	token, csrf, _, err := s.Auth.Create(r.Context(), u, clientIP(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, s.sessionCookie(token))
	s.audit(r, "auth.login", "user", u.ID, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u), "csrf_token": csrf})
}

func (s *Server) checkSecondFactor(r *http.Request, u *store.User, code, recovery string) (bool, string, error) {
	secret, err := s.Svc.Box.Decrypt(*u.TOTPSecretEnc, "users:"+u.ID+":totp")
	if err != nil {
		return false, "", err
	}
	if code != "" && auth.ValidateTOTP(string(secret), code, time.Now()) {
		return true, "totp", nil
	}
	if recovery != "" {
		ok, err := s.Store.ConsumeRecoveryCode(r.Context(), u.ID, auth.HashRecoveryCode(recovery), store.Now())
		if err != nil {
			return false, "", err
		}
		if ok {
			return true, "recovery", nil
		}
	}
	return false, "", nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p != nil {
		_ = s.Auth.Destroy(r.Context(), p.Session.ID)
		s.audit(r, "auth.logout", "user", p.User.ID, "ok", "")
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(p.User), "csrf_token": p.CSRF})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !auth.VerifyPassword(p.User.PasswordHash, req.CurrentPassword) {
		s.audit(r, "user.password_change", "user", p.User.ID, "denied", "wrong current password")
		writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "current password is incorrect", nil)
		return
	}
	if err := auth.ValidatePasswordPolicy(req.NewPassword, p.User.Email); err != nil {
		writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", err.Error(), nil)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.UpdateUserPassword(r.Context(), p.User.ID, hash, store.Now()); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.DeleteUserSessions(r.Context(), p.User.ID, p.Session.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.password_change", "user", p.User.ID, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"uri":    auth.TOTPURI("KPCCSSL", p.User.Email, secret),
	})
}

type totpEnableRequest struct {
	Secret   string `json:"secret"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req totpEnableRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !auth.VerifyPassword(p.User.PasswordHash, req.Password) {
		writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "password is incorrect", nil)
		return
	}
	if !auth.ValidateTOTP(req.Secret, req.Code, time.Now()) {
		writeProblem(w, r, http.StatusUnprocessableEntity, "E_TOTP_INVALID", "invalid two-factor code", nil)
		return
	}
	enc, err := s.Svc.Box.Encrypt([]byte(req.Secret), "users:"+p.User.ID+":totp")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.SetUserTOTP(r.Context(), p.User.ID, &enc, store.Now()); err != nil {
		s.fail(w, r, err)
		return
	}
	codes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Store.DeleteRecoveryCodes(r.Context(), p.User.ID)
	for _, c := range codes {
		if err := s.Store.CreateRecoveryCode(r.Context(), id.New(), p.User.ID, auth.HashRecoveryCode(c), store.Now()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	_ = s.Store.DeleteUserSessions(r.Context(), p.User.ID, p.Session.ID)
	s.audit(r, "user.totp_enable", "user", p.User.ID, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req totpDisableRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !auth.VerifyPassword(p.User.PasswordHash, req.Password) {
		writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "password is incorrect", nil)
		return
	}
	if p.User.TOTPSecretEnc != nil {
		ok, _, err := s.checkSecondFactor(r, p.User, req.Code, "")
		if err != nil || !ok {
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_TOTP_INVALID", "invalid two-factor code", nil)
			return
		}
	}
	if err := s.Store.SetUserTOTP(r.Context(), p.User.ID, nil, store.Now()); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Store.DeleteRecoveryCodes(r.Context(), p.User.ID)
	_ = s.Store.DeleteUserSessions(r.Context(), p.User.ID, p.Session.ID)
	s.audit(r, "user.totp_disable", "user", p.User.ID, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]userView, 0, len(users))
	for i := range users {
		out = append(out, viewUser(&users[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func serviceErrUnauthorized() error { return service.ErrUnauthorized }
