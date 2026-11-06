package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
)

type deploymentCredentialView struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	ConfiguredFields []string       `json:"configured_fields"`
	PublicConfig     map[string]any `json:"public_config"`
	CreatedAt        int64          `json:"created_at"`
	UpdatedAt        int64          `json:"updated_at"`
}

// publicCredentialFields lists non-secret fields that are safe to show to
// operators who only need to select a credential.
var publicCredentialFields = map[string][]string{
	"baota":  {"url", "ignore_tls"},
	"1panel": {"url", "version", "ignore_tls"},
	"ssh":    {"host", "port", "user", "host_key_sha256", "insecure_ignore_host_key"},
}

func (s *Server) deploymentCredentialView(c store.DeploymentCredential) deploymentCredentialView {
	v := deploymentCredentialView{
		ID: c.ID, Name: c.Name, Type: c.Type, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		ConfiguredFields: []string{}, PublicConfig: map[string]any{},
	}
	if cfg, err := s.Svc.DecryptDeploymentCredentialConfig(&c); err == nil {
		for k := range cfg {
			v.ConfiguredFields = append(v.ConfiguredFields, k)
		}
		sort.Strings(v.ConfiguredFields)
		for _, field := range publicCredentialFields[c.Type] {
			if value, ok := cfg[field]; ok {
				v.PublicConfig[field] = value
			}
		}
	}
	return v
}

func (s *Server) listDeploymentCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListDeploymentCredentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]deploymentCredentialView, 0, len(items))
	for _, c := range items {
		out = append(out, s.deploymentCredentialView(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "types": deploy.DeploymentCredentialTypes()})
}

func (s *Server) createDeploymentCredential(w http.ResponseWriter, r *http.Request) {
	var in service.DeploymentCredentialInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.CreateDeploymentCredential(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment_credential.create", "deployment_credential", c.ID, "ok", "type="+c.Type)
	writeJSON(w, http.StatusCreated, s.deploymentCredentialView(*c))
}

func (s *Server) updateDeploymentCredential(w http.ResponseWriter, r *http.Request) {
	var in service.DeploymentCredentialInput
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	c, err := s.Svc.UpdateDeploymentCredential(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment_credential.update", "deployment_credential", c.ID, "ok", "type="+c.Type)
	writeJSON(w, http.StatusOK, s.deploymentCredentialView(*c))
}

func (s *Server) deleteDeploymentCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteDeploymentCredential(r.Context(), id); err != nil {
		s.audit(r, "deployment_credential.delete", "deployment_credential", id, "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment_credential.delete", "deployment_credential", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDeploymentCredentialConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type   string `json:"type"`
		Config any    `json:"config"`
	}
	if err := decodeJSON(w, r, &in, 64*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	raw, err := json.Marshal(in.Config)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid config", nil)
		return
	}
	if err := s.Svc.TestDeploymentCredential(r.Context(), in.Type, raw); err != nil {
		s.audit(r, "deployment_credential.test", "deployment_credential", "", "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment_credential.test", "deployment_credential", "", "ok", "type="+in.Type)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testDeploymentCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.TestDeploymentCredentialByID(r.Context(), id); err != nil {
		s.audit(r, "deployment_credential.test", "deployment_credential", id, "error", err.Error())
		s.fail(w, r, err)
		return
	}
	s.audit(r, "deployment_credential.test", "deployment_credential", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}
