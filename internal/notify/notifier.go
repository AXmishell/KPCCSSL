// Package notify delivers certificate lifecycle events to SMTP, webhook and
// Telegram channels with deduplication and bounded retries.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
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

	// UseSystemMail makes a notification channel reuse the transactional SMTP
	// settings and only store channel-specific recipients/overrides.
	UseSystemMail bool `json:"use_system_mail,omitempty"`
	// AllowInsecureAuth permits SMTP AUTH over a plaintext connection. It is
	// only useful for trusted relays that do not offer STARTTLS.
	AllowInsecureAuth bool `json:"allow_insecure_auth,omitempty"`
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

// FeishuConfig configures a Feishu/Lark custom bot. Secret enables request
// signing and is optional.
type FeishuConfig struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret"`
}

// DingTalkConfig configures a DingTalk custom robot. Secret enables request
// signing and is optional.
type DingTalkConfig struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret"`
}

// WeComConfig configures a WeCom (WeChat Work) group robot.
type WeComConfig struct {
	WebhookURL string `json:"webhook_url"`
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

	// SystemMail resolves the transactional mail transport. Notification SMTP
	// channels with use_system_mail=true reuse it.
	SystemMail func(ctx context.Context) (SMTPConfig, error)
	// MailSender delivers one SMTP message. It defaults to SendSMTP and is
	// replaceable in tests.
	MailSender func(ctx context.Context, cfg SMTPConfig, subject, body string) error
}

// New builds a notifier with a safe HTTP client.
func New(st *store.Store, box *secret.Box) *Notifier {
	return &Notifier{
		Store:      st,
		Box:        box,
		MailSender: SendSMTP,
		Client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: deploy.SafeTransport(),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirects are not followed")
			},
		},
	}
}

// A PermanentError marks a delivery failure that will not succeed on retry,
// such as an invalid configuration or missing credentials.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	var pe *PermanentError
	if errors.As(err, &pe) {
		return err
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err is non-retryable.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
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
			if lastErr == nil || IsPermanent(lastErr) || attempts >= 3 {
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
		return Permanent(fmt.Errorf("decrypt channel: %w", err))
	}
	switch ch.Type {
	case "smtp":
		var cfg SMTPConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		if cfg.UseSystemMail {
			if n.SystemMail == nil {
				return Permanent(errors.New("system mail is not configured"))
			}
			base, err := n.SystemMail(ctx)
			if err != nil {
				return Permanent(err)
			}
			if strings.TrimSpace(cfg.From) != "" {
				base.From = cfg.From
			}
			base.To = cfg.To
			base.UseSystemMail = false
			base.AllowInsecureAuth = cfg.AllowInsecureAuth
			cfg = base
		}
		if err := ValidateSMTPConfig(&cfg, true, true); err != nil {
			return Permanent(err)
		}
		sender := n.MailSender
		if sender == nil {
			sender = SendSMTP
		}
		return sender(ctx, cfg, subject, body)
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		return n.sendWebhook(ctx, cfg, ch, subject, body)
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		if cfg.BotToken == "" || cfg.ChatID == "" {
			return Permanent(errors.New("bot_token and chat_id are required"))
		}
		return n.sendTelegram(ctx, cfg, subject, body)
	case "feishu":
		var cfg FeishuConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		return n.sendFeishu(ctx, cfg, subject, body)
	case "dingtalk":
		var cfg DingTalkConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		return n.sendDingTalk(ctx, cfg, subject, body)
	case "wecom":
		var cfg WeComConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return Permanent(fmt.Errorf("invalid config: %w", err))
		}
		return n.sendWeCom(ctx, cfg, subject, body)
	}
	return Permanent(fmt.Errorf("unsupported channel type %q", ch.Type))
}

// NormalizeConfig validates a channel config and returns the canonical JSON
// that should be persisted. Defaults are written back so validation-time and
// send-time behaviour cannot diverge.
func (n *Notifier) NormalizeConfig(chType string, configJSON []byte) ([]byte, error) {
	switch chType {
	case "smtp":
		var cfg SMTPConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if cfg.UseSystemMail {
			// Transport credentials live in the settings table. Only the
			// recipients and an optional From override belong to the channel.
			cfg.Host, cfg.Username, cfg.Password = "", "", ""
			cfg.Port, cfg.TLS = 0, ""
			if err := normalizeSMTPConfig(&cfg); err != nil {
				return nil, err
			}
			if len(cfg.To) == 0 {
				return nil, errors.New("to is required")
			}
		} else if err := ValidateSMTPConfig(&cfg, true, true); err != nil {
			return nil, err
		}
		return json.Marshal(cfg)
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if err := deploy.ValidateWebhookURL(cfg.URL); err != nil {
			return nil, err
		}
		return json.Marshal(cfg)
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if cfg.BotToken == "" || cfg.ChatID == "" {
			return nil, errors.New("bot_token and chat_id are required")
		}
		return json.Marshal(cfg)
	case "feishu":
		var cfg FeishuConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if err := validateBotURL(cfg.WebhookURL, "open.feishu.cn", "open.larksuite.com"); err != nil {
			return nil, err
		}
		return json.Marshal(cfg)
	case "dingtalk":
		var cfg DingTalkConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if err := validateBotURL(cfg.WebhookURL, "oapi.dingtalk.com"); err != nil {
			return nil, err
		}
		return json.Marshal(cfg)
	case "wecom":
		var cfg WeComConfig
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if err := validateBotURL(cfg.WebhookURL, "qyapi.weixin.qq.com"); err != nil {
			return nil, err
		}
		return json.Marshal(cfg)
	}
	return nil, errors.New("unsupported channel type")
}

// Validate checks a channel config before storing it.
func (n *Notifier) Validate(chType, configJSON string) error {
	_, err := n.NormalizeConfig(chType, []byte(configJSON))
	return err
}

// NormalizeSMTPConfig trims fields and applies transport defaults.
func NormalizeSMTPConfig(cfg *SMTPConfig) error {
	return normalizeSMTPConfig(cfg)
}

// ValidateSMTPConfig normalizes and validates a mail transport configuration.
func ValidateSMTPConfig(cfg *SMTPConfig, requireFrom, requireTo bool) error {
	if err := normalizeSMTPConfig(cfg); err != nil {
		return err
	}
	if cfg.Host == "" {
		return errors.New("SMTP host is required")
	}
	if requireFrom && cfg.From == "" {
		return errors.New("sender address is required")
	}
	if requireTo && len(cfg.To) == 0 {
		return errors.New("at least one recipient is required")
	}
	return nil
}

func normalizeSMTPConfig(cfg *SMTPConfig) error {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.TLS = strings.ToLower(strings.TrimSpace(cfg.TLS))

	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("SMTP port must be between 1 and 65535")
	}
	if cfg.TLS == "" {
		cfg.TLS = "starttls"
	}
	switch cfg.TLS {
	case "tls", "starttls", "none":
	default:
		return errors.New("SMTP TLS mode must be tls, starttls or none")
	}
	if cfg.From != "" {
		from, err := mail.ParseAddress(cfg.From)
		if err != nil || strings.ContainsAny(from.Address, "\r\n") {
			return errors.New("invalid sender address")
		}
		cfg.From = from.Address
	}
	for i, to := range cfg.To {
		to = strings.TrimSpace(to)
		if to == "" {
			return errors.New("recipient address is required")
		}
		addr, err := mail.ParseAddress(to)
		if err != nil || strings.ContainsAny(addr.Address, "\r\n") {
			return fmt.Errorf("invalid recipient address")
		}
		cfg.To[i] = addr.Address
	}
	if cfg.Username != "" && cfg.TLS == "none" && !cfg.AllowInsecureAuth {
		return errors.New("SMTP authentication requires TLS; set allow_insecure_auth to override")
	}
	return nil
}

func (n *Notifier) sendWebhook(ctx context.Context, cfg WebhookConfig, ch *store.NotificationChannel, subject, body string) error {
	if err := deploy.ValidateWebhookURL(cfg.URL); err != nil {
		return Permanent(err)
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

// validateBotURL only accepts HTTPS webhooks on the vendor's official host.
// This prevents users from turning a notification channel into an SSRF probe.
func validateBotURL(raw string, hosts ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid webhook URL")
	}
	if u.Scheme != "https" {
		return errors.New("webhook URL must use https")
	}
	if u.Host == "" || u.User != nil {
		return errors.New("webhook URL must include a host")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("webhook URL must use port 443")
	}
	for _, host := range hosts {
		if strings.EqualFold(u.Hostname(), host) {
			return nil
		}
	}
	return fmt.Errorf("webhook URL must point to %s", strings.Join(hosts, " or "))
}

// feishuSign builds the Feishu custom-bot signature. The timestamp and secret
// form the HMAC key and the message is empty.
func feishuSign(timestamp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", timestamp, secret)))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// dingTalkSign signs "timestamp\nsecret" with the secret as the HMAC key.
func dingTalkSign(timestamp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d\n%s", timestamp, secret)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// botResponseError extracts the error returned by Feishu, DingTalk and WeCom.
func botResponseError(data []byte) error {
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("bot returned errcode %d: %s", out.ErrCode, out.ErrMsg)
	}
	if out.Code != 0 {
		return fmt.Errorf("bot returned code %d: %s", out.Code, out.Msg)
	}
	return nil
}

func (n *Notifier) sendFeishu(ctx context.Context, cfg FeishuConfig, subject, body string) error {
	if err := validateBotURL(cfg.WebhookURL, "open.feishu.cn", "open.larksuite.com"); err != nil {
		return Permanent(err)
	}
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": subject + "\n\n" + body},
	}
	if cfg.Secret != "" {
		ts := time.Now().Unix()
		payload["timestamp"] = fmt.Sprint(ts)
		payload["sign"] = feishuSign(ts, cfg.Secret)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.postBotJSON(ctx, "Feishu", cfg.WebhookURL, data)
}

func (n *Notifier) sendDingTalk(ctx context.Context, cfg DingTalkConfig, subject, body string) error {
	if err := validateBotURL(cfg.WebhookURL, "oapi.dingtalk.com"); err != nil {
		return Permanent(err)
	}
	target, err := url.Parse(cfg.WebhookURL)
	if err != nil {
		return err
	}
	if cfg.Secret != "" {
		ts := time.Now().UnixMilli()
		q := target.Query()
		q.Set("timestamp", fmt.Sprint(ts))
		q.Set("sign", dingTalkSign(ts, cfg.Secret))
		target.RawQuery = q.Encode()
	}
	data, err := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": subject + "\n\n" + body},
	})
	if err != nil {
		return err
	}
	return n.postBotJSON(ctx, "DingTalk", target.String(), data)
}

func (n *Notifier) sendWeCom(ctx context.Context, cfg WeComConfig, subject, body string) error {
	if err := validateBotURL(cfg.WebhookURL, "qyapi.weixin.qq.com"); err != nil {
		return Permanent(err)
	}
	data, err := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": subject + "\n\n" + body},
	})
	if err != nil {
		return err
	}
	return n.postBotJSON(ctx, "WeCom", cfg.WebhookURL, data)
}

func (n *Notifier) postBotJSON(ctx context.Context, name, rawURL string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		return httpError(name+" request failed", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", name, resp.StatusCode)
	}
	if err := botResponseError(respBody); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// httpError strips the request URL from transport errors. Telegram and the
// chat bots embed secrets in their webhook URLs, so returning *url.Error
// verbatim would leak the token into logs, audit rows and API responses.
func httpError(prefix string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %v", prefix, ue.Err)
	}
	return fmt.Errorf("%s: %v", prefix, err)
}

// SendSMTP delivers a plain-text message using an explicit configuration. It is
// used by the authentication mailer in addition to notification channels.
func SendSMTP(ctx context.Context, cfg SMTPConfig, subject, body string) error {
	return sendSMTP(ctx, cfg, subject, body)
}

func sendSMTP(ctx context.Context, cfg SMTPConfig, subject, body string) error {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.TLS == "" {
		// Defense in depth: a missing mode must never fall back to plaintext.
		cfg.TLS = "starttls"
	}
	if cfg.TLS == "none" && cfg.Username != "" && !cfg.AllowInsecureAuth {
		return errors.New("SMTP authentication requires TLS")
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
