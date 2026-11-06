package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/kpccssl/internal/acme/fake"
	"github.com/axmishell/kpccssl/internal/api"
	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/config"
	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/notify"
	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
)

type harness struct {
	server *httptest.Server
	client *http.Client
	store  *store.Store
	svc    *service.Service
	runner *job.Runner
	csrf   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: ":0", DataDir: dir, DBPath: filepath.Join(dir, "test.db"),
		MasterKeyFile: filepath.Join(dir, "master.key"), ACMEHome: filepath.Join(dir, "acme"),
		CertDir: filepath.Join(dir, "certs"), LogDir: filepath.Join(dir, "logs"),
		JobConcurrency: 1, JobTimeout: 30 * time.Second, Dev: true, FakeACME: true,
		SetupToken: "setup-token",
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	allow := filepath.Join(dir, "deploy")
	if err := os.MkdirAll(allow, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.DeployRoots = []string{allow}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(cfg.MasterKeyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	logs, err := job.NewLogs(filepath.Join(cfg.LogDir, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	runner := job.NewRunner(st, logs, cfg.JobTimeout)
	svc, err := service.New(cfg, st, box, fake.New(), logs, runner, deploy.New(st, box, cfg.DeployRoots), notify.New(st, box))
	if err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(st, "kpccssl_session", false)
	api.SetWebHandler(nil)
	srv := httptest.NewServer(api.New(cfg, st, svc, am, "test").Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{server: srv, client: &http.Client{Jar: jar}, store: st, svc: svc, runner: runner}
}

func (h *harness) do(t *testing.T, method, path string, body any, csrf string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+"/api/v1"+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (h *harness) mustDo(t *testing.T, method, path string, body any, csrf string, want int) []byte {
	t.Helper()
	status, data := h.do(t, method, path, body, csrf)
	if status != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, status, want, data)
	}
	return data
}

func (h *harness) drainJobs(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		ran, err := h.runner.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			return
		}
	}
}

func TestEndToEndIssueFlow(t *testing.T) {
	h := newHarness(t)

	// Unauthenticated access is rejected.
	if status, _ := h.do(t, "GET", "/me", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("GET /me = %d", status)
	}
	// Wrong setup token is rejected.
	if status, _ := h.do(t, "POST", "/setup", map[string]string{"token": "nope", "email": "a@example.com", "password": "correct-horse-battery-staple"}, ""); status != http.StatusBadRequest {
		t.Fatalf("bad setup = %d", status)
	}
	data := h.mustDo(t, "POST", "/setup", map[string]string{"token": "setup-token", "email": "admin@example.com", "password": "correct-horse-battery-staple"}, "", http.StatusCreated)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(data, &me); err != nil {
		t.Fatal(err)
	}
	h.csrf = me.CSRF
	if h.csrf == "" {
		t.Fatal("missing csrf token")
	}

	// Missing CSRF is rejected.
	if status, _ := h.do(t, "POST", "/ca-accounts", map[string]string{"name": "x"}, ""); status != http.StatusForbidden {
		t.Fatalf("csrf check = %d", status)
	}

	data = h.mustDo(t, "POST", "/ca-accounts", map[string]any{
		"name": "LE staging", "directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
		"email": "admin@example.com", "tos_accepted": true,
	}, h.csrf, http.StatusCreated)
	var ca struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &ca)
	if ca.ID == "" {
		t.Fatal("missing CA id")
	}
	if err := h.store.SetCAStatus(context.Background(), ca.ID, "registered", nil, store.Now()); err != nil {
		t.Fatal(err)
	}

	data = h.mustDo(t, "POST", "/dns-credentials", map[string]any{
		"name": "cf", "provider": "dns_cf", "config": map[string]string{"CF_Token": "super-secret"},
	}, h.csrf, http.StatusCreated)
	var dns struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &dns)
	if strings.Contains(string(data), "super-secret") {
		t.Fatal("API leaked the DNS token")
	}
	cred, err := h.store.GetDNSCredential(context.Background(), dns.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cred.ConfigEnc, "super-secret") {
		t.Fatal("credential stored in plaintext")
	}

	data = h.mustDo(t, "POST", "/certificates", map[string]any{
		"name": "example", "primary_domain": "example.com", "san": []string{"www.example.com"},
		"key_type": "ec-256", "ca_account_id": ca.ID, "challenge_type": "dns-01",
		"dns_credential_id": dns.ID, "auto_renew": true,
	}, h.csrf, http.StatusCreated)
	var cert struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	_ = json.Unmarshal(data, &cert)

	// Issue with an idempotency key.
	data = h.mustDo(t, "POST", "/certificates/"+cert.ID+"/issue", map[string]any{}, h.csrf, http.StatusAccepted)
	var accepted struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(data, &accepted)
	if accepted.JobID == "" {
		t.Fatal("missing job id")
	}
	h.drainJobs(t)

	data = h.mustDo(t, "GET", "/certificates/"+cert.ID, nil, "", http.StatusOK)
	var detail struct {
		Certificate struct {
			Status            string  `json:"status"`
			CurrentRevisionID *string `json:"current_revision_id"`
			NextRenewAt       *int64  `json:"next_renew_at"`
		} `json:"certificate"`
		CurrentRevision *struct {
			NotAfter int64  `json:"not_after"`
			Serial   string `json:"serial"`
		} `json:"current_revision"`
	}
	_ = json.Unmarshal(data, &detail)
	if detail.Certificate.Status != "active" {
		t.Fatalf("certificate status = %s (%s)", detail.Certificate.Status, data)
	}
	if detail.Certificate.CurrentRevisionID == nil || detail.CurrentRevision == nil {
		t.Fatal("missing current revision")
	}
	if detail.CurrentRevision.NotAfter <= time.Now().Unix() {
		t.Fatal("certificate already expired")
	}

	// Repeat the same idempotency key: same job, no new issuance.
	req, _ := http.NewRequest("POST", h.server.URL+"/api/v1/certificates/"+cert.ID+"/issue", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", h.csrf)
	req.Header.Set("Idempotency-Key", "same-key")
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("issue status = %d", resp.StatusCode)
	}
	jobID1 := resp.Header.Get("Location")
	req2, _ := http.NewRequest("POST", h.server.URL+"/api/v1/certificates/"+cert.ID+"/issue", bytes.NewReader([]byte("{}")))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-CSRF-Token", h.csrf)
	req2.Header.Set("Idempotency-Key", "same-key")
	resp2, err := h.client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("Location"); got != jobID1 {
		t.Fatalf("idempotency mismatch: %s vs %s", got, jobID1)
	}

	// Private-key downloads require a fresh step-up grant.
	data = h.mustDo(t, "POST", "/me/step-up", map[string]string{"password": "correct-horse-battery-staple"}, h.csrf, http.StatusOK)
	var stepUp struct {
		Token string `json:"step_up_token"`
	}
	if err := json.Unmarshal(data, &stepUp); err != nil || stepUp.Token == "" {
		t.Fatalf("step-up token missing: %s", data)
	}

	// Download ZIP and verify it contains the key material.
	zipReq, _ := http.NewRequest("GET", h.server.URL+"/api/v1/certificates/"+cert.ID+"/download?format=zip", nil)
	zipReq.Header.Set("X-Step-Up-Token", stepUp.Token)
	zipResp, err := h.client.Do(zipReq)
	if err != nil {
		t.Fatal(err)
	}
	zipResp.Body.Close()
	if zipResp.StatusCode != http.StatusOK {
		t.Fatalf("zip download = %d", zipResp.StatusCode)
	}
	if status, _ := h.do(t, "GET", "/certificates/"+cert.ID+"/download?format=zip", nil, ""); status != http.StatusPreconditionRequired {
		t.Fatalf("zip without confirm = %d", status)
	}
	data = h.mustDo(t, "GET", "/certificates/"+cert.ID+"/download?format=pem", nil, "", http.StatusOK)
	if !bytes.Contains(data, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("PEM download missing certificate")
	}
	// Private key requires explicit confirmation.
	if status, _ := h.do(t, "GET", "/certificates/"+cert.ID+"/download?format=pem&key=true", nil, ""); status != http.StatusPreconditionRequired {
		t.Fatalf("key download without confirm = %d", status)
	}

	// SSE endpoint replays history and terminates with job.done.
	req3, _ := http.NewRequest("GET", h.server.URL+"/api/v1/jobs/"+accepted.JobID+"/events", nil)
	resp3, err := h.client.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp3.Body, 1<<20))
	if !strings.Contains(string(body), "event: job.done") {
		t.Fatalf("SSE stream missing job.done: %s", body)
	}
	if !strings.Contains(string(body), "job.log") {
		t.Fatalf("SSE stream missing log lines: %s", body)
	}

	// Admin can download the raw job log.
	data = h.mustDo(t, "GET", "/jobs/"+accepted.JobID+"/log", nil, "", http.StatusOK)
	if !strings.Contains(string(data), "=== job") {
		t.Fatalf("raw job log missing content: %s", data)
	}

	// Audit log should contain the setup and certificate creation.
	data = h.mustDo(t, "GET", "/audit-logs", nil, "", http.StatusOK)
	if !strings.Contains(string(data), "certificate.create") {
		t.Fatalf("audit log missing create: %s", data)
	}
}

func TestWorkflowApplyVerifyAndNotify(t *testing.T) {
	h := newHarness(t)
	data := h.mustDo(t, "POST", "/setup", map[string]string{"token": "setup-token", "email": "admin@example.com", "password": "correct-horse-battery-staple"}, "", http.StatusCreated)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(data, &me)

	now := store.Now()
	ca := &store.CAAccount{ID: "ca-workflow", Name: "CA", DirectoryURL: "https://ca", Status: "registered", KeyVersion: 1, TOSAcceptedAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := h.store.CreateCAAccount(context.Background(), ca); err != nil {
		t.Fatal(err)
	}
	cred, err := h.svc.CreateDNS(context.Background(), service.DNSInput{Name: "cf", Provider: "dns_cf", Config: map[string]string{"CF_Token": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := h.svc.CreateCertificate(context.Background(), service.CertInput{
		Name: "workflow", PrimaryDomain: "wf.example.com", KeyType: "ec-256", CAAccountID: ca.ID,
		ChallengeType: "dns-01", DNSCredentialID: cred.ID, AutoRenew: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	definition := map[string]any{
		"version": 1,
		"steps": []map[string]any{
			{"id": "apply", "type": "apply", "config": map[string]any{"certificate_id": cert.ID}},
			{"id": "verify", "type": "verify", "config": map[string]any{"certificate_id": cert.ID, "min_days": 1}},
			{"id": "notify", "type": "notify", "config": map[string]any{"title": "workflow done", "body": "run {{run_id}}"}},
		},
	}
	data = h.mustDo(t, "POST", "/workflows", map[string]any{
		"name": "WF", "enabled": true, "trigger_type": "manual", "definition": definition,
	}, me.CSRF, http.StatusCreated)
	var wf struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &wf)
	data = h.mustDo(t, "POST", "/workflows/"+wf.ID+"/run", map[string]any{}, me.CSRF, http.StatusAccepted)
	var started struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(data, &started)

	wfRunner := service.NewWorkflowRunner(h.svc)
	for i := 0; i < 20; i++ {
		_, _ = wfRunner.RunOnce(context.Background())
		h.drainJobs(t)
	}
	data = h.mustDo(t, "GET", "/workflow-runs/"+started.RunID, nil, "", http.StatusOK)
	var detail struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
		Steps []struct {
			StepID string `json:"step_id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(data, &detail)
	if detail.Run.Status != "succeeded" {
		t.Fatalf("workflow status = %s (%s)", detail.Run.Status, data)
	}
	if len(detail.Steps) != 3 {
		t.Fatalf("steps = %+v", detail.Steps)
	}
	for _, st := range detail.Steps {
		if st.Status != "succeeded" {
			t.Fatalf("step %s = %s", st.StepID, st.Status)
		}
	}
	issued, err := h.store.GetCertificate(context.Background(), cert.ID)
	if err != nil || issued.Status != "active" || issued.CurrentRevisionID == nil {
		t.Fatalf("certificate not active: %+v err=%v", issued, err)
	}
}

func TestWorkflowGraphConnection(t *testing.T) {
	h := newHarness(t)
	data := h.mustDo(t, "POST", "/setup", map[string]string{"token": "setup-token", "email": "admin@example.com", "password": "correct-horse-battery-staple"}, "", http.StatusCreated)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(data, &me)

	definition := map[string]any{
		"version": 2,
		"nodes": []map[string]any{
			{"id": "start", "type": "wait", "name": "start", "config": map[string]any{"seconds": 1}, "position": map[string]any{"x": 10, "y": 10}},
			{"id": "done", "type": "notify", "name": "done", "config": map[string]any{"title": "graph done"}, "position": map[string]any{"x": 240, "y": 10}},
		},
		"edges": []map[string]any{
			{"id": "e1", "from": "start", "to": "done", "condition": "success"},
		},
	}
	data = h.mustDo(t, "POST", "/workflows", map[string]any{
		"name": "Graph WF", "enabled": true, "trigger_type": "manual", "definition": definition,
	}, me.CSRF, http.StatusCreated)
	var wf struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &wf)
	data = h.mustDo(t, "POST", "/workflows/"+wf.ID+"/run", map[string]any{}, me.CSRF, http.StatusAccepted)
	var started struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(data, &started)

	wfRunner := service.NewWorkflowRunner(h.svc)
	for i := 0; i < 20; i++ {
		_, _ = wfRunner.RunOnce(context.Background())
		h.drainJobs(t)
		time.Sleep(100 * time.Millisecond)
	}
	data = h.mustDo(t, "GET", "/workflow-runs/"+started.RunID, nil, "", http.StatusOK)
	var detail struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
		Steps []struct {
			StepID string `json:"step_id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(data, &detail)
	if detail.Run.Status != "succeeded" || len(detail.Steps) != 2 {
		t.Fatalf("run=%s steps=%+v", detail.Run.Status, detail.Steps)
	}
	for _, st := range detail.Steps {
		if st.Status != "succeeded" {
			t.Fatalf("step %s = %s", st.StepID, st.Status)
		}
	}
}

func TestDeploymentLocalFile(t *testing.T) {
	h := newHarness(t)
	data := h.mustDo(t, "POST", "/setup", map[string]string{"token": "setup-token", "email": "admin@example.com", "password": "correct-horse-battery-staple"}, "", http.StatusCreated)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(data, &me)

	dir := t.TempDir()
	// The deployment directory must be inside the allow-list root used by the
	// harness, so discover it from the configured roots.
	allow := h.svc.Cfg.DeployRoots[0]
	nowTOS := store.Now()
	ca := &store.CAAccount{ID: "ca", Name: "CA", DirectoryURL: "https://ca", Status: "registered", KeyVersion: 1, TOSAcceptedAt: &nowTOS, CreatedAt: nowTOS, UpdatedAt: nowTOS}
	if err := h.store.CreateCAAccount(context.Background(), ca); err != nil {
		t.Fatal(err)
	}
	cred, _ := h.svc.CreateDNS(context.Background(), service.DNSInput{Name: "cf", Provider: "dns_cf", Config: map[string]string{"CF_Token": "x"}})
	cert, err := h.svc.CreateCertificate(context.Background(), service.CertInput{
		Name: "deploy", PrimaryDomain: "deploy.example.com", KeyType: "ec-256", CAAccountID: ca.ID,
		ChallengeType: "dns-01", DNSCredentialID: cred.ID, AutoRenew: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := h.svc.Enqueue(context.Background(), "issue", &cert.ID, &ca.ID, map[string]any{}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = j
	h.drainJobs(t)

	second := filepath.Join(allow, "second")
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = dir
	for i, target := range []string{allow, second} {
		cfg := fmt.Sprintf(`{"directory":%q,"cert_file":"cert.pem","key_file":"privkey.pem","chain_file":"chain.pem","fullchain_file":"fullchain.pem","mode":"0640","backup":true}`, target)
		status, data := h.do(t, "POST", "/deployments", map[string]any{
			"certificate_id": cert.ID, "name": fmt.Sprintf("local-%d", i), "type": "local_file", "config": json.RawMessage(cfg), "enabled": true,
		}, me.CSRF)
		if status != http.StatusCreated {
			t.Fatalf("create deployment = %d: %s", status, data)
		}
	}
	h.mustDo(t, "POST", "/certificates/"+cert.ID+"/deploy", map[string]any{}, me.CSRF, http.StatusAccepted)
	h.drainJobs(t)
	for _, target := range []string{allow, second} {
		for _, name := range []string{"cert.pem", "privkey.pem", "chain.pem", "fullchain.pem"} {
			if _, err := os.Stat(filepath.Join(target, name)); err != nil {
				t.Fatalf("deployed file %s/%s missing: %v", target, name, err)
			}
		}
	}
}
