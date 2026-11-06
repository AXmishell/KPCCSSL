package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

const (
	stepMaxAttempts   = 3
	stepRetryBaseSecs = 15
	stepRetryMaxSecs  = 300
)

// WorkflowRunner advances durable workflow runs. It does not execute acme.sh
// itself: apply/deploy steps enqueue the existing jobs and only observe them.
type WorkflowRunner struct {
	Store   *store.Store
	Service *Service
	Owner   string
}

// NewWorkflowRunner creates a runner. The owner ID prevents two processes that
// share the database (serve + worker) from advancing the same run.
func NewWorkflowRunner(s *Service) *WorkflowRunner {
	return &WorkflowRunner{Store: s.Store, Service: s, Owner: id.New()}
}

// Run blocks until ctx is canceled.
func (r *WorkflowRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	recoverTicker := time.NewTicker(30 * time.Second)
	defer recoverTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < 8; i++ {
				progressed, err := r.RunOnce(ctx)
				if err != nil {
					slog.Warn("workflow runner", "error", err)
				}
				if !progressed {
					break
				}
			}
		case <-recoverTicker.C:
			if n, err := r.Store.RecoverWorkflowRuns(ctx, store.Now()-120); err != nil {
				slog.Warn("workflow watchdog", "error", err)
			} else if n > 0 {
				slog.Warn("recovered stale workflow runs", "count", n)
			}
		}
	}
}

// RunOnce advances running workflows and claims one queued run.
func (r *WorkflowRunner) RunOnce(ctx context.Context) (bool, error) {
	runs, err := r.Store.ListRunningWorkflowRuns(ctx, r.Owner)
	if err != nil {
		return false, err
	}
	progressed := false
	for i := range runs {
		finished, err := r.advance(ctx, &runs[i])
		if err != nil {
			return progressed, err
		}
		if finished {
			progressed = true
		}
	}
	run, err := r.Store.ClaimNextWorkflowRun(ctx, store.Now(), r.Owner)
	if err != nil {
		return progressed, err
	}
	if run == nil {
		return progressed, nil
	}
	_, err = r.advance(ctx, run)
	return true, err
}

func (r *WorkflowRunner) advance(ctx context.Context, run *store.WorkflowRun) (bool, error) {
	_ = r.Store.HeartbeatWorkflowRun(ctx, run.ID, store.Now())
	// Cancel may race with a claimed run. Never create steps or jobs after the
	// run has left the running state.
	if !r.runActive(ctx, run.ID) {
		return true, nil
	}
	logw := r.Service.Logs.Writer(run.ID)
	var def WorkflowDefinition
	if err := json.Unmarshal([]byte(run.DefinitionJSON), &def); err != nil {
		msg := "invalid workflow definition: " + err.Error()
		_ = r.Store.FinishWorkflowRun(ctx, run.ID, "failed", &msg, store.Now())
		r.Service.Logs.Close(run.ID)
		return true, nil
	}
	if len(def.Nodes) > 0 {
		return r.advanceGraph(ctx, run, &def, logw)
	}
	stepRuns, err := r.Store.ListStepRuns(ctx, run.ID)
	if err != nil {
		return false, err
	}
	if len(stepRuns) < len(def.Steps) {
		if err := r.ensureStepRuns(ctx, run, &def); err != nil {
			return false, err
		}
		stepRuns, err = r.Store.ListStepRuns(ctx, run.ID)
		if err != nil {
			return false, err
		}
	}
	byStep := map[string]*store.WorkflowStepRun{}
	for i := range stepRuns {
		byStep[stepRuns[i].StepID] = &stepRuns[i]
	}
	groups := groupSteps(def.Steps)
	hadBlockingFailure := false
	anyFailure := false
	anyBlockingFailure := false
	pending := false
	for _, group := range groups {
		when := group[0].When
		if when == "" {
			when = "success"
		}
		if (when == "success" && hadBlockingFailure) || (when == "failure" && !hadBlockingFailure) {
			for _, step := range group {
				st := byStep[step.ID]
				if st != nil && (st.Status == "pending" || st.Status == "running") {
					_ = r.Store.FinishStepRun(ctx, st.ID, "skipped", "{}", nil, store.Now())
				}
			}
			continue
		}

		groupFailed := false
		groupBlockingFailure := false
		groupRunning := false
		var pendingSteps []WorkflowStep
		for _, step := range group {
			st := byStep[step.ID]
			if st == nil {
				continue
			}
			if st.Status == "running" {
				var jobIDs []string
				_ = json.Unmarshal([]byte(st.JobIDsJSON), &jobIDs)
				var done bool
				var err error
				if len(jobIDs) > 0 {
					done, err = r.checkStepJobs(ctx, run, st)
				} else {
					// Crash between StartStepRun and SetStepJobs, or a wait
					// step: re-enter the executor. Job-backed steps are
					// idempotent through their workflow idempotency key.
					done, err = r.executeStep(ctx, run, step, st, logw, &def, byStep)
				}
				if err != nil {
					return false, err
				}
				if !done {
					groupRunning = true
					continue
				}
				// Re-read the updated row.
				st, _ = r.Store.GetStepRun(ctx, run.ID, step.ID)
				if st != nil {
					byStep[step.ID] = st
				}
			}
			if st == nil {
				continue
			}
			switch st.Status {
			case "failed":
				groupFailed = true
				anyFailure = true
				if !step.ContinueOnError {
					groupBlockingFailure = true
					anyBlockingFailure = true
				}
			case "pending":
				if st.AvailableAt > store.Now() {
					pending = true
					continue
				}
				pendingSteps = append(pendingSteps, step)
			}
		}
		if groupRunning {
			// A downstream group must not start until this group finishes.
			return false, nil
		}
		if pending {
			// A step is waiting for its retry backoff. Downstream groups must
			// wait as well, otherwise a success branch could run before the
			// retried step finishes.
			return false, nil
		}
		if groupFailed {
			if groupBlockingFailure {
				hadBlockingFailure = true
			}
			continue
		}
		if len(pendingSteps) > 0 {
			for _, step := range pendingSteps {
				st := byStep[step.ID]
				if st == nil {
					continue
				}
				if !r.runActive(ctx, run.ID) {
					return true, nil
				}
				if err := r.Store.StartStepRun(ctx, st.ID, store.Now()); err != nil {
					return false, err
				}
				if cur, _ := r.Store.GetStepRun(ctx, run.ID, step.ID); cur != nil {
					byStep[step.ID] = cur
					st = cur
				}
				fmt.Fprintf(logw, "=== step %s (%s) ===\n", step.ID, step.Type)
				done, err := r.executeStep(ctx, run, step, st, logw, &def, byStep)
				if err != nil {
					retried, retryErr := r.retryOrFailStep(ctx, step, st, err, logw)
					if retryErr != nil {
						return false, retryErr
					}
					if retried {
						return false, nil
					}
					groupFailed = true
					anyFailure = true
					if !step.ContinueOnError {
						groupBlockingFailure = true
						anyBlockingFailure = true
					}
					continue
				}
				if !done {
					pending = true
					continue
				}
				// A synchronous step may have finished inside executeStep.
				cur, _ := r.Store.GetStepRun(ctx, run.ID, step.ID)
				if cur != nil {
					byStep[step.ID] = cur
					if cur.Status == "failed" {
						groupFailed = true
						anyFailure = true
						if !step.ContinueOnError {
							groupBlockingFailure = true
							anyBlockingFailure = true
						}
					}
				}
			}
			if groupFailed && groupBlockingFailure {
				hadBlockingFailure = true
			}
			if pending {
				return false, nil
			}
			continue
		}
	}
	if pending {
		return false, nil
	}
	status := "succeeded"
	var runErr *string
	if anyBlockingFailure {
		status = "failed"
		msg := "one or more workflow steps failed"
		runErr = &msg
	} else if anyFailure {
		status = "partial"
		msg := "one or more non-blocking workflow steps failed"
		runErr = &msg
	}
	if err := r.Store.FinishWorkflowRun(ctx, run.ID, status, runErr, store.Now()); err != nil {
		return false, err
	}
	fmt.Fprintf(logw, "=== workflow %s ===\n", status)
	r.Service.Logs.Close(run.ID)
	return true, nil
}

type graphReadiness int

const (
	graphWait graphReadiness = iota
	graphNodeReady
	graphSkip
)

// advanceGraph executes a node/edge graph. A node becomes ready when every
// upstream node is terminal and every incoming edge condition is satisfied.
func (r *WorkflowRunner) advanceGraph(ctx context.Context, run *store.WorkflowRun, def *WorkflowDefinition, logw io.Writer) (bool, error) {
	if !r.runActive(ctx, run.ID) {
		return true, nil
	}
	stepRuns, err := r.Store.ListStepRuns(ctx, run.ID)
	if err != nil {
		return false, err
	}
	if len(stepRuns) < len(def.Nodes) {
		if err := r.ensureGraphStepRuns(ctx, run, def); err != nil {
			return false, err
		}
		stepRuns, err = r.Store.ListStepRuns(ctx, run.ID)
		if err != nil {
			return false, err
		}
	}
	byStep := map[string]*store.WorkflowStepRun{}
	for i := range stepRuns {
		byStep[stepRuns[i].StepID] = &stepRuns[i]
	}
	incoming := map[string][]WorkflowEdge{}
	for _, e := range def.Edges {
		incoming[e.To] = append(incoming[e.To], e)
	}
	continueOnError := map[string]bool{}
	for _, n := range def.Nodes {
		continueOnError[n.ID] = n.ContinueOnError
	}
	anyRunning := false
	for iter := 0; iter < 100; iter++ {
		progress := false
		for _, n := range def.Nodes {
			st := byStep[n.ID]
			if st == nil || st.Status != "running" {
				continue
			}
			var jobIDs []string
			_ = json.Unmarshal([]byte(st.JobIDsJSON), &jobIDs)
			step := nodeToStep(n)
			var done bool
			var err error
			if len(jobIDs) > 0 {
				done, err = r.checkStepJobs(ctx, run, st)
			} else {
				done, err = r.executeStep(ctx, run, step, st, logw, def, byStep)
			}
			if err != nil {
				return false, err
			}
			if !done {
				anyRunning = true
				continue
			}
			if cur, _ := r.Store.GetStepRun(ctx, run.ID, n.ID); cur != nil {
				byStep[n.ID] = cur
			}
			progress = true
		}
		for _, n := range def.Nodes {
			st := byStep[n.ID]
			if st == nil || st.Status != "pending" {
				continue
			}
			switch evaluateGraphNode(incoming[n.ID], byStep, continueOnError) {
			case graphSkip:
				_ = r.Store.FinishStepRun(ctx, st.ID, "skipped", "{}", nil, store.Now())
				if cur, _ := r.Store.GetStepRun(ctx, run.ID, n.ID); cur != nil {
					byStep[n.ID] = cur
				}
				progress = true
			case graphWait:
				continue
			case graphNodeReady:
				if st.AvailableAt > store.Now() {
					anyRunning = true
					continue
				}
				if !r.runActive(ctx, run.ID) {
					return true, nil
				}
				if err := r.Store.StartStepRun(ctx, st.ID, store.Now()); err != nil {
					return false, err
				}
				if cur, _ := r.Store.GetStepRun(ctx, run.ID, n.ID); cur != nil {
					byStep[n.ID] = cur
					st = cur
				}
				step := nodeToStep(n)
				fmt.Fprintf(logw, "=== node %s (%s) ===\n", n.ID, n.Type)
				done, err := r.executeStep(ctx, run, step, st, logw, def, byStep)
				if err != nil {
					retried, retryErr := r.retryOrFailStep(ctx, step, st, err, logw)
					if retryErr != nil {
						return false, retryErr
					}
					if retried {
						return false, nil
					}
					progress = true
					continue
				}
				if !done {
					anyRunning = true
					continue
				}
				if cur, _ := r.Store.GetStepRun(ctx, run.ID, n.ID); cur != nil {
					byStep[n.ID] = cur
				}
				progress = true
			}
		}
		if anyRunning {
			return false, nil
		}
		if !progress {
			break
		}
	}
	for _, n := range def.Nodes {
		st := byStep[n.ID]
		if st != nil && (st.Status == "pending" || st.Status == "running") {
			return false, nil
		}
	}
	anyFailure := false
	blockingFailure := false
	for _, n := range def.Nodes {
		st := byStep[n.ID]
		if st != nil && st.Status == "failed" {
			anyFailure = true
			if !n.ContinueOnError {
				blockingFailure = true
			}
		}
	}
	status := "succeeded"
	var runErr *string
	if blockingFailure {
		status = "failed"
		msg := "one or more workflow nodes failed"
		runErr = &msg
	} else if anyFailure {
		status = "partial"
		msg := "one or more non-blocking workflow nodes failed"
		runErr = &msg
	}
	if err := r.Store.FinishWorkflowRun(ctx, run.ID, status, runErr, store.Now()); err != nil {
		return false, err
	}
	fmt.Fprintf(logw, "=== workflow %s ===\n", status)
	r.Service.Logs.Close(run.ID)
	return true, nil
}

func evaluateGraphNode(edges []WorkflowEdge, steps map[string]*store.WorkflowStepRun, continueOnError map[string]bool) graphReadiness {
	if len(edges) == 0 {
		return graphNodeReady
	}
	// Wait until every upstream source is terminal before deciding whether an
	// unsatisfied condition means "skip".
	for _, e := range edges {
		src := steps[e.From]
		if src == nil || src.Status == "pending" || src.Status == "running" {
			return graphWait
		}
		if src.Status != "succeeded" && src.Status != "failed" && src.Status != "skipped" && src.Status != "canceled" {
			return graphWait
		}
	}
	for _, e := range edges {
		src := steps[e.From]
		soft := continueOnError[e.From]
		condition := e.Condition
		if condition == "" {
			condition = "success"
		}
		satisfied := false
		switch condition {
		case "success":
			// continue_on_error means "keep going after this failure": success
			// edges stay usable, matching the legacy ordered-step runner.
			satisfied = src.Status == "succeeded" || (src.Status == "failed" && soft)
		case "failure":
			// A non-blocking failure does not open failure branches. This also
			// matches the legacy semantics where only blocking failures change
			// the flow.
			satisfied = src.Status == "failed" && !soft
		case "always":
			satisfied = true
		}
		if !satisfied {
			return graphSkip
		}
	}
	return graphNodeReady
}

func (r *WorkflowRunner) runActive(ctx context.Context, runID string) bool {
	run, err := r.Store.GetWorkflowRun(ctx, runID)
	return err == nil && run.Status == "running"
}

// retryOrFailStep applies a small backoff for transient errors (for example a
// certificate operation that is already in progress) and otherwise marks the
// step failed. It returns true when a retry was scheduled.
func (r *WorkflowRunner) retryOrFailStep(ctx context.Context, step WorkflowStep, st *store.WorkflowStepRun, err error, logw io.Writer) (bool, error) {
	msg := job.Redact(err.Error())
	attempt := st.Attempts
	if attempt < 1 {
		attempt = 1
	}
	if attempt < stepMaxAttempts && retryableStepError(err) {
		delay := stepRetryBaseSecs << (attempt - 1)
		if delay > stepRetryMaxSecs {
			delay = stepRetryMaxSecs
		}
		availableAt := store.Now() + int64(delay)
		if retryErr := r.Store.RetryStepRun(ctx, st.ID, availableAt, &msg); retryErr != nil {
			return false, retryErr
		}
		fmt.Fprintf(logw, "=== step %s retry %d/%d in %ds: %s ===\n", step.ID, attempt, stepMaxAttempts, delay, msg)
		return true, nil
	}
	_ = r.Store.FinishStepRun(ctx, st.ID, "failed", st.OutputJSON, &msg, store.Now())
	fmt.Fprintf(logw, "=== step failed: %s ===\n", msg)
	return false, nil
}

func retryableStepError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"another operation is in progress",
		"rate limit",
		"timeout",
		"timed out",
		"connection",
		"temporarily",
		"try again",
		"already exists",
		"database is locked",
		"database is busy",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (r *WorkflowRunner) ensureGraphStepRuns(ctx context.Context, run *store.WorkflowRun, def *WorkflowDefinition) error {
	if !r.runActive(ctx, run.ID) {
		return nil
	}
	for _, n := range def.Nodes {
		name := n.Name
		if name == "" {
			name = n.Type + " " + n.ID
		}
		if err := r.Store.CreateStepRun(ctx, &store.WorkflowStepRun{
			ID: id.New(), RunID: run.ID, StepID: n.ID, Type: n.Type, Name: name,
			Status: "pending", CreatedAt: store.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func nodeToStep(n WorkflowNode) WorkflowStep {
	return WorkflowStep{
		ID: n.ID, Type: n.Type, Name: n.Name, Config: n.Config,
		ContinueOnError: n.ContinueOnError,
	}
}

func (r *WorkflowRunner) ensureStepRuns(ctx context.Context, run *store.WorkflowRun, def *WorkflowDefinition) error {
	if !r.runActive(ctx, run.ID) {
		return nil
	}
	for _, step := range def.Steps {
		name := step.Name
		if name == "" {
			name = step.Type + " " + step.ID
		}
		if err := r.Store.CreateStepRun(ctx, &store.WorkflowStepRun{
			ID: id.New(), RunID: run.ID, StepID: step.ID, Type: step.Type, Name: name,
			Status: "pending", CreatedAt: store.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *WorkflowRunner) checkStepJobs(ctx context.Context, run *store.WorkflowRun, st *store.WorkflowStepRun) (bool, error) {
	var jobIDs []string
	_ = json.Unmarshal([]byte(st.JobIDsJSON), &jobIDs)
	if len(jobIDs) == 0 {
		return false, nil
	}
	var lastErr string
	var firstJob *store.Job
	for _, jobID := range jobIDs {
		j, err := r.Store.GetJob(ctx, jobID)
		if err != nil {
			lastErr = "job " + jobID + " disappeared"
			break
		}
		if firstJob == nil {
			firstJob = j
		}
		switch j.Status {
		case "succeeded":
			continue
		case "queued", "running":
			return false, nil
		default:
			if j.ErrorMessage != nil && *j.ErrorMessage != "" {
				lastErr = *j.ErrorMessage
			} else {
				lastErr = "job " + jobID + " " + j.Status
			}
		}
	}
	if lastErr != "" {
		msg := job.Redact(lastErr)
		_ = r.Store.FinishStepRun(ctx, st.ID, "failed", st.OutputJSON, &msg, store.Now())
		return true, nil
	}
	output := st.OutputJSON
	if st.Type == StepApply && firstJob != nil && firstJob.CertificateID != nil {
		out := map[string]any{"certificate_id": *firstJob.CertificateID, "job_id": firstJob.ID}
		// Pin the revision produced by this apply so a later deploy/verify does
		// not race with a concurrent renewal that changes the current revision.
		if c, err := r.Store.GetCertificate(ctx, *firstJob.CertificateID); err == nil && c.CurrentRevisionID != nil {
			out["revision_id"] = *c.CurrentRevisionID
		}
		output = mustJSON(out)
	}
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", output, nil, store.Now())
	return true, nil
}
