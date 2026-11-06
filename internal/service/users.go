package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/store"
)

// UserInput is the admin user-management payload. Disabled is a pointer so a
// PATCH can distinguish "leave unchanged" from "disable".
type UserInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Disabled *bool  `json:"disabled"`
}

// CreateUser creates an additional operator/viewer/admin account.
func (s *Service) CreateUser(ctx context.Context, in UserInput) (*store.User, error) {
	email := normalizeEmail(strings.TrimSpace(in.Email))
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if err := validateRole(in.Role); err != nil {
		return nil, err
	}
	if err := auth.ValidatePasswordPolicy(in.Password, email); err != nil {
		return nil, err
	}
	if _, err := s.Store.GetUserByEmail(ctx, email); err == nil {
		return nil, &ConflictError{Msg: "email is already registered"}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	now := store.Now()
	u := &store.User{
		ID: id.New(), Email: email, PasswordHash: hash, Role: in.Role,
		CreatedAt: now, UpdatedAt: now,
	}
	if in.Disabled != nil {
		u.Disabled = *in.Disabled
	}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUser changes role, disabled state and optionally resets the password.
func (s *Service) UpdateUser(ctx context.Context, targetID, actorID string, in UserInput) (*store.User, error) {
	u, err := s.Store.GetUser(ctx, targetID)
	if err != nil {
		return nil, err
	}
	role := u.Role
	if in.Role != "" && in.Role != u.Role {
		if err := validateRole(in.Role); err != nil {
			return nil, err
		}
		role = in.Role
	}
	disabled := u.Disabled
	if in.Disabled != nil {
		disabled = *in.Disabled
	}
	if targetID == actorID && disabled {
		return nil, errors.New("you cannot disable your own account")
	}
	if u.Role == "admin" && (role != "admin" || disabled) {
		n, err := s.Store.CountActiveAdmins(ctx)
		if err != nil {
			return nil, err
		}
		if n <= 1 {
			return nil, errors.New("cannot remove the last active administrator")
		}
	}
	now := store.Now()
	if in.Password != "" {
		if err := auth.ValidatePasswordPolicy(in.Password, u.Email); err != nil {
			return nil, err
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return nil, err
		}
		if err := s.Store.UpdateUserPassword(ctx, targetID, hash, now); err != nil {
			return nil, err
		}
		_ = s.Store.DeleteUserSessions(ctx, targetID, "")
	}
	if err := s.Store.UpdateUserMeta(ctx, targetID, role, disabled, now); err != nil {
		return nil, err
	}
	if disabled {
		_ = s.Store.DeleteUserSessions(ctx, targetID, "")
	}
	return s.Store.GetUser(ctx, targetID)
}

func validateRole(role string) error {
	switch role {
	case "admin", "operator", "viewer":
		return nil
	}
	return fmt.Errorf("role must be admin, operator or viewer")
}
