package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TencentConfig deploys to Tencent Cloud CDN, WAF, EdgeOne (TEO) or COS
// through the SSL service. COS additionally needs the bucket name.
type TencentConfig struct {
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
	Region    string `json:"region"`
	Domain    string `json:"domain"`
	Bucket    string `json:"bucket"`
}

func (c TencentConfig) validateAuth() error {
	if strings.TrimSpace(c.SecretID) == "" {
		return errors.New("secret_id is required")
	}
	if strings.TrimSpace(c.SecretKey) == "" {
		return errors.New("secret_key is required")
	}
	return nil
}

func (c TencentConfig) validate(depType string) error {
	if err := c.validateAuth(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Domain) == "" {
		return errors.New("domain is required")
	}
	if depType == "tencent_cos" && strings.TrimSpace(c.Bucket) == "" {
		return errors.New("bucket is required")
	}
	return nil
}

func (c TencentConfig) region() string {
	if strings.TrimSpace(c.Region) == "" {
		return "ap-beijing"
	}
	return strings.TrimSpace(c.Region)
}

const tencentSSLHost = "ssl.tencentcloudapi.com"

func tencentRequest(ctx context.Context, cfg TencentConfig, action string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ts := time.Now().Unix()
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")
	contentType := "application/json; charset=utf-8"

	hashedPayload := sha256.Sum256(body)
	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		"/",
		"",
		"content-type:" + contentType,
		"host:" + tencentSSLHost,
		"",
		"content-type;host",
		hex.EncodeToString(hashedPayload[:]),
	}, "\n")

	scope := date + "/ssl/tc3_request"
	hashedCanonical := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		strconv.FormatInt(ts, 10),
		scope,
		hex.EncodeToString(hashedCanonical[:]),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+cfg.SecretKey), date)
	secretService := hmacSHA256(secretDate, "ssl")
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+tencentSSLHost, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2019-12-05")
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-TC-Region", cfg.region())
	req.Header.Set("Authorization", fmt.Sprintf(
		"TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%s",
		cfg.SecretID, scope, signature,
	))

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Tencent Cloud: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Response map[string]any `json:"Response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse Tencent Cloud response: %w (HTTP %d)", err, resp.StatusCode)
	}
	if envelope.Response == nil {
		return nil, fmt.Errorf("Tencent Cloud returned an empty response (HTTP %d)", resp.StatusCode)
	}
	if raw, ok := envelope.Response["Error"]; ok {
		errObj, _ := raw.(map[string]any)
		return nil, fmt.Errorf("Tencent Cloud %s: %s", stringValue(errObj["Code"]), stringValue(errObj["Message"]))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Tencent Cloud returned HTTP %d", resp.StatusCode)
	}
	return envelope.Response, nil
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// TestTencent validates credentials by listing certificates.
func TestTencent(ctx context.Context, depType string, cfg TencentConfig) error {
	if err := cfg.validate(depType); err != nil {
		return err
	}
	_, err := tencentRequest(ctx, cfg, "DescribeCertificates", map[string]any{"Limit": 1})
	return err
}

// tencentResourceType maps a deployment type to the SSL service resource type.
// Tencent Cloud deploys CDN, WAF and EdgeOne certificates through the same
// DeployCertificateInstance action and only differs in ResourceType.
func tencentResourceType(depType string) (string, bool) {
	switch depType {
	case "tencent_cdn":
		return "cdn", true
	case "tencent_waf":
		return "waf", true
	case "tencent_teo":
		return "teo", true
	case "tencent_cos":
		return "cos", true
	}
	return "", false
}

// tencentInstanceIDs builds the instance list. COS uses the
// "region|bucket|domain" form; every other product accepts the domain list.
func tencentInstanceIDs(depType string, cfg TencentConfig) ([]string, error) {
	domains := splitDomains(cfg.Domain)
	if len(domains) == 0 {
		return nil, errors.New("domain is required")
	}
	if depType != "tencent_cos" {
		return domains, nil
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	ids := make([]string, 0, len(domains))
	for _, domain := range domains {
		ids = append(ids, cfg.region()+"|"+bucket+"|"+domain)
	}
	return ids, nil
}

// DeployTencent uploads the certificate and deploys it to the selected product.
func DeployTencent(ctx context.Context, depType string, cfg TencentConfig, b *Bundle) error {
	if err := cfg.validate(depType); err != nil {
		return err
	}
	resourceType, ok := tencentResourceType(depType)
	if !ok {
		return fmt.Errorf("unsupported Tencent deployment type %q", depType)
	}
	uploaded, err := tencentRequest(ctx, cfg, "UploadCertificate", map[string]any{
		"CertificatePublicKey":  string(b.CertChain()),
		"CertificatePrivateKey": string(b.Key),
		"Repeatable":            false,
	})
	if err != nil {
		return fmt.Errorf("upload certificate: %w", err)
	}
	certID := stringValue(uploaded["CertificateId"])
	if certID == "" {
		return errors.New("Tencent Cloud did not return CertificateId")
	}

	instances, err := tencentInstanceIDs(depType, cfg)
	if err != nil {
		return err
	}
	result, err := tencentRequest(ctx, cfg, "DeployCertificateInstance", map[string]any{
		"CertificateId":  certID,
		"InstanceIdList": instances,
		"ResourceType":   resourceType,
	})
	if err != nil {
		return fmt.Errorf("deploy certificate: %w", err)
	}
	if status, ok := result["DeployStatus"].(float64); ok && status != 1 {
		record := stringValue(result["DeployRecordId"])
		return fmt.Errorf("Tencent Cloud has a deployment in progress (record %s); no new task was created", record)
	}
	return nil
}
