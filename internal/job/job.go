// Package job implements the durable job queue, per-job log files and the SSE
// broadcast hub.
package job

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/store"
)

// Line is one log line pushed to SSE clients.
type Line struct {
	Seq    int64  `json:"seq"`
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

// Logs owns job log files and their in-process subscribers.
type Logs struct {
	dir  string
	mu   sync.Mutex
	w    map[string]*writer
	subs map[string]map[chan Line]struct{}
}

// NewLogs creates a log manager rooted at dir.
func NewLogs(dir string) (*Logs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Logs{dir: dir, w: map[string]*writer{}, subs: map[string]map[chan Line]struct{}{}}, nil
}

// Path returns the log file path for a job.
func (l *Logs) Path(jobID string) string { return filepath.Join(l.dir, jobID+".log") }

// Writer returns a line-oriented writer for a job.
func (l *Logs) Writer(jobID string) io.Writer {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w, ok := l.w[jobID]; ok {
		return w
	}
	w := &writer{logs: l, jobID: jobID, stream: "stdout"}
	l.w[jobID] = w
	return w
}

// Close flushes and removes the writer state for a finished job.
func (l *Logs) Close(jobID string) {
	l.mu.Lock()
	w := l.w[jobID]
	delete(l.w, jobID)
	l.mu.Unlock()
	if w != nil {
		w.flush()
	}
}

// Read returns log lines from the file starting after seq.
func (l *Logs) Read(jobID string, after int64, limit int) ([]Line, error) {
	f, err := os.Open(l.Path(jobID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []Line
	var seq int64
	for sc.Scan() {
		seq++
		if seq <= after {
			continue
		}
		out = append(out, Line{Seq: seq, Stream: "stdout", Line: Redact(sc.Text())})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, sc.Err()
}

// Subscribe returns a channel of redacted live lines and an unsubscribe func.
func (l *Logs) Subscribe(jobID string) (<-chan Line, func()) {
	ch := make(chan Line, 256)
	l.mu.Lock()
	if l.subs[jobID] == nil {
		l.subs[jobID] = map[chan Line]struct{}{}
	}
	l.subs[jobID][ch] = struct{}{}
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		if set, ok := l.subs[jobID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(l.subs, jobID)
			}
		}
		l.mu.Unlock()
	}
}

func (l *Logs) broadcast(jobID string, line Line) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch := range l.subs[jobID] {
		select {
		case ch <- line:
		default: // slow consumer: drop rather than block the job
		}
	}
}

type writer struct {
	logs   *Logs
	jobID  string
	stream string
	mu     sync.Mutex
	buf    strings.Builder
	seq    int64
	f      *os.File
}

func (w *writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		f, err := os.OpenFile(w.logs.Path(w.jobID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		w.f = f
		// Resume sequence from the existing file.
		if lines, _ := w.logs.Read(w.jobID, 0, 0); len(lines) > 0 {
			w.seq = lines[len(lines)-1].Seq
		}
	}
	n := len(p)
	w.buf.Write(p)
	for {
		s := w.buf.String()
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(s[:idx], "\r")
		w.buf.Reset()
		w.buf.WriteString(s[idx+1:])
		w.seq++
		_, _ = w.f.WriteString(line + "\n")
		w.logs.broadcast(w.jobID, Line{Seq: w.seq, Stream: w.stream, Line: Redact(line)})
	}
	return n, nil
}

func (w *writer) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.seq++
		line := w.buf.String()
		w.buf.Reset()
		if w.f != nil {
			_, _ = w.f.WriteString(line + "\n")
		}
		w.logs.broadcast(w.jobID, Line{Seq: w.seq, Stream: w.stream, Line: Redact(line)})
	}
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}

var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:(?:bearer|basic)\s+)?[^\s,;]+`),
	regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)[^\s,;]+`),
	regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|hmac|private[_-]?key|access[_-]?key)\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(CF_Token|Ali_Secret|DP_Key|AWS_SECRET_ACCESS_KEY|GD_Secret)=([^\s]+)`),
}

// Redact removes credentials, cookies and private keys from log text.
func Redact(s string) string {
	for _, re := range redactPatterns {
		s = re.ReplaceAllString(s, "${1}[REDACTED]")
	}
	return s
}

// Cleanup removes log files last modified before cutoff.
func (l *Logs) Cleanup(cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(l.dir, e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// PermanentError marks a handler failure that must not be retried.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// Handler executes one job. The writer receives command output.
type Handler func(ctx context.Context, j *store.Job, log io.Writer) error

// Runner claims and executes jobs.
type Runner struct {
	Store      *store.Store
	Logs       *Logs
	Timeout    time.Duration
	handlers   map[string]Handler
	retryDelay func(jobType string, attempt int) time.Duration
}

// NewRunner creates a runner.
func NewRunner(st *store.Store, logs *Logs, timeout time.Duration) *Runner {
	return &Runner{
		Store:      st,
		Logs:       logs,
		Timeout:    timeout,
		handlers:   map[string]Handler{},
		retryDelay: RetryDelay,
	}
}

// Handle registers a job handler.
func (r *Runner) Handle(jobType string, h Handler) { r.handlers[jobType] = h }

// Run processes jobs until ctx is canceled.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				ran, err := r.RunOnce(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "job runner: %v\n", err)
					break
				}
				if !ran {
					break
				}
			}
		}
	}
}

// RunOnce claims and executes at most one job.
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	j, err := r.Store.ClaimNext(ctx, store.Now())
	if err != nil {
		return false, err
	}
	if j == nil {
		return false, nil
	}
	r.execute(ctx, j)
	return true, nil
}

func (r *Runner) execute(parent context.Context, j *store.Job) {
	logPath := r.Logs.Path(j.ID)
	w := r.Logs.Writer(j.ID)
	defer r.Logs.Close(j.ID)

	h, ok := r.handlers[j.Type]
	if !ok {
		r.finish(parent, j, fmt.Errorf("no handler registered for job type %s", j.Type), "failed")
		return
	}

	ctx := parent
	var cancel context.CancelFunc
	if r.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, r.Timeout)
		defer cancel()
	}

	stopHeartbeat := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-t.C:
				_ = r.Store.Heartbeat(context.Background(), j.ID, store.Now())
			}
		}
	}()

	fmt.Fprintf(w, "=== job %s (%s), attempt %d/%d ===\n", j.ID, j.Type, j.Attempts, j.MaxAttempts)
	err := h(ctx, j, w)
	close(stopHeartbeat)
	if err != nil {
		var ae *acme.Error
		code := acme.CodeUnknown
		msg := err.Error()
		if errors.As(err, &ae) {
			code, msg = ae.Code, ae.Message
		}
		msg = Redact(msg)
		var pe *PermanentError
		permanent := errors.As(err, &pe)
		if code == "" {
			code = acme.CodeUnknown
		}
		fmt.Fprintf(w, "=== failed: %s: %s ===\n", code, msg)
		retry := !permanent && j.Attempts < j.MaxAttempts && retryable(code)
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if retry {
			delay := r.retryDelay(j.Type, j.Attempts)
			available := store.Now() + int64(delay/time.Second)
			fmt.Fprintf(w, "=== retry %d/%d in %s ===\n", j.Attempts+1, j.MaxAttempts, delay)
			if err := r.Store.RequeueJob(finishCtx, j.ID, available, &code, &msg, store.Now()); err != nil {
				fmt.Fprintf(w, "=== requeue error: %v ===\n", err)
			}
		} else {
			_ = r.Store.FinishJob(finishCtx, j.ID, "failed", &code, &msg, store.Now())
		}
		_ = logPath
		return
	}
	fmt.Fprintf(w, "=== succeeded ===\n")
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.Store.FinishJob(finishCtx, j.ID, "succeeded", nil, nil, store.Now())
}

func (r *Runner) finish(ctx context.Context, j *store.Job, err error, status string) {
	msg := Redact(err.Error())
	code := acme.CodeACMEInternal
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.Store.FinishJob(finishCtx, j.ID, status, &code, &msg, store.Now())
}

func retryable(code string) bool {
	switch code {
	case acme.CodeRateLimit, acme.CodeDomainInvalid, acme.CodeCertMissing, acme.CodeCanceled, acme.CodeInterrupted, acme.CodeInvalidArgument:
		return false
	}
	return true
}

// RetryDelay returns the backoff for the given job type and attempt number
// (1-based).
func RetryDelay(jobType string, attempt int) time.Duration {
	table := map[string][]time.Duration{
		"issue":            {time.Minute, 10 * time.Minute, time.Hour},
		"renew":            {time.Hour, 6 * time.Hour, 24 * time.Hour, 24 * time.Hour},
		"deploy":           {time.Minute, 5 * time.Minute, 15 * time.Minute},
		"register_account": {time.Minute, 5 * time.Minute, 15 * time.Minute},
		"sync":             {time.Minute, 5 * time.Minute, 15 * time.Minute},
		"revoke":           {},
	}
	delays := table[jobType]
	if len(delays) == 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}
