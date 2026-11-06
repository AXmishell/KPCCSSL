package api

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/axmishell/kpccssl/internal/service"
)

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := s.Svc.ListWorkflows(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	v, err := s.Svc.GetWorkflow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var in service.WorkflowInput
	if err := decodeJSON(w, r, &in, 512*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	v, err := s.Svc.CreateWorkflow(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workflow.create", "workflow", v.ID, "ok", v.Name)
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	var in service.WorkflowInput
	if err := decodeJSON(w, r, &in, 512*1024); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", nil)
		return
	}
	id := chi.URLParam(r, "id")
	v, err := s.Svc.UpdateWorkflow(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workflow.update", "workflow", id, "ok", v.Name)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.DeleteWorkflow(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workflow.delete", "workflow", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := s.Svc.RunWorkflow(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workflow.run", "workflow", id, "ok", "run="+run.ID)
	w.Header().Set("Location", "/api/v1/workflow-runs/"+run.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID})
}

func (s *Server) listWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	items, err := s.Svc.ListWorkflowRuns(r.Context(), chi.URLParam(r, "id"), parseLimit(r, 50, 200))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getWorkflowRun(w http.ResponseWriter, r *http.Request) {
	detail, err := s.Svc.GetWorkflowRunDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) cancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Svc.CancelWorkflowRun(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workflow.cancel", "workflow_run", id, "ok", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workflowRunLog(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetWorkflowRun(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	path := s.Svc.Logs.Path(id)
	if _, err := os.Stat(path); err != nil {
		writeProblem(w, r, http.StatusNotFound, "E_NOT_FOUND", "workflow log not found", nil)
		return
	}
	s.audit(r, "workflow.log.download", "workflow_run", id, "ok", "")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+id+".log")
	http.ServeFile(w, r, path)
}
