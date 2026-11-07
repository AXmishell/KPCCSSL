package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/certificate"
	"github.com/axmishell/kpccssl/internal/id"
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
	// AllowMultipleStarts opts a graph into multiple root nodes. It is false by
	// default so an accidentally unconnected node cannot become a second start
	// node and run before the rest of the pipeline.
	AllowMultipleStarts bool `json:"allow_multiple_starts,omitempty"`
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
	// Enabled is optional. New workflows default to enabled; updates preserve
	// the current value when the field is omitted.
	Enabled     *bool  `json:"enabled,omitempty"`
	TriggerType string `json:"trigger_type"`
	Schedule    string `json:"schedule"`
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
	ID          string          `json:"id"`
	RunID       string          `json:"run_id"`
	StepID      string          `json:"step_id"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	JobIDs      []string        `json:"job_ids"`
	Output      json.RawMessage `json:"output"`
	Error       *string         `json:"error"`
	StartedAt   *int64          `json:"started_at"`
	FinishedAt  *int64          `json:"finished_at"`
	AvailableAt int64           `json:"available_at"`
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
// CertificateID is empty. DeploymentIDs limits the deploy to selected targets;
// empty means every enabled target.
type DeployStepConfig struct {
	CertificateID string   `json:"certificate_id,omitempty"`
	RevisionID    string   `json:"revision_id,omitempty"`
	DeploymentIDs []string `json:"deployment_ids,omitempty"`
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
	latest, err := s.Store.LatestWorkflowRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowView, 0, len(rows))
	for i := range rows {
		v, err := workflowViewFrom(&rows[i], latest[rows[i].ID])
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
	name, desc, definitionJSON, trigger, schedule, err := s.normalizeWorkflow(ctx, in)
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
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
	name, desc, definitionJSON, trigger, schedule, err := s.normalizeWorkflow(ctx, in)
	if err != nil {
		return nil, err
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	existing.Name, existing.Description, existing.DefinitionJSON = name, desc, definitionJSON
	existing.Enabled, existing.TriggerType, existing.Schedule = enabled, trigger, schedule
	existing.UpdatedAt = store.Now()
	if err := s.Store.UpdateWorkflow(ctx, existing); err != nil {
		return nil, err
	}
	return s.workflowView(ctx, existing)
}

// DeleteWorkflow removes a definition and all of its runs. An active run must
// be canceled first: deleting it would remove the run history while the runner
// and its jobs continue in the background.
func (s *Service) DeleteWorkflow(ctx context.Context, id string) error {
	active, err := s.Store.ActiveWorkflowRun(ctx, id)
	if err != nil {
		return err
	}
	if active {
		return &ConflictError{Msg: "workflow has a queued or running run; cancel it first"}
	}
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

// CancelWorkflowRun cancels a pending/running workflow run and any jobs that
// are still queued for it. Running jobs cannot be interrupted by the worker,
// so they are allowed to finish; no new steps will be started.
func (s *Service) CancelWorkflowRun(ctx context.Context, id string) error {
	now := store.Now()
	if err := s.Store.CancelWorkflowRun(ctx, id, now); err != nil {
		return err
	}
	if _, err := s.Store.CancelJobsByPrefix(ctx, "workflow:"+id+":", "canceled because the workflow run was canceled", now); err != nil {
		slog.Warn("cannot cancel queued workflow jobs", "run", id, "error", err)
	}
	// A queued issue job may have moved the certificate into a transitional
	// state. Repair those certificates now that the job is canceled.
	_, _ = s.Store.RecoverInterruptedCertificates(ctx, now)
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
	runs, err := s.Store.ListWorkflowRuns(ctx, w.ID, 1)
	var lastRun *store.WorkflowRun
	if err == nil && len(runs) > 0 {
		lastRun = &runs[0]
	}
	return workflowViewFrom(w, lastRun)
}

func workflowViewFrom(w *store.Workflow, lastRun *store.WorkflowRun) (*WorkflowView, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal([]byte(w.DefinitionJSON), &def); err != nil {
		return nil, fmt.Errorf("workflow %s: invalid definition: %w", w.ID, err)
	}
	return &WorkflowView{
		ID: w.ID, Name: w.Name, Description: w.Description, Definition: def,
		Enabled: w.Enabled, TriggerType: w.TriggerType, Schedule: w.Schedule,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, LastRun: lastRun,
	}, nil
}

func (s *Service) normalizeWorkflow(ctx context.Context, in WorkflowInput) (name, desc, definitionJSON string, trigger, schedule string, err error) {
	name = strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "\r\n\x00") {
		return "", "", "", "", "", errors.New("workflow name must be 1-100 characters")
	}
	desc = strings.TrimSpace(in.Description)
	if len(desc) > 500 || strings.ContainsAny(desc, "\x00") {
		return "", "", "", "", "", errors.New("workflow description is too long")
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
		return "", "", "", "", "", err
	}
	if err := s.validateWorkflowReferences(ctx, &def); err != nil {
		return "", "", "", "", "", err
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return "", "", "", "", "", err
	}
	trigger = in.TriggerType
	if trigger == "" {
		trigger = "manual"
	}
	if trigger != "manual" && trigger != "schedule" {
		return "", "", "", "", "", errors.New("trigger_type must be manual or schedule")
	}
	if trigger == "schedule" {
		schedule, err = normalizeSchedule(in.Schedule)
		if err != nil {
			return "", "", "", "", "", err
		}
	} else {
		schedule = ""
	}
	return name, desc, string(raw), trigger, schedule, nil
}

func validateWorkflowDefinition(def *WorkflowDefinition) error {
	if def.Version != 0 && def.Version != 1 && def.Version != 2 {
		return fmt.Errorf("unsupported workflow definition version %d", def.Version)
	}
	if def.Version == 1 && len(def.Nodes) > 0 {
		return errors.New("version 1 workflow must use steps, not nodes")
	}
	if def.Version == 2 && len(def.Steps) > 0 {
		return errors.New("version 2 workflow must use nodes, not steps")
	}
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
		if err := validateStepName(st.Name); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
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

// validateWorkflowReferences rejects definitions that point at missing or
// unusable resources at save time instead of failing only when the run starts.
func (s *Service) validateWorkflowReferences(ctx context.Context, def *WorkflowDefinition) error {
	steps := def.Steps
	if len(def.Nodes) > 0 {
		steps = make([]WorkflowStep, 0, len(def.Nodes))
		for _, n := range def.Nodes {
			steps = append(steps, nodeToStep(n))
		}
	}
	for _, st := range steps {
		switch st.Type {
		case StepApply:
			var cfg ApplyStepConfig
			if len(st.Config) > 0 {
				if err := json.Unmarshal(st.Config, &cfg); err != nil {
					return fmt.Errorf("step %q: %w", st.ID, err)
				}
			}
			if cfg.CertificateID != "" {
				if _, err := s.Store.GetCertificate(ctx, cfg.CertificateID); err != nil {
					return fmt.Errorf("step %q: certificate %s: %w", st.ID, cfg.CertificateID, err)
				}
				continue
			}
			if cfg.PrimaryDomain == "" {
				continue
			}
			if _, err := certificate.NormalizeDomain(cfg.PrimaryDomain); err != nil {
				return fmt.Errorf("step %q: %w", st.ID, err)
			}
			if err := certificate.ValidateKeyType(cfg.KeyType); err != nil {
				return fmt.Errorf("step %q: %w", st.ID, err)
			}
			if err := certificate.ValidateChallenge(cfg.ChallengeType, cfg.SAN); err != nil {
				return fmt.Errorf("step %q: %w", st.ID, err)
			}
			ca, err := s.Store.GetCAAccount(ctx, cfg.CAAccountID)
			if err != nil {
				return fmt.Errorf("step %q: CA account %s: %w", st.ID, cfg.CAAccountID, err)
			}
			if ca.Status == "disabled" {
				return fmt.Errorf("step %q: CA account %s is disabled", st.ID, cfg.CAAccountID)
			}
			if cfg.ChallengeType == "dns-01" {
				if cfg.DNSCredentialID == "" {
					return fmt.Errorf("step %q: DNS-01 requires a DNS credential", st.ID)
				}
				if _, err := s.Store.GetDNSCredential(ctx, cfg.DNSCredentialID); err != nil {
					return fmt.Errorf("step %q: DNS credential %s: %w", st.ID, cfg.DNSCredentialID, err)
				}
			}
			if cfg.ChallengeType == "http-01-webroot" && strings.TrimSpace(cfg.Webroot) == "" {
				return fmt.Errorf("step %q: HTTP-01 webroot requires a webroot path", st.ID)
			}
		case StepDeploy:
			var cfg DeployStepConfig
			if len(st.Config) > 0 {
				if err := json.Unmarshal(st.Config, &cfg); err != nil {
					return fmt.Errorf("step %q: %w", st.ID, err)
				}
			}
			if cfg.CertificateID != "" {
				if _, err := s.Store.GetCertificate(ctx, cfg.CertificateID); err != nil {
					return fmt.Errorf("step %q: certificate %s: %w", st.ID, cfg.CertificateID, err)
				}
			}
			if cfg.RevisionID != "" && cfg.CertificateID != "" {
				rev, err := s.Store.GetRevision(ctx, cfg.RevisionID)
				if err != nil {
					return fmt.Errorf("step %q: revision %s: %w", st.ID, cfg.RevisionID, err)
				}
				if rev.CertificateID != cfg.CertificateID {
					return fmt.Errorf("step %q: revision %s does not belong to certificate %s", st.ID, cfg.RevisionID, cfg.CertificateID)
				}
			}
		case StepVerify:
			var cfg VerifyStepConfig
			if len(st.Config) > 0 {
				if err := json.Unmarshal(st.Config, &cfg); err != nil {
					return fmt.Errorf("step %q: %w", st.ID, err)
				}
			}
			if cfg.CertificateID != "" {
				if _, err := s.Store.GetCertificate(ctx, cfg.CertificateID); err != nil {
					return fmt.Errorf("step %q: certificate %s: %w", st.ID, cfg.CertificateID, err)
				}
			}
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
		if err := validateStepName(n.Name); err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
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
	edgePairs := map[string]bool{}
	for i := range def.Edges {
		e := &def.Edges[i]
		e.ID = strings.TrimSpace(e.ID)
		if e.ID == "" {
			e.ID = fmt.Sprintf("edge-%d", i+1)
		}
		if len(e.ID) > 64 {
			return fmt.Errorf("edge %d: id must be at most 64 characters", i+1)
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
		pair := e.From + ">" + e.To
		if edgePairs[pair] {
			return fmt.Errorf("duplicate edge from %q to %q", e.From, e.To)
		}
		edgePairs[pair] = true
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
	roots := 0
	for id := range nodes {
		if incoming[id] == 0 {
			roots++
		}
	}
	if roots == 0 {
		return errors.New("workflow graph has no start node")
	}
	if roots > 1 && !def.AllowMultipleStarts {
		return errors.New("workflow graph has multiple start nodes; connect them or set allow_multiple_starts")
	}
	if hasCycle(def) {
		return errors.New("workflow graph contains a cycle")
	}
	return nil
}

// validateStepName keeps user-provided labels bounded and free of control
// characters. Empty names are allowed and fall back to the step id.
func validateStepName(name string) error {
	if len(name) > 100 {
		return errors.New("name must be at most 100 characters")
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("name must not contain control characters")
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
		recent, err := s.Store.RecentWorkflowRun(ctx, w.ID, "schedule", store.Now()-60)
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
		Error: st.Error, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt, AvailableAt: st.AvailableAt,
	}
}
