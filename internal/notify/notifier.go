// Package notify delivers certificate lifecycle events to SMTP, webhook and
// Telegram channels with deduplication and bounded retries.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/store"
)

// SMTPConfig configures a mail channel.
type SMTPConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	TLS      string   `json:"tls"` // tls | starttls | none
}

// WebhookConfig posts signed JSON.
type WebhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// TelegramConfig configures a bot channel.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// Event is a notification payload.
type Event struct {
	Type  string
	Key   string
	Title string
	Body  string
}

// Notifier dispatches events.
type Notifier struct {
	Store  *store.Store
	Box    *secret.Box
	Client *http.Client
}

// New builds a notifier with a safe HTTP client.
func New(st *store.Store, box *secret.Box) *Notifier {
	return &Notifier{
		Store: st,
		Box:   box,
		Client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: deploy.SafeTransport(),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirects are not followed")
			},
		},
	}
}

// Dispatch sends an event to every subscribed channel. It returns an aggregate
// error but never stops on the first failure.
func (n *Notifier) Dispatch(ctx context.Context, ev Event) error {
	channels, err := n.Store.EnabledChannelsForEvent(ctx, ev.Type)
	if err != nil {
		return err
	}
	var errs []string
	for _, ch := range channels {
		deliveryID := id.New()
		claimed, err := n.Store.ClaimDelivery(ctx, deliveryID, ch.ID, ev.Type, ev.Key, store.Now())
		if err != nil {
			errs = append(errs, "dedupe: "+err.Error())
			continue
		}
		if !claimed {
			continue
		}
		var lastErr error
		attempts := 0
		for attempts < 3 {
			attempts++
			lastErr = n.Send(ctx, &ch, ev.Title, ev.Body)
			if lastErr == nil {
				break
			}
			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
			case <-time.After(time.Duration(attempts) * time.Second):
			}
		}
		status := "sent"
		var msg *string
		if lastErr != nil {
			status = "failed"
			m := lastErr.Error()
			msg = &m
			errs = append(errs, ch.Name+": "+m)
		}
		_ = n.Store.FinishDelivery(ctx, deliveryID, status, attempts, msg, store.Now())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Send delivers a message to one channel.
func (n *Notifier) Send(ctx context.Context, ch *store.NotificationChannel, subject, body string) error {
	plain, err := n.Box.Decrypt(ch.ConfigEnc, "notification_channels:"+ch.ID+":config")
	if err != nil {
		return fmt.Errorf("decrypt channel: %w", err)
	}
	switch ch.Type {
	case "smtp":
		var cfg SMTPConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return sendSMTP(ctx, cfg, subject, body)
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return n.sendWebhook(ctx, cfg, ch, subject, body)
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return n.sendTelegram(ctx, cfg, subject, body)
	}
	return fmt.Errorf("unsupported channel type %q", ch.Type)
}

// Validate checks a channel config before storing it.
func (n *Notifier) Validate(chType, configJSON string) error {
	switch chType {
	case "smtp":
		var cfg SMTPConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if cfg.Host == "" || cfg.From == "" || len(cfg.To) == 0 {
			return errors.New("host, from and to are required")
		}
		if cfg.Port == 0 {
			cfg.Port = 587
		}
		if cfg.TLS == "" {
			cfg.TLS = "starttls"
		}
		if cfg.TLS != "tls" && cfg.TLS != "starttls" && cfg.TLS != "none" {
			return errors.New("tls must be tls, starttls or none")
		}
		return nil
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return deploy.ValidateWebhookURL(cfg.URL)
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if cfg.BotToken == "" || cfg.ChatID == "" {
			return errors.New("bot_token and chat_id are required")
		}
		return nil
	}
	return errors.New("unsupported channel type")
}

func (n *Notifier) sendWebhook(ctx context.Context, cfg WebhookConfig, ch *store.NotificationChannel, subject, body string) error {
	if err := deploy.ValidateWebhookURL(cfg.URL); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"channel": ch.Name, "subject": subject, "body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		mac.Write(payload)
		req.Header.Set("X-KPCCSSL-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		return httpError("webhook request failed", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) sendTelegram(ctx context.Context, cfg TelegramConfig, subject, body string) error {
	u := "https://api.telegram.org/bot" + url.PathEscape(cfg.BotToken) + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", cfg.ChatID)
	form.Set("text", subject+"\n\n"+body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := n.Client.Do(req)
	if err != nil {
		return httpError("telegram request failed", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// httpError strips the request URL from transport errors. Telegram embeds the
// bot token in its URL, so returning *url.Error verbatim would leak the token
// into logs, audit rows and API responses.
func httpError(prefix string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %v", prefix, ue.Err)
	}
	return fmt.Errorf("%s: %v", prefix, err)
}

func sendSMTP(ctx context.Context, cfg SMTPConfig, subject, body string) error {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	switch cfg.TLS {
	case "tls":
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	default:
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()
	if cfg.TLS == "starttls" {
		ok, _ := c.Extension("STARTTLS")
		if !ok {
			return errors.New("smtp server does not support STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return err
	}
	for _, to := range cfg.To {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + sanitizeHeader(cfg.From) + "\r\n" +
		"To: " + sanitizeHeader(strings.Join(cfg.To, ", ")) + "\r\n" +
		"Subject: " + sanitizeHeader(subject) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
