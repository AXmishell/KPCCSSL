package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/store"
)

// DeploymentInput is the create/update payload.
type DeploymentInput struct {
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Config        json.RawMessage `json:"config"`
	Enabled       bool            `json:"enabled"`
	CertificateID string          `json:"certificate_id"`
}

// CreateDeployment validates and stores a deployment target.
func (s *Service) CreateDeployment(ctx context.Context, certID string, in DeploymentInput) (*store.Deployment, error) {
	if _, err := s.Store.GetCertificate(ctx, certID); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if len(in.Config) == 0 {
		return nil, errors.New("config is required")
	}
	if err := s.Deployer.Validate(in.Type, string(in.Config)); err != nil {
		return nil, err
	}
	now := store.Now()
	d := &store.Deployment{
		ID: id.New(), CertificateID: certID, Name: in.Name, Type: in.Type,
		CredentialID: credentialIDFromConfig(in.Config),
		Enabled:      in.Enabled, KeyVersion: s.Box.Version(), CreatedAt: now, UpdatedAt: now,
	}
	enc, err := s.Box.Encrypt(in.Config, "deployments:"+d.ID+":config")
	if err != nil {
		return nil, err
	}
	d.ConfigEnc = enc
	if err := s.Store.CreateDeployment(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// UpdateDeployment updates a target; an empty config keeps the stored one.
func (s *Service) UpdateDeployment(ctx context.Context, id string, in DeploymentInput) (*store.Deployment, error) {
	d, err := s.Store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		d.Name = strings.TrimSpace(in.Name)
	}
	if in.Type != "" {
		d.Type = in.Type
	}
	if len(in.Config) > 0 {
		if err := s.Deployer.Validate(d.Type, string(in.Config)); err != nil {
			return nil, err
		}
		enc, err := s.Box.Encrypt(in.Config, "deployments:"+d.ID+":config")
		if err != nil {
			return nil, err
		}
		d.ConfigEnc, d.KeyVersion = enc, s.Box.Version()
		d.CredentialID = credentialIDFromConfig(in.Config)
	}
	d.Enabled = in.Enabled
	d.UpdatedAt = store.Now()
	if err := s.Store.UpdateDeployment(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDeployment removes a target.
func (s *Service) DeleteDeployment(ctx context.Context, id string) error {
	return s.Store.DeleteDeployment(ctx, id)
}

// TestDeployment validates a deployment configuration and checks
// connectivity without changing the target.
func (s *Service) TestDeployment(ctx context.Context, depType string, config json.RawMessage) error {
	if strings.TrimSpace(depType) == "" {
		return errors.New("type is required")
	}
	if len(config) == 0 {
		return errors.New("config is required")
	}
	return s.Deployer.Test(ctx, depType, string(config))
}

// TestDeploymentByID checks a saved deployment target.
func (s *Service) TestDeploymentByID(ctx context.Context, id string) error {
	d, err := s.Store.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	return s.Deployer.TestStored(ctx, d)
}

// DeployNow enqueues deployment of the current revision.
func (s *Service) DeployNow(ctx context.Context, certID, idem string) ([]*store.Job, error) {
	c, err := s.Store.GetCertificate(ctx, certID)
	if err != nil {
		return nil, err
	}
	if c.CurrentRevisionID == nil {
		return nil, errors.New("certificate has no issued revision")
	}
	deps, err := s.Store.ListDeployments(ctx, certID)
	if err != nil {
		return nil, err
	}
	var out []*store.Job
	for _, d := range deps {
		if !d.Enabled {
			continue
		}
		key := idem
		if key != "" {
			key = key + ":" + d.ID
		}
		j, _, err := s.Enqueue(ctx, "deploy", &certID, nil, map[string]string{"deployment_id": d.ID, "revision_id": *c.CurrentRevisionID}, key, 3)
		if err != nil {
			return out, err
		}
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil, errors.New("no enabled deployment targets")
	}
	return out, nil
}

// DeployRevisionNow enqueues an explicit historical revision to every enabled
// deployment target. It is used by workflow deploy steps.
func (s *Service) DeployRevisionNow(ctx context.Context, certID, revisionID, idem string) ([]*store.Job, error) {
	return s.DeployRevisionTargets(ctx, certID, revisionID, nil, idem)
}

// DeployRevisionTargets enqueues a revision to the selected deployment targets.
// An empty deploymentIDs list means every enabled target of the certificate.
func (s *Service) DeployRevisionTargets(ctx context.Context, certID, revisionID string, deploymentIDs []string, idem string) ([]*store.Job, error) {
	if _, err := s.Store.GetCertificate(ctx, certID); err != nil {
		return nil, err
	}
	rev, err := s.Store.GetRevision(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if rev.CertificateID != certID {
		return nil, errors.New("revision does not belong to certificate")
	}
	deps, err := s.Store.ListDeployments(ctx, certID)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(deploymentIDs))
	for _, target := range deploymentIDs {
		if target = strings.TrimSpace(target); target != "" {
			selected[target] = true
		}
	}
	// Reject unknown targets instead of silently deploying to every target or
	// skipping a typo.
	if len(selected) > 0 {
		known := make(map[string]bool, len(deps))
		for _, d := range deps {
			known[d.ID] = true
		}
		for target := range selected {
			if !known[target] {
				return nil, fmt.Errorf("deployment target %s does not belong to this certificate", target)
			}
		}
	}
	var out []*store.Job
	for _, d := range deps {
		if !d.Enabled {
			continue
		}
		if len(selected) > 0 && !selected[d.ID] {
			continue
		}
		key := idem
		if key != "" {
			key = key + ":" + d.ID
			// Revision-scoped keys are shared with automatic deploys. Reuse an
			// in-flight or successful job, but allow a fresh attempt after a
			// failure so a re-run is not pinned to the old error forever.
			if existing, err := s.Store.GetJobByIdempotencyKey(ctx, key); err == nil {
				switch existing.Status {
				case "queued", "running", "succeeded":
					out = append(out, existing)
					continue
				default:
					key = key + ":retry:" + id.New()
				}
			}
		}
		j, _, err := s.Enqueue(ctx, "deploy", &certID, nil, map[string]string{"deployment_id": d.ID, "revision_id": rev.ID}, key, 3)
		if err != nil {
			return out, err
		}
		out = append(out, j)
	}
	if len(out) == 0 {
		if len(selected) > 0 {
			return nil, errors.New("none of the selected deployment targets are enabled")
		}
		return nil, errors.New("no enabled deployment targets")
	}
	return out, nil
}

func (s *Service) handleDeploy(ctx context.Context, j *store.Job, log io.Writer) error {
	var payload struct {
		DeploymentID string `json:"deployment_id"`
		RevisionID   string `json:"revision_id"`
	}
	if err := json.Unmarshal([]byte(j.PayloadJSON), &payload); err != nil {
		return job.Permanent(err)
	}
	dep, err := s.Store.GetDeployment(ctx, payload.DeploymentID)
	if err != nil {
		return job.Permanent(err)
	}
	rev, err := s.Store.GetRevision(ctx, payload.RevisionID)
	if err != nil {
		return job.Permanent(err)
	}
	fmt.Fprintf(log, "deploying revision %s to %s (%s)\n", rev.ID, dep.Name, dep.Type)
	if err := s.Deployer.Run(ctx, dep, rev); err != nil {
		code, msg := errorParts(err)
		s.AddEvent(ctx, dep.CertificateID, "deploy_failed", fmt.Sprintf("%s: %s", dep.Name, msg), j.ID)
		s.Notify(ctx, "deployment.failed", "deploy:"+dep.ID+":"+j.ID, "Deployment failed", dep.Name+"\n"+code+": "+msg)
		return err
	}
	s.AddEvent(ctx, dep.CertificateID, "deployed", fmt.Sprintf("deployed to %s", dep.Name), j.ID)
	fmt.Fprintf(log, "deployment succeeded\n")
	return nil
}

// credentialIDFromConfig extracts the optional credential reference embedded
// in a deployment config.
func credentialIDFromConfig(raw json.RawMessage) *string {
	var ref struct {
		CredentialID string `json:"credential_id"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil
	}
	id := strings.TrimSpace(ref.CredentialID)
	if id == "" {
		return nil
	}
	return &id
}
