package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/axmishell/kpccssl/internal/acme"
	"github.com/axmishell/kpccssl/internal/acme/acmesh"
	"github.com/axmishell/kpccssl/internal/acme/fake"
	"github.com/axmishell/kpccssl/internal/api"
	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/config"
	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/job"
	"github.com/axmishell/kpccssl/internal/notify"
	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/service"
	"github.com/axmishell/kpccssl/internal/store"
	"github.com/axmishell/kpccssl/internal/webui"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve", "worker":
		if err := runServe(cmd, args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "doctor":
		if err := runDoctor(args); err != nil {
			fmt.Fprintln(os.Stderr, "doctor:", err)
			os.Exit(1)
		}
	case "admin":
		if err := runAdmin(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("kpccssl %s (%s)\n", version, commit)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`KPCCSSL - self-hosted ACME certificate manager

Usage:
  kpccssl serve            Start API + SPA (default)
  kpccssl worker           Run jobs and the renewal scheduler only
  kpccssl migrate [--down] Apply or roll back database migrations
  kpccssl doctor           Check runtime prerequisites
  kpccssl admin reset-password --email <email>
  kpccssl version
`)
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}

// app holds the composed runtime.
type app struct {
	cfg    *config.Config
	store  *store.Store
	box    *secret.Box
	engine acme.Engine
	logs   *job.Logs
	runner *job.Runner
	svc    *service.Service
}

func buildApp(cfg *config.Config) (*app, error) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	box, err := secret.LoadOrCreate(cfg.MasterKeyFile, cfg.MasterKey)
	if err != nil {
		st.Close()
		return nil, err
	}
	var engine acme.Engine
	if cfg.FakeACME {
		engine = fake.New()
		slog.Warn("using fake ACME engine; never use in production")
	} else {
		engine = acmesh.New(cfg.ACMEBin, cfg.ACMEHome)
	}
	logs, err := job.NewLogs(filepath.Join(cfg.LogDir, "jobs"))
	if err != nil {
		st.Close()
		return nil, err
	}
	runner := job.NewRunner(st, logs, cfg.JobTimeout)
	dep := deploy.New(st, box, cfg.DeployRoots)
	notifier := notify.New(st, box)
	svc, err := service.New(cfg, st, box, engine, logs, runner, dep, notifier)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &app{cfg: cfg, store: st, box: box, engine: engine, logs: logs, runner: runner, svc: svc}, nil
}

func runServe(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Version = version
	setupLogger(cfg.LogLevel)
	// Never leave core dumps containing decrypted credentials on disk.
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		slog.Warn("cannot disable core dumps", "error", err)
	}
	slog.Info("starting kpccssl", "version", version, "mode", cmd, "data_dir", cfg.DataDir, "fake_acme", cfg.FakeACME)

	a, err := buildApp(cfg)
	if err != nil {
		return err
	}
	defer a.store.Close()

	needsSetup, err := a.svc.NeedsSetup(context.Background())
	if err != nil {
		return err
	}
	if needsSetup {
		slog.Warn("first-run setup required", "setup_token", a.svc.SetupTokenLogValue())
		tokenPath := filepath.Join(cfg.DataDir, "setup-token")
		if err := os.WriteFile(tokenPath, []byte(a.svc.SetupTokenLogValue()+"\n"), 0o600); err != nil {
			slog.Warn("cannot persist setup token", "error", err)
		} else {
			slog.Warn("setup token written to file", "path", tokenPath)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Reconcile before starting workers so a job cannot be claimed and then
	// marked interrupted by the crash-recovery sweep.
	if err := a.svc.Reconcile(ctx); err != nil {
		slog.Warn("startup reconciliation failed", "error", err)
	}
	for i := 0; i < cfg.JobConcurrency; i++ {
		go a.runner.Run(ctx)
	}
	sched := &service.Scheduler{Service: a.svc, Store: a.store}
	go sched.Run(ctx)

	var httpSrv *http.Server
	if cmd == "serve" {
		api.SetWebHandler(webui.Handler())
		am := auth.NewManager(a.store, cfg.SessionCookieName(), cfg.Secure())
		srv := api.New(cfg, a.store, a.svc, am, version)
		httpSrv = &http.Server{
			Addr:              cfg.Listen,
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       120 * time.Second,
			// No WriteTimeout: SSE streams live as long as a job runs.
		}
		go func() {
			slog.Info("http listening", "addr", cfg.Listen)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server failed", "error", err)
				stop()
			}
		}()
	}
	<-ctx.Done()
	slog.Info("shutting down")
	if httpSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	down := fs.Bool("down", false, "roll back one migration")
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg.LogLevel)
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *down {
		return errors.New("down migrations are intentionally unsupported in production; restore a backup instead")
	}
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}
	fmt.Println("migrations applied")
	return nil
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("KPCCSSL doctor %s (%s)\n\n", version, commit)
	failed := 0
	check := func(name string, err error) {
		if err != nil {
			failed++
			fmt.Printf("FAIL  %-24s %v\n", name, err)
			return
		}
		fmt.Printf("OK    %-24s\n", name)
	}
	warn := func(name string, err error) {
		fmt.Printf("WARN  %-24s %v\n", name, err)
	}
	check("data directory", checkDir(cfg.DataDir))
	check("acme home", checkDir(cfg.ACMEHome))
	check("cert directory", checkDir(cfg.CertDir))
	check("log directory", checkDir(cfg.LogDir))
	if _, err := os.Stat(cfg.MasterKeyFile); os.IsNotExist(err) {
		warn("master key", errors.New("not created yet; first serve will generate it"))
	} else {
		check("master key perms", checkFileMode(cfg.MasterKeyFile, 0o077))
	}
	st, err := store.Open(cfg.DBPath)
	check("database", err)
	if err == nil {
		defer st.Close()
		check("database migrate", st.Migrate(context.Background()))
		checkCAs(context.Background(), st, check, warn)
	}
	for _, p := range []struct {
		name string
		port int
	}{{"HTTP-01 port", cfg.HTTPPort}, {"TLS-ALPN port", cfg.TLSPort}} {
		ln, lerr := net.Listen("tcp", fmt.Sprintf(":%d", p.port))
		if lerr != nil {
			warn(p.name, fmt.Errorf(":%d is in use (%v)", p.port, lerr))
			continue
		}
		ln.Close()
		fmt.Printf("OK    %-24s :%d free\n", p.name, p.port)
	}
	bin := cfg.ACMEBin
	if cfg.FakeACME {
		fmt.Printf("WARN  fake ACME engine enabled; acme.sh checks skipped\n")
	} else {
		st, err := os.Stat(bin)
		if err == nil && st.Mode()&0o111 == 0 {
			err = errors.New("not executable")
		}
		check("acme.sh binary", err)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			v, verr := acmesh.New(bin, cfg.ACMEHome).Version(ctx)
			cancel()
			check("acme.sh version", verr)
			if verr == nil {
				fmt.Printf("      %-24s %s\n", "version", v)
			}
		}
		bad, err := acmesh.InspectMainAccountConf(filepath.Join(cfg.ACMEHome, "account.conf"))
		check("account.conf credentials", err)
		if len(bad) > 0 {
			fmt.Printf("FAIL  %-24s SAVED_* keys: %v\n", "account.conf secrets", bad)
			failed++
		}
	}
	check("tmp directory", checkDir(filepath.Join(cfg.DataDir, "tmp")))
	fmt.Printf("\nserver time: %s\n", time.Now().UTC().Format(time.RFC3339))
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	fmt.Println("all checks passed")
	return nil
}

// checkCAs verifies ACME directory reachability and clock skew.
func checkCAs(ctx context.Context, st *store.Store, check, warn func(string, error)) {
	cas, err := st.ListCAAccounts(ctx)
	if err != nil || len(cas) == 0 {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, ca := range cas {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ca.DirectoryURL, nil)
		if err != nil {
			check("CA "+ca.Name, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			check("CA "+ca.Name, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			check("CA "+ca.Name, fmt.Errorf("HTTP %d", resp.StatusCode))
			continue
		}
		if date := resp.Header.Get("Date"); date != "" {
			if t, err := http.ParseTime(date); err == nil {
				if skew := time.Since(t); skew > 5*time.Minute || skew < -5*time.Minute {
					warn("clock skew", fmt.Errorf("%s vs CA %s", time.Now().UTC().Format(time.RFC3339), date))
				}
			}
		}
		fmt.Printf("OK    %-24s %s\n", "CA "+ca.Name, resp.Status)
	}
}

func checkDir(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("not a directory")
	}
	f, err := os.CreateTemp(path, ".doctor-*")
	if err != nil {
		return fmt.Errorf("not writable: %w", err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

func checkFileMode(path string, forbidden os.FileMode) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&forbidden != 0 {
		return fmt.Errorf("permissions %04o are too open", st.Mode().Perm())
	}
	return nil
}

func runAdmin(args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		return errors.New("usage: kpccssl admin reset-password [--email <email>]")
	}
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	email := fs.String("email", "", "user email")
	_ = fs.Parse(args[1:])
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg.LogLevel)
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *email == "" {
		users, err := st.ListUsers(context.Background())
		if err != nil {
			return err
		}
		if len(users) == 0 {
			return errors.New("no users exist; start the server and complete setup")
		}
		*email = users[0].Email
		fmt.Printf("resetting password for %s\n", *email)
	}
	u, err := st.GetUserByEmail(context.Background(), strings.ToLower(*email))
	if err != nil {
		return err
	}
	fmt.Print("new password: ")
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		r := bufio.NewReader(os.Stdin)
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			return rerr
		}
		pw = []byte(strings.TrimSpace(line))
	}
	if err := auth.ValidatePasswordPolicy(string(pw), u.Email); err != nil {
		return err
	}
	hash, err := auth.HashPassword(string(pw))
	if err != nil {
		return err
	}
	now := store.Now()
	if err := st.UpdateUserPassword(context.Background(), u.ID, hash, now); err != nil {
		return err
	}
	if err := st.DeleteUserSessions(context.Background(), u.ID, ""); err != nil {
		return err
	}
	_ = st.InsertAudit(context.Background(), &store.AuditLog{
		ID: id.New(), ActorID: &u.ID, Action: "admin.reset_password", ResourceType: "user",
		ResourceID: &u.ID, Result: "ok", CreatedAt: now,
	})
	fmt.Println("password updated and sessions invalidated")
	return nil
}
