package deploy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BaotaConfig deploys to a Baota (aaPanel) panel. The panel type updates the
// panel's own SSL certificate; the site type uploads the certificate to the
// panel certificate library and binds it to one or more sites.
type BaotaConfig struct {
	URL       string   `json:"url"`
	APIKey    string   `json:"api_key"`
	IgnoreTLS bool     `json:"ignore_tls"`
	SiteNames []string `json:"site_names"`
}

func (c BaotaConfig) validate(site bool) error {
	if err := validatePanelURL(c.URL); err != nil {
		return err
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("api_key is required")
	}
	if site {
		names := make([]string, 0, len(c.SiteNames))
		for _, n := range c.SiteNames {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return errors.New("at least one site name is required")
		}
	}
	return nil
}

func btToken(timestamp, apiKey string) string {
	keyHash := md5.Sum([]byte(apiKey))
	keyHex := strings.ToLower(hex.EncodeToString(keyHash[:]))
	sign := md5.Sum([]byte(timestamp + keyHex))
	return strings.ToLower(hex.EncodeToString(sign[:]))
}

func baotaRequest(ctx context.Context, cfg BaotaConfig, endpoint string, form url.Values) (map[string]any, error) {
	rawURL, err := joinPanelURL(cfg.URL, endpoint)
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	form.Set("request_time", ts)
	form.Set("request_token", btToken(ts, cfg.APIKey))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "KPCCSSL")
	resp, err := panelHTTPClient(cfg.IgnoreTLS).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Baota panel: %w", err)
	}
	body, err := readLimited(resp, 1<<20)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse Baota response: %w (HTTP %d)", err, resp.StatusCode)
	}
	if status, ok := out["status"].(bool); ok && !status {
		return nil, fmt.Errorf("Baota panel error: %s", stringValue(out["msg"]))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Baota panel returned HTTP %d", resp.StatusCode)
	}
	return out, nil
}

// TestBaota checks the API key and network reachability without changing state.
func TestBaota(ctx context.Context, cfg BaotaConfig) error {
	if err := cfg.validate(false); err != nil {
		return err
	}
	_, err := baotaRequest(ctx, cfg, "system?action=GetNetWork", url.Values{})
	return err
}

// DeployBaota updates either the panel SSL or the configured sites.
func DeployBaota(ctx context.Context, depType string, cfg BaotaConfig, b *Bundle) error {
	site := depType == "baota_site"
	if err := cfg.validate(site); err != nil {
		return err
	}
	if !site {
		form := url.Values{}
		form.Set("cert_type", "1")
		form.Set("privateKey", string(b.Key))
		form.Set("certPem", string(b.CertChain()))
		if _, err := baotaRequest(ctx, cfg, "config?action=SetPanelSSL", form); err != nil {
			return fmt.Errorf("deploy to Baota panel SSL: %w", err)
		}
		return nil
	}

	// Upload to the certificate library first, then bind the resulting hash to
	// every site. This mirrors the panel's own multi-site flow.
	form := url.Values{}
	form.Set("key", string(b.Key))
	form.Set("csr", string(b.CertChain()))
	up, err := baotaRequest(ctx, cfg, "ssl/cert/save_cert", form)
	if err != nil {
		return fmt.Errorf("upload certificate to Baota: %w", err)
	}
	hash := stringValue(up["ssl_hash"])
	if hash == "" {
		return errors.New("Baota certificate upload did not return ssl_hash")
	}

	batch := make([]map[string]string, 0, len(cfg.SiteNames))
	for _, name := range cfg.SiteNames {
		if name = strings.TrimSpace(name); name != "" {
			batch = append(batch, map[string]string{"siteName": name, "ssl_hash": hash})
		}
	}
	raw, _ := json.Marshal(batch)
	bind := url.Values{}
	bind.Set("BatchInfo", string(raw))
	if _, err := baotaRequest(ctx, cfg, "ssl?action=SetBatchCertToSite", bind); err != nil {
		return fmt.Errorf("bind certificate to Baota sites: %w", err)
	}
	return nil
}

// OnePanelConfig deploys to a 1Panel panel. Version accepts "v1" (default) or
// "v2". The panel type updates the panel SSL; the site type updates one
// website's HTTPS settings.
type OnePanelConfig struct {
	URL       string `json:"url"`
	APIKey    string `json:"api_key"`
	Version   string `json:"version"`
	IgnoreTLS bool   `json:"ignore_tls"`
	SiteID    string `json:"site_id"`
}

func (c OnePanelConfig) validate(site bool) error {
	if err := validatePanelURL(c.URL); err != nil {
		return err
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("api_key is required")
	}
	if c.Version != "" && c.Version != "v1" && c.Version != "v2" {
		return errors.New("version must be v1 or v2")
	}
	if site {
		if strings.TrimSpace(c.SiteID) == "" {
			return errors.New("site_id is required")
		}
		if _, err := strconv.Atoi(c.SiteID); err != nil {
			return errors.New("site_id must be numeric")
		}
	}
	return nil
}

func onePanelToken(timestamp, apiKey string) string {
	sum := md5.Sum([]byte("1panel" + apiKey + timestamp))
	return hex.EncodeToString(sum[:])
}

func onePanelRequest(ctx context.Context, cfg OnePanelConfig, method, endpoint string, in map[string]any) (map[string]any, error) {
	prefix := "api/v1/"
	version := cfg.Version
	if version == "" {
		version = "v1"
	}
	if version == "v2" {
		endpoint = strings.TrimLeft(endpoint, "/")
		if endpoint == "settings" || strings.HasPrefix(endpoint, "settings/") {
			endpoint = "core/" + endpoint
		}
		prefix = "api/v2/"
		if in == nil {
			in = map[string]any{}
		}
		in["ssl"] = "Enable"
	}
	rawURL, err := joinPanelURL(cfg.URL, prefix+strings.TrimLeft(endpoint, "/"))
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	headers := map[string]string{
		"1Panel-Timestamp": ts,
		"1Panel-Token":     onePanelToken(ts, cfg.APIKey),
		"User-Agent":       "KPCCSSL",
	}
	client := panelHTTPClient(cfg.IgnoreTLS)
	var body []byte
	var status int
	if method == http.MethodGet {
		body, status, err = getJSON(ctx, client, rawURL, headers)
	} else {
		body, status, err = postJSON(ctx, client, rawURL, in, headers)
	}
	if err != nil {
		return nil, fmt.Errorf("request 1Panel: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse 1Panel response: %w (HTTP %d)", err, status)
	}
	code, _ := out["code"].(float64)
	if code != 0 && code != 200 {
		return nil, fmt.Errorf("1Panel error: %s", stringValue(out["message"]))
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("1Panel returned HTTP %d", status)
	}
	return out, nil
}

// TestOnePanel checks the API key and panel version.
func TestOnePanel(ctx context.Context, cfg OnePanelConfig) error {
	if err := cfg.validate(false); err != nil {
		return err
	}
	_, err := onePanelRequest(ctx, cfg, http.MethodGet, "settings/upgrade", nil)
	return err
}

// DeployOnePanel updates the panel certificate or a website's HTTPS config.
func DeployOnePanel(ctx context.Context, depType string, cfg OnePanelConfig, b *Bundle) error {
	site := depType == "1panel_site"
	if err := cfg.validate(site); err != nil {
		return err
	}
	if !site {
		_, err := onePanelRequest(ctx, cfg, http.MethodPost, "settings/ssl/update", map[string]any{
			"cert":    string(b.CertChain()),
			"key":     string(b.Key),
			"ssl":     "enable",
			"sslType": "import-paste",
		})
		if err != nil {
			return fmt.Errorf("deploy to 1Panel panel SSL: %w", err)
		}
		return nil
	}

	siteID := strings.TrimSpace(cfg.SiteID)
	detail, err := onePanelRequest(ctx, cfg, http.MethodGet, "websites/"+siteID+"/https", nil)
	if err != nil {
		return fmt.Errorf("get 1Panel site HTTPS config: %w", err)
	}
	data, _ := detail["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	websiteID, _ := strconv.Atoi(siteID)
	protocol := data["SSLProtocol"]
	if protocol == nil {
		protocol = []string{"TLSv1.3", "TLSv1.2", "TLSv1.1", "TLSv1"}
	}
	algorithm := "RSA"
	if v, ok := data["algorithm"].(string); ok && v != "" {
		algorithm = v
	}
	hsts, _ := data["hsts"].(bool)
	httpConfig, _ := data["httpConfig"].(string)
	if httpConfig == "" {
		httpConfig = "HTTPToHTTPS"
	}
	_, err = onePanelRequest(ctx, cfg, http.MethodPost, "websites/"+siteID+"/https", map[string]any{
		"SSLProtocol": protocol,
		"algorithm":   algorithm,
		"certificate": string(b.CertChain()),
		"privateKey":  string(b.Key),
		"enable":      true,
		"hsts":        hsts,
		"httpConfig":  httpConfig,
		"importType":  "paste",
		"type":        "manual",
		"websiteId":   websiteID,
	})
	if err != nil {
		return fmt.Errorf("deploy to 1Panel site: %w", err)
	}
	return nil
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}
