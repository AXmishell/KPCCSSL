package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AliyunConfig deploys to Alibaba Cloud CDN or WAF.
type AliyunConfig struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	Region          string `json:"region"`
	Domain          string `json:"domain"`
}

func (c AliyunConfig) validate() error {
	if strings.TrimSpace(c.AccessKeyID) == "" {
		return errors.New("access_key_id is required")
	}
	if strings.TrimSpace(c.AccessKeySecret) == "" {
		return errors.New("access_key_secret is required")
	}
	if strings.TrimSpace(c.Domain) == "" {
		return errors.New("domain is required")
	}
	return nil
}

func (c AliyunConfig) region() string {
	if strings.TrimSpace(c.Region) == "" {
		return "cn-hangzhou"
	}
	return strings.TrimSpace(c.Region)
}

func aliyunPercentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func aliyunRPC(ctx context.Context, endpoint, version, action, accessKeyID, accessKeySecret string, params url.Values) (map[string]any, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("AccessKeyId", accessKeyID)
	params.Set("Action", action)
	params.Set("Format", "JSON")
	params.Set("SignatureMethod", "HMAC-SHA1")
	params.Set("SignatureVersion", "1.0")
	params.Set("Version", version)
	params.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	params.Set("SignatureNonce", hex.EncodeToString(nonce[:]))

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for i, k := range keys {
		if i > 0 {
			canonical.WriteByte('&')
		}
		canonical.WriteString(aliyunPercentEncode(k))
		canonical.WriteByte('=')
		canonical.WriteString(aliyunPercentEncode(params.Get(k)))
	}
	stringToSign := "POST&%2F&" + aliyunPercentEncode(canonical.String())
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	params.Set("Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Alibaba Cloud: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse Alibaba Cloud response: %w (HTTP %d)", err, resp.StatusCode)
	}
	if code := stringValue(out["Code"]); code != "" {
		return nil, fmt.Errorf("Alibaba Cloud %s: %s", code, stringValue(out["Message"]))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Alibaba Cloud returned HTTP %d", resp.StatusCode)
	}
	return out, nil
}

// TestAliyun validates credentials against the selected cloud product.
func TestAliyun(ctx context.Context, depType string, cfg AliyunConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if depType == "aliyun_waf" {
		_, err := aliyunWAFInstance(ctx, cfg)
		return err
	}
	_, err := aliyunRPC(ctx, "https://cdn.aliyuncs.com/", "2018-05-10", "DescribeCdnService", cfg.AccessKeyID, cfg.AccessKeySecret, nil)
	return err
}

func aliyunWAFInstance(ctx context.Context, cfg AliyunConfig) (string, error) {
	region := cfg.region()
	endpoint := "https://wafopenapi." + region + ".aliyuncs.com/"
	params := url.Values{"RegionId": {region}}
	out, err := aliyunRPC(ctx, endpoint, "2021-10-01", "DescribeInstance", cfg.AccessKeyID, cfg.AccessKeySecret, params)
	if err != nil {
		return "", err
	}
	instance := stringValue(out["InstanceId"])
	if instance == "" {
		return "", errors.New("Alibaba Cloud WAF instance not found in the selected region")
	}
	return instance, nil
}

// DeployAliyun uploads a certificate to CDN or WAF and activates it.
func DeployAliyun(ctx context.Context, depType string, cfg AliyunConfig, b *Bundle) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if depType == "aliyun_cdn" {
		params := url.Values{
			"DomainName":  {strings.TrimSpace(cfg.Domain)},
			"SSLProtocol": {"on"},
			"SSLPub":      {string(b.CertChain())},
			"SSLPri":      {string(b.Key)},
		}
		_, err := aliyunRPC(ctx, "https://cdn.aliyuncs.com/", "2018-05-10", "SetCdnDomainSSLCertificate", cfg.AccessKeyID, cfg.AccessKeySecret, params)
		if err != nil {
			return fmt.Errorf("deploy to Alibaba Cloud CDN: %w", err)
		}
		return nil
	}
	return deployAliyunWAF(ctx, cfg, b)
}

func deployAliyunWAF(ctx context.Context, cfg AliyunConfig, b *Bundle) error {
	region := cfg.region()
	endpoint := "https://wafopenapi." + region + ".aliyuncs.com/"
	instance, err := aliyunWAFInstance(ctx, cfg)
	if err != nil {
		return err
	}

	detail, err := aliyunRPC(ctx, endpoint, "2021-10-01", "DescribeDomainDetail", cfg.AccessKeyID, cfg.AccessKeySecret, url.Values{
		"InstanceId": {instance},
		"Domain":     {strings.TrimSpace(cfg.Domain)},
		"RegionId":   {region},
	})
	if err != nil {
		return fmt.Errorf("get WAF domain detail: %w", err)
	}

	certName := fmt.Sprintf("kpccssl-%s-%d", strings.ReplaceAll(cfg.Domain, "*", "wildcard"), time.Now().Unix())
	created, err := aliyunRPC(ctx, endpoint, "2021-10-01", "CreateCerts", cfg.AccessKeyID, cfg.AccessKeySecret, url.Values{
		"InstanceId":  {instance},
		"CertName":    {certName},
		"CertContent": {string(b.CertChain())},
		"CertKey":     {string(b.Key)},
	})
	if err != nil {
		return fmt.Errorf("create WAF certificate: %w", err)
	}
	certID := stringValue(created["CertIdentifier"])
	if certID == "" {
		certID = stringValue(created["CertId"])
	}
	if certID == "" {
		return errors.New("Alibaba Cloud WAF did not return a certificate identifier")
	}

	params := url.Values{
		"InstanceId": {instance},
		"Domain":     {strings.TrimSpace(cfg.Domain)},
		"RegionId":   {region},
	}
	// ModifyDomain replaces the whole listen/redirect configuration, so carry
	// over the existing values and only change the certificate.
	allowed := map[string]map[string]bool{
		"Listen":   aliyunWAFListenKeys,
		"Redirect": aliyunWAFRedirectKeys,
	}
	for _, key := range []string{"Listen", "Redirect"} {
		if section, ok := detail[key].(map[string]any); ok {
			filtered := make(map[string]any, len(section))
			for k, v := range section {
				if allowed[key][k] {
					filtered[k] = v
				}
			}
			flattenAliyunParams(key, filtered, params)
		}
	}
	params.Set("Listen.CertId", certID)
	if _, err := aliyunRPC(ctx, endpoint, "2021-10-01", "ModifyDomain", cfg.AccessKeyID, cfg.AccessKeySecret, params); err != nil {
		return fmt.Errorf("update WAF domain certificate: %w", err)
	}
	return nil
}

var aliyunWAFListenKeys = map[string]bool{
	"CipherSuite": true, "CustomCiphers": true, "EnableTLSv3": true,
	"ExclusiveIp": true, "FocusHttps": true, "Http2Enabled": true,
	"IPv6Enabled": true, "ProtectionResource": true, "TLSVersion": true,
	"XffHeaderMode": true, "XffHeaders": true, "HttpPorts": true,
	"HttpsPorts": true,
}

var aliyunWAFRedirectKeys = map[string]bool{
	"ConnectTimeout": true, "FocusHttpBackend": true, "Keepalive": true,
	"KeepaliveRequests": true, "KeepaliveTimeout": true, "Loadbalance": true,
	"ReadTimeout": true, "Retry": true, "SniEnabled": true, "SniHost": true,
	"WriteTimeout": true, "XffProto": true, "Backends": true,
	"BackupBackends": true, "RequestHeaders": true,
}

func flattenAliyunParams(prefix string, value any, out url.Values) {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			flattenAliyunParams(prefix+"."+k, child, out)
		}
	case []any:
		for i, child := range v {
			flattenAliyunParams(prefix+"."+strconv.Itoa(i+1), child, out)
		}
	case nil:
		return
	case string:
		if v != "" {
			out.Set(prefix, v)
		}
	case bool:
		out.Set(prefix, strconv.FormatBool(v))
	case float64:
		out.Set(prefix, strconv.FormatFloat(v, 'f', -1, 64))
	default:
		out.Set(prefix, fmt.Sprint(v))
	}
}
