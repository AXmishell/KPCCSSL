package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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

type stepUpRequest struct {
	Password     string `json:"password"`
	TOTPCode     string `json:"totp_code"`
	RecoveryCode string `json:"recovery_code"`
}

// stepUp re-authenticates the current session for sensitive operations such as
// private-key downloads. It issues a short-lived, session-bound token.
func (s *Server) stepUp(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req stepUpRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("stepup:"+p.User.ID+":"+clientIP(r), 10, 15*time.Minute); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry in "+wait.Round(time.Second).String(), nil)
		return
	}
	if !auth.VerifyPassword(p.User.PasswordHash, req.Password) {
		s.audit(r, "auth.step_up", "user", p.User.ID, "denied", "wrong password")
		writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "password is incorrect", nil)
		return
	}
	if p.User.TOTPSecretEnc != nil {
		if req.TOTPCode == "" && req.RecoveryCode == "" {
			writeProblem(w, r, http.StatusUnauthorized, "E_TOTP_REQUIRED", "two-factor code required", nil)
			return
		}
		ok, _, err := s.checkSecondFactor(r, p.User, req.TOTPCode, req.RecoveryCode)
		if err != nil {
			writeProblem(w, r, http.StatusInternalServerError, "E_INTERNAL", "cannot verify second factor", nil)
			return
		}
		if !ok {
			s.audit(r, "auth.step_up", "user", p.User.ID, "denied", "invalid 2fa code")
			writeProblem(w, r, http.StatusUnauthorized, "E_TOTP_INVALID", "invalid two-factor code", nil)
			return
		}
	}
	token := s.Auth.IssueStepUp(p, time.Now())
	s.audit(r, "auth.step_up", "user", p.User.ID, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{
		"step_up_token": token,
		"expires_in":    int(auth.StepUpTTL / time.Second),
	})
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

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in service.UserInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	u, err := s.Svc.CreateUser(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.create", "user", u.ID, "ok", u.Role)
	writeJSON(w, http.StatusCreated, viewUser(u))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var in service.UserInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	u, err := s.Svc.UpdateUser(r.Context(), chi.URLParam(r, "id"), p.User.ID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.update", "user", u.ID, "ok", "")
	writeJSON(w, http.StatusOK, viewUser(u))
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// forgotPassword sends a reset link without revealing whether the account
// exists. Delivery failures are logged but the response is identical.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !s.originOK(r) {
		writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("forgot:ip:"+clientIP(r), 10, 15*time.Minute); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry in "+wait.Round(time.Second).String(), nil)
		return
	}
	emailKey := strings.ToLower(strings.TrimSpace(req.Email))
	if ok, _ := s.Svc.Limiter.Allow("forgot:email:"+emailKey, 3, 15*time.Minute); !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.Svc.RequestPasswordReset(r.Context(), req.Email, s.publicBaseURL(r))
	if errors.Is(err, service.ErrMailNotConfigured) {
		s.audit(r, "auth.forgot_password", "user", "", "error", "mail not configured")
		writeProblem(w, r, http.StatusUnprocessableEntity, "E_MAIL_NOT_CONFIGURED", "outbound email is not configured", nil)
		return
	}
	if err != nil {
		slog.Warn("password reset mail failed", "error", err)
		s.audit(r, "auth.forgot_password", "user", "", "error", "delivery failed")
	} else {
		s.audit(r, "auth.forgot_password", "user", "", "ok", "")
	}
	w.WriteHeader(http.StatusNoContent)
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !s.originOK(r) {
		writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("reset:ip:"+clientIP(r), 20, 15*time.Minute); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry in "+wait.Round(time.Second).String(), nil)
		return
	}
	if err := s.Svc.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		var invalid *service.InputError
		switch {
		case errors.Is(err, service.ErrInvalidToken):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_INVALID_TOKEN", "link is invalid or has expired", nil)
		case errors.As(err, &invalid):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", invalid.Msg, nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "auth.password_reset", "user", "", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

type confirmEmailRequest struct {
	Token string `json:"token"`
}

func (s *Server) confirmEmail(w http.ResponseWriter, r *http.Request) {
	var req confirmEmailRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if !s.originOK(r) {
		writeProblem(w, r, http.StatusForbidden, "E_ORIGIN", "cross-origin request refused", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("confirm_email:ip:"+clientIP(r), 20, 15*time.Minute); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry in "+wait.Round(time.Second).String(), nil)
		return
	}
	u, err := s.Svc.ConfirmEmailChange(r.Context(), req.Token)
	if err != nil {
		var conflict *service.ConflictError
		switch {
		case errors.Is(err, service.ErrInvalidToken):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_INVALID_TOKEN", "link is invalid or has expired", nil)
		case errors.As(err, &conflict):
			writeProblem(w, r, http.StatusConflict, "E_CONFLICT", conflict.Msg, nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "user.email_change", "user", u.ID, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"email": u.Email})
}

type changeEmailRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) requestEmailChange(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var req changeEmailRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if ok, wait := s.Svc.Limiter.Allow("email_change:user:"+p.User.ID, 5, time.Hour); !ok {
		writeProblem(w, r, http.StatusTooManyRequests, "E_RATE_LIMIT", "too many attempts, retry in "+wait.Round(time.Second).String(), nil)
		return
	}
	if err := s.Svc.RequestEmailChange(r.Context(), p.User.ID, req.Email, req.Password, s.publicBaseURL(r)); err != nil {
		var invalid *service.InputError
		var conflict *service.ConflictError
		var mailErr *service.MailError
		switch {
		case errors.Is(err, service.ErrUnauthorized):
			s.audit(r, "user.email_change_request", "user", p.User.ID, "denied", "wrong password")
			writeProblem(w, r, http.StatusUnauthorized, "E_UNAUTHENTICATED", "password is incorrect", nil)
		case errors.Is(err, service.ErrMailNotConfigured):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_MAIL_NOT_CONFIGURED", "outbound email is not configured", nil)
		case errors.As(err, &invalid):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", invalid.Msg, nil)
		case errors.As(err, &conflict):
			writeProblem(w, r, http.StatusConflict, "E_CONFLICT", conflict.Msg, nil)
		case errors.As(err, &mailErr):
			s.audit(r, "user.email_change_request", "user", p.User.ID, "error", "delivery failed")
			writeProblem(w, r, http.StatusBadGateway, "E_MAIL", "could not send verification email", nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "user.email_change_request", "user", p.User.ID, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	userID := chi.URLParam(r, "id")
	if err := s.Svc.AdminSendPasswordReset(r.Context(), userID, s.publicBaseURL(r)); err != nil {
		var invalid *service.InputError
		var mailErr *service.MailError
		switch {
		case errors.Is(err, service.ErrMailNotConfigured):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_MAIL_NOT_CONFIGURED", "outbound email is not configured", nil)
		case errors.As(err, &invalid):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_VALIDATION", invalid.Msg, nil)
		case errors.As(err, &mailErr):
			s.audit(r, "user.password_reset_email", "user", userID, "error", "delivery failed")
			writeProblem(w, r, http.StatusBadGateway, "E_MAIL", "could not send reset email", nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "user.password_reset_email", "user", userID, "ok", p.User.ID)
	w.WriteHeader(http.StatusNoContent)
}

type testMailRequest struct {
	To string `json:"to"`
}

func (s *Server) testMail(w http.ResponseWriter, r *http.Request) {
	var req testMailRequest
	if err := decodeJSON(w, r, &req, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	if err := s.Svc.TestMail(r.Context(), req.To); err != nil {
		var mailErr *service.MailError
		switch {
		case errors.Is(err, service.ErrMailNotConfigured):
			writeProblem(w, r, http.StatusUnprocessableEntity, "E_MAIL_NOT_CONFIGURED", "outbound email is not configured", nil)
		case errors.As(err, &mailErr):
			s.audit(r, "settings.mail_test", "settings", "", "error", "delivery failed")
			writeProblem(w, r, http.StatusBadGateway, "E_MAIL", "could not send test email", nil)
		default:
			s.fail(w, r, err)
		}
		return
	}
	s.audit(r, "settings.mail_test", "settings", "", "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func serviceErrUnauthorized() error { return service.ErrUnauthorized }
