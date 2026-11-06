package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

func (r *WorkflowRunner) executeStep(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer, def *WorkflowDefinition, byStep map[string]*store.WorkflowStepRun) (bool, error) {
	switch step.Type {
	case StepApply:
		return r.executeApply(ctx, run, step, st, logw)
	case StepDeploy:
		return r.executeDeploy(ctx, run, step, st, logw, def, byStep)
	case StepNotify:
		return r.executeNotify(ctx, run, step, st, logw)
	case StepVerify:
		return r.executeVerify(ctx, run, step, st, logw, def, byStep)
	case StepWait:
		var cfg WaitStepConfig
		if len(step.Config) > 0 {
			if err := json.Unmarshal(step.Config, &cfg); err != nil {
				return false, err
			}
		}
		if st.StartedAt == nil {
			return false, nil
		}
		if store.Now()-*st.StartedAt < int64(cfg.Seconds) {
			return false, nil
		}
		_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", "{}", nil, store.Now())
		return true, nil
	}
	return false, fmt.Errorf("unsupported step type %q", step.Type)
}

func (r *WorkflowRunner) executeApply(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer) (bool, error) {
	var cfg ApplyStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	// An apply step never inherits a certificate. It either uses an explicit
	// certificate_id or creates the inline certificate described by its config.
	// Inheriting here would silently reuse the first certificate when a graph
	// contains multiple apply nodes.
	certID := cfg.CertificateID
	if certID == "" {
		if cfg.PrimaryDomain == "" {
			return false, errors.New("apply step requires certificate_id or primary_domain")
		}
		var err error
		certID, err = r.createInlineCertificate(ctx, cfg)
		if err != nil {
			return false, err
		}
	}
	output := mustJSON(map[string]any{"certificate_id": certID})
	_ = r.Store.SetStepOutput(ctx, st.ID, output)
	c, err := r.Store.GetCertificate(ctx, certID)
	if err != nil {
		return false, err
	}
	mode := cfg.Mode
	if mode == "" || mode == "auto" {
		if c.CurrentRevisionID != nil {
			mode = "renew"
		} else {
			mode = "issue"
		}
	}
	key := "workflow:" + run.ID + ":" + step.ID
	var j *store.Job
	if mode == "issue" {
		j, err = r.Service.Issue(ctx, certID, key, cfg.Force)
	} else {
		j, err = r.Service.Renew(ctx, certID, key, cfg.Force)
	}
	if err != nil {
		return false, err
	}
	fmt.Fprintf(logw, "queued %s job %s for %s\n", mode, j.ID, certID)
	_ = r.Store.SetStepJobs(ctx, st.ID, mustJSON([]string{j.ID}))
	return r.cancelIfInactive(ctx, run.ID, st, []string{j.ID})
}

func (r *WorkflowRunner) executeDeploy(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer, def *WorkflowDefinition, byStep map[string]*store.WorkflowStepRun) (bool, error) {
	var cfg DeployStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	certID := cfg.CertificateID
	revisionID := cfg.RevisionID
	if certID == "" {
		var err error
		certID, err = r.resolveUpstreamValue(ctx, run.ID, step.ID, "certificate_id", def, byStep)
		if err != nil {
			return false, err
		}
		if certID == "" {
			return false, errors.New("deploy step has no certificate_id and no upstream certificate")
		}
		// Pin the deploy to the revision produced by the upstream apply when
		// possible, instead of racing with a concurrent renewal that changes
		// the certificate's current revision.
		if revisionID == "" {
			revisionID, err = r.resolveUpstreamValue(ctx, run.ID, step.ID, "revision_id", def, byStep)
			if err != nil {
				return false, err
			}
		}
	}
	key := "workflow:" + run.ID + ":" + step.ID
	var jobs []*store.Job
	var err error
	if revisionID != "" {
		jobs, err = r.Service.DeployRevisionNow(ctx, certID, revisionID, key)
	} else {
		jobs, err = r.Service.DeployNow(ctx, certID, key)
	}
	if err != nil {
		return false, err
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	fmt.Fprintf(logw, "queued %d deploy job(s) for %s\n", len(ids), certID)
	_ = r.Store.SetStepJobs(ctx, st.ID, mustJSON(ids))
	out := map[string]any{"certificate_id": certID}
	if revisionID != "" {
		out["revision_id"] = revisionID
	}
	_ = r.Store.SetStepOutput(ctx, st.ID, mustJSON(out))
	return r.cancelIfInactive(ctx, run.ID, st, ids)
}

func (r *WorkflowRunner) executeNotify(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer) (bool, error) {
	var cfg NotifyStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	event := cfg.Event
	if event == "" {
		event = "workflow.notify"
	}
	title := renderWorkflowText(cfg.Title, run, step)
	body := renderWorkflowText(cfg.Body, run, step)
	fmt.Fprintf(logw, "sending notification %s: %s\n", event, title)
	if err := r.Service.Notify(ctx, event, "workflow:"+run.ID+":"+step.ID, title, body); err != nil {
		msg := job.Redact(err.Error())
		_ = r.Store.FinishStepRun(ctx, st.ID, "failed", "{}", &msg, store.Now())
		fmt.Fprintf(logw, "=== notification failed: %s ===\n", msg)
		return true, nil
	}
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", "{}", nil, store.Now())
	return true, nil
}

func (r *WorkflowRunner) executeVerify(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer, def *WorkflowDefinition, byStep map[string]*store.WorkflowStepRun) (bool, error) {
	var cfg VerifyStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	certID := cfg.CertificateID
	revisionID := ""
	if certID == "" {
		var err error
		certID, err = r.resolveUpstreamValue(ctx, run.ID, step.ID, "certificate_id", def, byStep)
		if err != nil {
			return false, err
		}
		if certID == "" {
			return false, errors.New("verify step has no certificate_id")
		}
		// Verify the exact revision produced upstream when available.
		revisionID, err = r.resolveUpstreamValue(ctx, run.ID, step.ID, "revision_id", def, byStep)
		if err != nil {
			return false, err
		}
	}
	c, err := r.Store.GetCertificate(ctx, certID)
	if err != nil {
		return false, err
	}
	if revisionID == "" {
		if c.CurrentRevisionID == nil {
			return false, errors.New("certificate has no issued revision")
		}
		revisionID = *c.CurrentRevisionID
	}
	rev, err := r.Store.GetRevision(ctx, revisionID)
	if err != nil {
		return false, err
	}
	if rev.CertificateID != certID {
		return false, errors.New("revision does not belong to certificate")
	}
	if rev.NotAfter <= store.Now()+int64(cfg.MinDays)*86400 {
		msg := fmt.Sprintf("certificate expires at %s (minimum %d days required)", time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339), cfg.MinDays)
		_ = r.Store.FinishStepRun(ctx, st.ID, "failed", mustJSON(map[string]any{"certificate_id": certID, "revision_id": rev.ID}), &msg, store.Now())
		return true, nil
	}
	fmt.Fprintf(logw, "certificate %s verified, expires %s\n", certID, time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339))
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", mustJSON(map[string]any{"certificate_id": certID, "revision_id": rev.ID, "not_after": rev.NotAfter}), nil, store.Now())
	return true, nil
}

// cancelIfInactive closes the race between starting a step and a concurrent
// cancellation. Queued jobs created by this step are canceled; running jobs are
// left to finish because the worker has no interruption signal.
func (r *WorkflowRunner) cancelIfInactive(ctx context.Context, runID string, st *store.WorkflowStepRun, jobIDs []string) (bool, error) {
	if r.runActive(ctx, runID) {
		return false, nil
	}
	now := store.Now()
	for _, jobID := range jobIDs {
		_ = r.Store.CancelJob(ctx, jobID, now)
	}
	_ = r.Store.FinishStepRun(ctx, st.ID, "canceled", st.OutputJSON, nil, now)
	return true, nil
}

// resolveUpstreamValue finds a value from an upstream step output. Graph
// definitions follow incoming edges; legacy ordered steps scan backwards.
func (r *WorkflowRunner) resolveUpstreamValue(ctx context.Context, runID, stepID, key string, def *WorkflowDefinition, byStep map[string]*store.WorkflowStepRun) (string, error) {
	if def != nil && len(def.Edges) > 0 {
		return resolveGraphValue(stepID, key, def, byStep)
	}
	return r.resolveSequentialValue(ctx, runID, key)
}

func resolveGraphValue(stepID, key string, def *WorkflowDefinition, byStep map[string]*store.WorkflowStepRun) (string, error) {
	incoming := map[string][]string{}
	for _, e := range def.Edges {
		incoming[e.To] = append(incoming[e.To], e.From)
	}
	// BFS backwards so each node is assigned its shortest distance from the
	// current node. The nearest level with an output wins; two different values
	// at that level are ambiguous.
	dist := map[string]int{stepID: 0}
	frontier := []string{stepID}
	for len(frontier) > 0 {
		var next []string
		for _, id := range frontier {
			for _, prev := range incoming[id] {
				if _, ok := dist[prev]; ok {
					continue
				}
				dist[prev] = dist[id] + 1
				next = append(next, prev)
			}
		}
		frontier = next
	}
	maxDist := 0
	for _, d := range dist {
		if d > maxDist {
			maxDist = d
		}
	}
	for d := 1; d <= maxDist; d++ {
		values := map[string]bool{}
		for id, idDist := range dist {
			if idDist != d {
				continue
			}
			if st := byStep[id]; st != nil && st.Status == "succeeded" {
				if v := outputString(st.OutputJSON, key); v != "" {
					values[v] = true
				}
			}
		}
		if len(values) == 1 {
			for v := range values {
				return v, nil
			}
		}
		if len(values) > 1 {
			return "", fmt.Errorf("ambiguous %s inherited from multiple upstream steps; set it explicitly", key)
		}
	}
	return "", nil
}

func (r *WorkflowRunner) resolveSequentialValue(ctx context.Context, runID, key string) (string, error) {
	steps, err := r.Store.ListStepRuns(ctx, runID)
	if err != nil {
		return "", err
	}
	for i := len(steps) - 1; i >= 0; i-- {
		st := &steps[i]
		if st.Status != "succeeded" {
			continue
		}
		if v := outputString(st.OutputJSON, key); v != "" {
			return v, nil
		}
	}
	return "", nil
}

func outputString(raw, key string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func (r *WorkflowRunner) createInlineCertificate(ctx context.Context, cfg ApplyStepConfig) (string, error) {
	primary, err := certificate.NormalizeDomain(cfg.PrimaryDomain)
	if err != nil {
		return "", err
	}
	if existing, err := r.Store.SearchCertificatesByDomain(ctx, primary); err == nil && existing != nil {
		if existing.Status == "revoked" || existing.Status == "disabled" {
			return "", fmt.Errorf("certificate %s for %s is %s", existing.ID, primary, existing.Status)
		}
		return existing.ID, nil
	}
	autoRenew := true
	if cfg.AutoRenew != nil {
		autoRenew = *cfg.AutoRenew
	}
	c, err := r.Service.CreateCertificate(ctx, CertInput{
		Name: cfg.Name, PrimaryDomain: cfg.PrimaryDomain, SAN: cfg.SAN, KeyType: cfg.KeyType,
		CAAccountID: cfg.CAAccountID, ChallengeType: cfg.ChallengeType,
		DNSCredentialID: cfg.DNSCredentialID, Webroot: cfg.Webroot, AutoRenew: autoRenew,
	})
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

func groupSteps(steps []WorkflowStep) [][]WorkflowStep {
	var groups [][]WorkflowStep
	last := ""
	for _, step := range steps {
		if step.ParallelGroup != "" && step.ParallelGroup == last {
			groups[len(groups)-1] = append(groups[len(groups)-1], step)
			continue
		}
		groups = append(groups, []WorkflowStep{step})
		last = step.ParallelGroup
	}
	return groups
}

func renderWorkflowText(tpl string, run *store.WorkflowRun, step WorkflowStep) string {
	r := strings.NewReplacer(
		"{{run_id}}", run.ID,
		"{{workflow_id}}", run.WorkflowID,
		"{{step_id}}", step.ID,
		"{{trigger}}", run.TriggerType,
	)
	return r.Replace(tpl)
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
