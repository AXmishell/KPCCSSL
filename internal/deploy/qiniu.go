package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// QiniuConfig deploys a certificate to a Qiniu CDN domain. The certificate is
// uploaded to the Qiniu certificate store first and then bound to the domain.
type QiniuConfig struct {
	AccessKey    string `json:"access_key"`
	AccessSecret string `json:"access_secret"`
	Domain       string `json:"domain"`
}

func (c QiniuConfig) validateAuth() error {
	if strings.TrimSpace(c.AccessKey) == "" {
		return errors.New("access_key is required")
	}
	if strings.TrimSpace(c.AccessSecret) == "" {
		return errors.New("access_secret is required")
	}
	return nil
}

func (c QiniuConfig) validate() error {
	if err := c.validateAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Domain) == "" {
		return errors.New("domain is required")
	}
	return nil
}

// qiniuToken builds the QBox management token. It matches the Qiniu Go SDK:
// management JSON requests are signed by the request path (with query) plus a
// trailing newline. The request body is not part of the signature.
func qiniuToken(accessKey, secretKey, path, query string) string {
	payload := path
	if query != "" {
		payload += "?" + query
	}
	payload += "\n"
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(payload))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))
	return "QBox " + accessKey + ":" + sign
}

// qiniuAPIBase is a variable so tests can point the client at a local server.
var qiniuAPIBase = "https://api.qiniu.com"

type qiniuErrorResponse struct {
	Code  int    `json:"code"`
	Error string `json:"error"`
}

func qiniuRequest(ctx context.Context, cfg QiniuConfig, method, path, query string, body []byte) ([]byte, error) {
	rawURL := qiniuAPIBase + path
	if query != "" {
		rawURL += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", qiniuToken(cfg.AccessKey, cfg.AccessSecret, path, query))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Qiniu: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail qiniuErrorResponse
		_ = json.Unmarshal(data, &detail)
		if detail.Error != "" {
			return nil, fmt.Errorf("Qiniu %d: %s", detail.Code, detail.Error)
		}
		return nil, fmt.Errorf("Qiniu returned HTTP %d", resp.StatusCode)
	}
	return data, nil
}

// TestQiniu lists certificates to verify the credentials.
func TestQiniu(ctx context.Context, cfg QiniuConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	_, err := qiniuRequest(ctx, cfg, http.MethodGet, "/sslcert", "", nil)
	return err
}

type qiniuCertResponse struct {
	CertID string `json:"certID"`
}

// DeployQiniu uploads the certificate and enables HTTPS for every domain.
func DeployQiniu(ctx context.Context, cfg QiniuConfig, b *Bundle) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"pri": string(b.Key),
		"ca":  string(b.CertChain()),
	})
	if err != nil {
		return err
	}
	uploaded, err := qiniuRequest(ctx, cfg, http.MethodPost, "/sslcert", "", body)
	if err != nil {
		return fmt.Errorf("upload certificate to Qiniu: %w", err)
	}
	var cert qiniuCertResponse
	if err := json.Unmarshal(uploaded, &cert); err != nil {
		return fmt.Errorf("parse Qiniu certificate response: %w", err)
	}
	if cert.CertID == "" {
		return errors.New("Qiniu did not return certID")
	}

	bindBody, err := json.Marshal(map[string]string{"certid": cert.CertID})
	if err != nil {
		return err
	}
	for _, domain := range splitDomains(cfg.Domain) {
		path := "/domain/" + url.PathEscape(domain) + "/sslize"
		if _, err := qiniuRequest(ctx, cfg, http.MethodPut, path, "", bindBody); err != nil {
			return fmt.Errorf("bind certificate to Qiniu domain %s: %w", domain, err)
		}
	}
	return nil
}
