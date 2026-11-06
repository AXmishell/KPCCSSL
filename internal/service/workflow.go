package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

// Workflow step types. The execution model intentionally keeps the leaf
// operations on the existing durable job queue; only the orchestration layer is
// new.
const (
	StepApply  = "apply"
	StepDeploy = "deploy"
	StepNotify = "notify"
	StepVerify = "verify"
	StepWait   = "wait"
)

// WorkflowDefinition is the editable, versioned pipeline document. Version 1
// used an ordered Steps list; version 2 is a node/edge graph. The runner keeps
// both for backwards compatibility.
type WorkflowDefinition struct {
	Version int            `json:"version"`
	Steps   []WorkflowStep `json:"steps,omitempty"`
	Nodes   []WorkflowNode `json:"nodes,omitempty"`
	Edges   []WorkflowEdge `json:"edges,omitempty"`
}

// WorkflowNode is a graph node. Position is UI-only and is preserved in the
// definition snapshot for later editing.
type WorkflowNode struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	Name            string            `json:"name,omitempty"`
	Config          json.RawMessage   `json:"config,omitempty"`
	Position        *WorkflowPosition `json:"position,omitempty"`
	ContinueOnError bool              `json:"continue_on_error,omitempty"`
}

type WorkflowPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowEdge connects two nodes. Condition is success (default), failure or
// always.
type WorkflowEdge struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// WorkflowStep is one node in a workflow. Steps with the same non-empty
// ParallelGroup are started together; When controls success/failure branches.
type WorkflowStep struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Name            string          `json:"name,omitempty"`
	Config          json.RawMessage `json:"config,omitempty"`
	When            string          `json:"when,omitempty"` // success | failure | always
	ParallelGroup   string          `json:"parallel_group,omitempty"`
	ContinueOnError bool            `json:"continue_on_error,omitempty"`
}

// WorkflowInput is the create/update payload.
type WorkflowInput struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Definition  WorkflowDefinition `json:"definition"`
	Enabled     bool               `json:"enabled"`
	TriggerType string             `json:"trigger_type"`
	Schedule    string             `json:"schedule"`
}

// WorkflowView is the public workflow representation.
type WorkflowView struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Definition  WorkflowDefinition `json:"definition"`
	Enabled     bool               `json:"enabled"`
	TriggerType string             `json:"trigger_type"`
	Schedule    string             `json:"schedule"`
	CreatedAt   int64              `json:"created_at"`
	UpdatedAt   int64              `json:"updated_at"`
	LastRun     *store.WorkflowRun `json:"last_run,omitempty"`
}

// WorkflowStepRunView includes decoded job IDs and output for the UI.
type WorkflowStepRunView struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	StepID     string          `json:"step_id"`
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Attempts   int             `json:"attempts"`
	JobIDs     []string        `json:"job_ids"`
	Output     json.RawMessage `json:"output"`
	Error      *string         `json:"error"`
	StartedAt  *int64          `json:"started_at"`
	FinishedAt *int64          `json:"finished_at"`
}

// WorkflowRunDetail is the run plus its step timeline.
type WorkflowRunDetail struct {
	Run    *store.WorkflowRun    `json:"run"`
	Steps  []WorkflowStepRunView `json:"steps"`
	LogURL string                `json:"log_url"`
}

// ApplyStepConfig supports both an existing certificate and inline creation.
type ApplyStepConfig struct {
	CertificateID   string   `json:"certificate_id,omitempty"`
	PrimaryDomain   string   `json:"primary_domain,omitempty"`
	Name            string   `json:"name,omitempty"`
	SAN             []string `json:"san,omitempty"`
	KeyType         string   `json:"key_type,omitempty"`
	CAAccountID     string   `json:"ca_account_id,omitempty"`
	ChallengeType   string   `json:"challenge_type,omitempty"`
	DNSCredentialID string   `json:"dns_credential_id,omitempty"`
	Webroot         string   `json:"webroot,omitempty"`
	AutoRenew       *bool    `json:"auto_renew,omitempty"`
	Mode            string   `json:"mode,omitempty"` // issue | renew | auto
	Force           bool     `json:"force,omitempty"`
}

// DeployStepConfig inherits the certificate from a previous apply step when
// CertificateID is empty.
type DeployStepConfig struct {
	CertificateID string `json:"certificate_id,omitempty"`
	RevisionID    string `json:"revision_id,omitempty"`
}

type NotifyStepConfig struct {
	Event string `json:"event,omitempty"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type VerifyStepConfig struct {
	CertificateID string `json:"certificate_id,omitempty"`
	MinDays       int    `json:"min_days,omitempty"`
}

type WaitStepConfig struct {
	Seconds int `json:"seconds"`
}

// ListWorkflows returns workflows with their latest run.
func (s *Service) ListWorkflows(ctx context.Context) ([]WorkflowView, error) {
	rows, err := s.Store.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowView, 0, len(rows))
	for i := range rows {
		v, err := s.workflowView(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// GetWorkflow returns one workflow.
func (s *Service) GetWorkflow(ctx context.Context, id string) (*WorkflowView, error) {
	w, err := s.Store.GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.workflowView(ctx, w)
}

// CreateWorkflow validates and stores a workflow.
func (s *Service) CreateWorkflow(ctx context.Context, in WorkflowInput) (*WorkflowView, error) {
	name, desc, definitionJSON, enabled, trigger, schedule, err := s.normalizeWorkflow(in)
	if err != nil {
		return nil, err
	}
	now := store.Now()
	w := &store.Workflow{
		ID: id.New(), Name: name, Description: desc, DefinitionJSON: definitionJSON,
		Enabled: enabled, TriggerType: trigger, Schedule: schedule, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateWorkflow(ctx, w); err != nil {
		return nil, err
	}
	return s.workflowView(ctx, w)
}

// UpdateWorkflow replaces a workflow definition.
func (s *Service) UpdateWorkflow(ctx context.Context, id string, in WorkflowInput) (*WorkflowView, error) {
	existing, err := s.Store.GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	name, desc, definitionJSON, enabled, trigger, schedule, err := s.normalizeWorkflow(in)
	if err != nil {
		return nil, err
	}
	existing.Name, existing.Description, existing.DefinitionJSON = name, desc, definitionJSON
	existing.Enabled, existing.TriggerType, existing.Schedule = enabled, trigger, schedule
	existing.UpdatedAt = store.Now()
	if err := s.Store.UpdateWorkflow(ctx, existing); err != nil {
		return nil, err
	}
	return s.workflowView(ctx, existing)
}

// DeleteWorkflow removes a definition and all of its runs.
func (s *Service) DeleteWorkflow(ctx context.Context, id string) error {
	return s.Store.DeleteWorkflow(ctx, id)
}

// RunWorkflow queues a manual workflow run.
func (s *Service) RunWorkflow(ctx context.Context, id string) (*store.WorkflowRun, error) {
	w, err := s.Store.GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.enqueueWorkflowRun(ctx, w, "manual")
}

// CancelWorkflowRun cancels a pending/running workflow run.
func (s *Service) CancelWorkflowRun(ctx context.Context, id string) error {
	if err := s.Store.CancelWorkflowRun(ctx, id, store.Now()); err != nil {
		return err
	}
	s.Logs.Close(id)
	return nil
}

// ListWorkflowRuns returns recent runs.
func (s *Service) ListWorkflowRuns(ctx context.Context, workflowID string, limit int) ([]store.WorkflowRun, error) {
	return s.Store.ListWorkflowRuns(ctx, workflowID, limit)
}

// GetWorkflowRunDetail returns a run and its step timeline.
func (s *Service) GetWorkflowRunDetail(ctx context.Context, id string) (*WorkflowRunDetail, error) {
	run, err := s.Store.GetWorkflowRun(ctx, id)
	if err != nil {
		return nil, err
	}
	steps, err := s.Store.ListStepRuns(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowStepRunView, 0, len(steps))
	for i := range steps {
		out = append(out, stepRunView(&steps[i]))
	}
	return &WorkflowRunDetail{Run: run, Steps: out, LogURL: "/api/v1/workflow-runs/" + run.ID + "/log"}, nil
}

func (s *Service) workflowView(ctx context.Context, w *store.Workflow) (*WorkflowView, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal([]byte(w.DefinitionJSON), &def); err != nil {
		return nil, fmt.Errorf("workflow %s: invalid definition: %w", w.ID, err)
	}
	v := &WorkflowView{
		ID: w.ID, Name: w.Name, Description: w.Description, Definition: def,
		Enabled: w.Enabled, TriggerType: w.TriggerType, Schedule: w.Schedule,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
	runs, err := s.Store.ListWorkflowRuns(ctx, w.ID, 1)
	if err == nil && len(runs) > 0 {
		v.LastRun = &runs[0]
	}
	return v, nil
}

func (s *Service) normalizeWorkflow(in WorkflowInput) (name, desc, definitionJSON string, enabled bool, trigger, schedule string, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "\r\n\x00") {
		return "", "", "", false, "", "", errors.New("workflow name must be 1-100 characters")
	}
	desc = strings.TrimSpace(in.Description)
	if len(desc) > 500 || strings.ContainsAny(desc, "\x00") {
		return "", "", "", false, "", "", errors.New("workflow description is too long")
	}
	def := in.Definition
	if def.Version == 0 {
		if len(def.Nodes) > 0 {
			def.Version = 2
		} else {
			def.Version = 1
		}
	}
	if err := validateWorkflowDefinition(&def); err != nil {
		return "", "", "", false, "", "", err
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return "", "", "", false, "", "", err
	}
	trigger = in.TriggerType
	if trigger == "" {
		trigger = "manual"
	}
	if trigger != "manual" && trigger != "schedule" {
		return "", "", "", false, "", "", errors.New("trigger_type must be manual or schedule")
	}
	if trigger == "schedule" {
		schedule, err = normalizeSchedule(in.Schedule)
		if err != nil {
			return "", "", "", false, "", "", err
		}
	} else {
		schedule = ""
	}
	return name, desc, string(raw), in.Enabled, trigger, schedule, nil
}

func validateWorkflowDefinition(def *WorkflowDefinition) error {
	if len(def.Nodes) > 0 {
		if len(def.Steps) > 0 {
			return errors.New("workflow cannot mix steps and nodes")
		}
		return validateWorkflowGraph(def)
	}
	if len(def.Steps) == 0 {
		return errors.New("workflow must contain at least one step or node")
	}
	if len(def.Steps) > 50 {
		return errors.New("workflow contains too many steps")
	}
	ids := map[string]bool{}
	groups := map[string]string{}
	for i := range def.Steps {
		st := &def.Steps[i]
		st.ID = strings.TrimSpace(st.ID)
		if st.ID == "" || len(st.ID) > 64 {
			return fmt.Errorf("step %d: id must be 1-64 characters", i+1)
		}
		if ids[st.ID] {
			return fmt.Errorf("duplicate step id %q", st.ID)
		}
		ids[st.ID] = true
		switch st.Type {
		case StepApply, StepDeploy, StepNotify, StepVerify, StepWait:
		default:
			return fmt.Errorf("step %q: unsupported type %q", st.ID, st.Type)
		}
		switch st.When {
		case "", "success", "failure", "always":
		default:
			return fmt.Errorf("step %q: when must be success, failure or always", st.ID)
		}
		if st.ParallelGroup != "" {
			if len(st.ParallelGroup) > 64 {
				return fmt.Errorf("step %q: parallel_group is too long", st.ID)
			}
			if prev, ok := groups[st.ParallelGroup]; ok && prev != st.When {
				return fmt.Errorf("steps in parallel group %q must use the same when value", st.ParallelGroup)
			}
			groups[st.ParallelGroup] = st.When
		}
		if err := validateStepConfig(*st); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
	}
	return nil
}

func validateWorkflowGraph(def *WorkflowDefinition) error {
	if len(def.Nodes) == 0 {
		return errors.New("workflow must contain at least one node")
	}
	if len(def.Nodes) > 100 {
		return errors.New("workflow contains too many nodes")
	}
	nodes := map[string]bool{}
	for i := range def.Nodes {
		n := &def.Nodes[i]
		n.ID = strings.TrimSpace(n.ID)
		if n.ID == "" || len(n.ID) > 64 {
			return fmt.Errorf("node %d: id must be 1-64 characters", i+1)
		}
		if nodes[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		nodes[n.ID] = true
		switch n.Type {
		case StepApply, StepDeploy, StepNotify, StepVerify, StepWait:
		default:
			return fmt.Errorf("node %q: unsupported type %q", n.ID, n.Type)
		}
		if err := validateStepConfig(WorkflowStep{ID: n.ID, Type: n.Type, Config: n.Config}); err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
	}
	incoming := map[string]int{}
	edgeIDs := map[string]bool{}
	for i := range def.Edges {
		e := &def.Edges[i]
		e.ID = strings.TrimSpace(e.ID)
		if e.ID == "" {
			e.ID = fmt.Sprintf("edge-%d", i+1)
		}
		if edgeIDs[e.ID] {
			return fmt.Errorf("duplicate edge id %q", e.ID)
		}
		edgeIDs[e.ID] = true
		if !nodes[e.From] || !nodes[e.To] {
			return fmt.Errorf("edge %q references an unknown node", e.ID)
		}
		if e.From == e.To {
			return fmt.Errorf("edge %q cannot connect a node to itself", e.ID)
		}
		if e.Condition == "" {
			e.Condition = "success"
		}
		switch e.Condition {
		case "success", "failure", "always":
		default:
			return fmt.Errorf("edge %q: condition must be success, failure or always", e.ID)
		}
		incoming[e.To]++
	}
	start := false
	for id := range nodes {
		if incoming[id] == 0 {
			start = true
			break
		}
	}
	if !start {
		return errors.New("workflow graph has no start node")
	}
	if hasCycle(def) {
		return errors.New("workflow graph contains a cycle")
	}
	return nil
}

func hasCycle(def *WorkflowDefinition) bool {
	edges := map[string][]string{}
	for _, e := range def.Edges {
		edges[e.From] = append(edges[e.From], e.To)
	}
	state := map[string]int{} // 0=unvisited, 1=visiting, 2=done
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for _, n := range def.Nodes {
		if visit(n.ID) {
			return true
		}
	}
	return false
}

func validateStepConfig(st WorkflowStep) error {
	switch st.Type {
	case StepApply:
		var cfg ApplyStepConfig
		if len(st.Config) > 0 {
			if err := json.Unmarshal(st.Config, &cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}
		}
		if cfg.CertificateID == "" && cfg.PrimaryDomain == "" {
			return errors.New("certificate_id or primary_domain is required")
		}
		if cfg.PrimaryDomain != "" {
			if cfg.KeyType == "" || cfg.CAAccountID == "" || cfg.ChallengeType == "" {
				return errors.New("inline certificate requires key_type, ca_account_id and challenge_type")
			}
		}
		switch cfg.Mode {
		case "", "auto", "issue", "renew":
		default:
			return errors.New("mode must be auto, issue or renew")
		}
	case StepDeploy:
		var cfg DeployStepConfig
		if len(st.Config) > 0 {
			if err := json.Unmarshal(st.Config, &cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}
		}
	case StepNotify:
		var cfg NotifyStepConfig
		if len(st.Config) > 0 {
			if err := json.Unmarshal(st.Config, &cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}
		}
		if cfg.Title == "" && cfg.Body == "" {
			return errors.New("notify requires title or body")
		}
	case StepVerify:
		var cfg VerifyStepConfig
		if len(st.Config) > 0 {
			if err := json.Unmarshal(st.Config, &cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}
		}
		if cfg.MinDays < 0 || cfg.MinDays > 365 {
			return errors.New("min_days must be between 0 and 365")
		}
	case StepWait:
		var cfg WaitStepConfig
		if err := json.Unmarshal(st.Config, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		if cfg.Seconds < 1 || cfg.Seconds > 86400 {
			return errors.New("seconds must be between 1 and 86400")
		}
	}
	return nil
}

// normalizeSchedule accepts human-readable schedules to avoid pulling a cron
// dependency into the single binary.
func normalizeSchedule(raw string) (string, error) {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	if len(parts) < 2 {
		return "", errors.New("schedule must be like 'daily 03:00', 'weekly 1 03:00' or 'monthly 1 03:00'")
	}
	switch parts[0] {
	case "daily":
		if len(parts) != 2 {
			return "", errors.New("daily schedule must be 'daily HH:MM'")
		}
	case "weekly":
		if len(parts) != 3 {
			return "", errors.New("weekly schedule must be 'weekly <0-6> HH:MM'")
		}
		if _, err := scheduleInt(parts[1], 0, 6); err != nil {
			return "", errors.New("weekday must be 0-6")
		}
	case "monthly":
		if len(parts) != 3 {
			return "", errors.New("monthly schedule must be 'monthly <1-31> HH:MM'")
		}
		if _, err := scheduleInt(parts[1], 1, 31); err != nil {
			return "", errors.New("day must be 1-31")
		}
	default:
		return "", errors.New("schedule kind must be daily, weekly or monthly")
	}
	if _, _, err := parseClock(parts[len(parts)-1]); err != nil {
		return "", err
	}
	return strings.Join(parts, " "), nil
}

func scheduleInt(raw string, min, max int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, errors.New("out of range")
	}
	return n, nil
}

func parseClock(raw string) (int, int, error) {
	hh, mm, ok := strings.Cut(raw, ":")
	if !ok {
		return 0, 0, errors.New("time must be HH:MM")
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 || h > 23 {
		return 0, 0, errors.New("hour must be 0-23")
	}
	m, err := strconv.Atoi(mm)
	if err != nil || m < 0 || m > 59 {
		return 0, 0, errors.New("minute must be 0-59")
	}
	return h, m, nil
}

func scheduleMatches(raw string, now time.Time) bool {
	parts := strings.Fields(raw)
	if len(parts) < 2 {
		return false
	}
	h, m, err := parseClock(parts[len(parts)-1])
	if err != nil || now.Hour() != h || now.Minute() != m {
		return false
	}
	switch parts[0] {
	case "daily":
		return true
	case "weekly":
		wd, err := strconv.Atoi(parts[1])
		return err == nil && int(now.Weekday()) == wd
	case "monthly":
		day, err := strconv.Atoi(parts[1])
		return err == nil && now.Day() == day
	}
	return false
}

func (s *Service) enqueueWorkflowRun(ctx context.Context, w *store.Workflow, trigger string) (*store.WorkflowRun, error) {
	now := store.Now()
	run := &store.WorkflowRun{
		ID: id.New(), WorkflowID: w.ID, Status: "queued", TriggerType: trigger,
		DefinitionJSON: w.DefinitionJSON, CreatedAt: now,
	}
	if err := s.Store.CreateWorkflowRun(ctx, run); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, &ConflictError{Msg: "workflow is already running"}
		}
		return nil, err
	}
	return run, nil
}

// RunScheduledWorkflows creates queued runs for enabled schedules. It is called
// once per minute by the scheduler.
func (s *Service) RunScheduledWorkflows(ctx context.Context) {
	workflows, err := s.Store.ListWorkflows(ctx)
	if err != nil {
		slog.Warn("workflow schedule scan failed", "error", err)
		return
	}
	now := time.Now()
	for i := range workflows {
		w := &workflows[i]
		if !w.Enabled || w.TriggerType != "schedule" || !scheduleMatches(w.Schedule, now) {
			continue
		}
		recent, err := s.Store.RecentWorkflowRun(ctx, w.ID, store.Now()-60)
		if err != nil || recent {
			continue
		}
		if _, err := s.enqueueWorkflowRun(ctx, w, "schedule"); err != nil {
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				slog.Warn("cannot queue scheduled workflow", "workflow", w.ID, "error", err)
			}
		}
	}
}

func stepRunView(st *store.WorkflowStepRun) WorkflowStepRunView {
	var jobIDs []string
	_ = json.Unmarshal([]byte(st.JobIDsJSON), &jobIDs)
	if jobIDs == nil {
		jobIDs = []string{}
	}
	output := json.RawMessage(st.OutputJSON)
	if len(output) == 0 {
		output = json.RawMessage("{}")
	}
	return WorkflowStepRunView{
		ID: st.ID, RunID: st.RunID, StepID: st.StepID, Type: st.Type, Name: st.Name,
		Status: st.Status, Attempts: st.Attempts, JobIDs: jobIDs, Output: output,
		Error: st.Error, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
	}
}

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
	if err := r.ensureStepRuns(ctx, run, &def); err != nil {
		return false, err
	}
	stepRuns, err := r.Store.ListStepRuns(ctx, run.ID)
	if err != nil {
		return false, err
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
					done, err = r.executeStep(ctx, run, step, st, logw)
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
				pendingSteps = append(pendingSteps, step)
			}
		}
		if groupRunning {
			// A downstream group must not start until this group finishes.
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
				if err := r.Store.StartStepRun(ctx, st.ID, store.Now()); err != nil {
					return false, err
				}
				fmt.Fprintf(logw, "=== step %s (%s) ===\n", step.ID, step.Type)
				done, err := r.executeStep(ctx, run, step, byStep[step.ID], logw)
				if err != nil {
					msg := job.Redact(err.Error())
					_ = r.Store.FinishStepRun(ctx, st.ID, "failed", "{}", &msg, store.Now())
					fmt.Fprintf(logw, "=== step failed: %s ===\n", msg)
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
	if err := r.ensureGraphStepRuns(ctx, run, def); err != nil {
		return false, err
	}
	stepRuns, err := r.Store.ListStepRuns(ctx, run.ID)
	if err != nil {
		return false, err
	}
	byStep := map[string]*store.WorkflowStepRun{}
	for i := range stepRuns {
		byStep[stepRuns[i].StepID] = &stepRuns[i]
	}
	incoming := map[string][]WorkflowEdge{}
	for _, e := range def.Edges {
		incoming[e.To] = append(incoming[e.To], e)
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
				done, err = r.executeStep(ctx, run, step, st, logw)
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
			switch evaluateGraphNode(incoming[n.ID], byStep) {
			case graphSkip:
				_ = r.Store.FinishStepRun(ctx, st.ID, "skipped", "{}", nil, store.Now())
				if cur, _ := r.Store.GetStepRun(ctx, run.ID, n.ID); cur != nil {
					byStep[n.ID] = cur
				}
				progress = true
			case graphWait:
				continue
			case graphNodeReady:
				if err := r.Store.StartStepRun(ctx, st.ID, store.Now()); err != nil {
					return false, err
				}
				step := nodeToStep(n)
				fmt.Fprintf(logw, "=== node %s (%s) ===\n", n.ID, n.Type)
				done, err := r.executeStep(ctx, run, step, st, logw)
				if err != nil {
					msg := job.Redact(err.Error())
					_ = r.Store.FinishStepRun(ctx, st.ID, "failed", "{}", &msg, store.Now())
					fmt.Fprintf(logw, "=== node failed: %s ===\n", msg)
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

func evaluateGraphNode(edges []WorkflowEdge, steps map[string]*store.WorkflowStepRun) graphReadiness {
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
		condition := e.Condition
		if condition == "" {
			condition = "success"
		}
		satisfied := false
		switch condition {
		case "success":
			satisfied = src.Status == "succeeded"
		case "failure":
			satisfied = src.Status == "failed"
		case "always":
			satisfied = true
		}
		if !satisfied {
			return graphSkip
		}
	}
	return graphNodeReady
}

func (r *WorkflowRunner) ensureGraphStepRuns(ctx context.Context, run *store.WorkflowRun, def *WorkflowDefinition) error {
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
		output = mustJSON(map[string]any{"certificate_id": *firstJob.CertificateID, "job_id": firstJob.ID})
	}
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", output, nil, store.Now())
	return true, nil
}

func (r *WorkflowRunner) executeStep(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer) (bool, error) {
	switch step.Type {
	case StepApply:
		return r.executeApply(ctx, run, step, st, logw)
	case StepDeploy:
		return r.executeDeploy(ctx, run, step, st, logw)
	case StepNotify:
		return r.executeNotify(ctx, run, step, st, logw)
	case StepVerify:
		return r.executeVerify(ctx, run, step, st, logw)
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
	certID := cfg.CertificateID
	if certID == "" {
		certID = r.inheritCertificateID(ctx, run.ID)
	}
	if certID == "" {
		var err error
		certID, err = r.createInlineCertificate(ctx, cfg)
		if err != nil {
			return false, err
		}
	}
	if certID == "" {
		return false, errors.New("no certificate configured for apply step")
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
	return false, nil
}

func (r *WorkflowRunner) executeDeploy(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer) (bool, error) {
	var cfg DeployStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	certID := cfg.CertificateID
	if certID == "" {
		certID = r.inheritCertificateID(ctx, run.ID)
	}
	if certID == "" {
		return false, errors.New("deploy step has no certificate_id and no preceding apply step")
	}
	key := "workflow:" + run.ID + ":" + step.ID
	var jobs []*store.Job
	var err error
	if cfg.RevisionID != "" {
		jobs, err = r.Service.DeployRevisionNow(ctx, certID, cfg.RevisionID, key)
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
	_ = r.Store.SetStepOutput(ctx, st.ID, mustJSON(map[string]any{"certificate_id": certID}))
	return len(ids) == 0, nil
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
	r.Service.Notify(ctx, event, "workflow:"+run.ID+":"+step.ID, title, body)
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", "{}", nil, store.Now())
	return true, nil
}

func (r *WorkflowRunner) executeVerify(ctx context.Context, run *store.WorkflowRun, step WorkflowStep, st *store.WorkflowStepRun, logw io.Writer) (bool, error) {
	var cfg VerifyStepConfig
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return false, err
		}
	}
	certID := cfg.CertificateID
	if certID == "" {
		certID = r.inheritCertificateID(ctx, run.ID)
	}
	if certID == "" {
		return false, errors.New("verify step has no certificate_id")
	}
	c, err := r.Store.GetCertificate(ctx, certID)
	if err != nil {
		return false, err
	}
	if c.CurrentRevisionID == nil {
		return false, errors.New("certificate has no issued revision")
	}
	rev, err := r.Store.GetRevision(ctx, *c.CurrentRevisionID)
	if err != nil {
		return false, err
	}
	if rev.NotAfter <= store.Now()+int64(cfg.MinDays)*86400 {
		msg := fmt.Sprintf("certificate expires at %s (minimum %d days required)", time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339), cfg.MinDays)
		_ = r.Store.FinishStepRun(ctx, st.ID, "failed", mustJSON(map[string]any{"certificate_id": certID}), &msg, store.Now())
		return true, nil
	}
	fmt.Fprintf(logw, "certificate %s verified, expires %s\n", certID, time.Unix(rev.NotAfter, 0).UTC().Format(time.RFC3339))
	_ = r.Store.FinishStepRun(ctx, st.ID, "succeeded", mustJSON(map[string]any{"certificate_id": certID, "revision_id": rev.ID, "not_after": rev.NotAfter}), nil, store.Now())
	return true, nil
}

func (r *WorkflowRunner) inheritCertificateID(ctx context.Context, runID string) string {
	steps, err := r.Store.ListStepRuns(ctx, runID)
	if err != nil {
		return ""
	}
	for i := len(steps) - 1; i >= 0; i-- {
		st := &steps[i]
		if st.Status != "succeeded" || len(st.OutputJSON) == 0 {
			continue
		}
		var out struct {
			CertificateID string `json:"certificate_id"`
		}
		if json.Unmarshal([]byte(st.OutputJSON), &out) == nil && out.CertificateID != "" {
			return out.CertificateID
		}
	}
	return ""
}

func (r *WorkflowRunner) createInlineCertificate(ctx context.Context, cfg ApplyStepConfig) (string, error) {
	primary, err := certificate.NormalizeDomain(cfg.PrimaryDomain)
	if err != nil {
		return "", err
	}
	if existing, err := r.Store.SearchCertificatesByDomain(ctx, primary); err == nil && existing != nil {
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
