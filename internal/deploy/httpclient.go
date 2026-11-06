package deploy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// panelHTTPClient returns a client for admin-configured panel endpoints.
// Panels usually live on a private network and often use self-signed
// certificates, so callers may explicitly opt out of TLS verification.
func panelHTTPClient(ignoreTLS bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ignoreTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit per-target opt-in
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return errors.New("refusing cross-host redirect from panel")
			}
			return nil
		},
	}
}

func validatePanelURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid panel URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("panel URL must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("panel URL must include a host")
	}
	return nil
}

func joinPanelURL(base, endpoint string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	pathPart, query, _ := strings.Cut(endpoint, "?")
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(pathPart, "/")
	u.RawQuery = query
	return u.String(), nil
}

func readLimited(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func postJSON(ctx context.Context, client *http.Client, rawURL string, in any, headers map[string]string) ([]byte, int, error) {
	var body []byte
	var err error
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil {
			return nil, 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	data, err := readLimited(resp, 1<<20)
	return data, resp.StatusCode, err
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	data, err := readLimited(resp, 1<<20)
	return data, resp.StatusCode, err
}
