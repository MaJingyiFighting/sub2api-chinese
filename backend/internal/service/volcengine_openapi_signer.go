package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const volcengineTimeFormat = "20060102T150405Z"

func signVolcengineOpenAPI(req *http.Request, body []byte, ak, sk, region, service string, now time.Time) error {
	if req == nil {
		return fmt.Errorf("volcengine request is nil")
	}
	ak = strings.TrimSpace(ak)
	sk = strings.TrimSpace(sk)
	region = strings.TrimSpace(region)
	service = strings.TrimSpace(service)
	if ak == "" || sk == "" {
		return fmt.Errorf("volcengine quota probe requires Access Key / Secret Key")
	}
	if region == "" {
		region = "cn-beijing"
	}
	if service == "" {
		service = "ark"
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	bodyHash := sha256Hex(body)
	xDate := now.Format(volcengineTimeFormat)
	shortDate := xDate[:8]

	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", bodyHash)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	signedHeaders := "host;x-content-sha256;x-date"
	canonicalHeaders := "host:" + strings.ToLower(strings.TrimSpace(req.Host)) + "\n" +
		"x-content-sha256:" + bodyHash + "\n" +
		"x-date:" + xDate + "\n"
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalVolcenginePath(req),
		canonicalVolcengineQuery(req),
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")
	credentialScope := strings.Join([]string{shortDate, region, service, "request"}, "/")
	stringToSign := strings.Join([]string{
		"HMAC-SHA256",
		xDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := volcengineSigningKey(sk, shortDate, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		ak,
		credentialScope,
		signedHeaders,
		signature,
	))
	return nil
}

func canonicalVolcenginePath(req *http.Request) string {
	if req.URL == nil || req.URL.EscapedPath() == "" {
		return "/"
	}
	return req.URL.EscapedPath()
}

func canonicalVolcengineQuery(req *http.Request) string {
	if req.URL == nil || req.URL.RawQuery == "" {
		return ""
	}
	values := req.URL.Query()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(values))
	for _, key := range keys {
		vals := append([]string(nil), values[key]...)
		sort.Strings(vals)
		for _, value := range vals {
			parts = append(parts, volcengineQueryEscape(key)+"="+volcengineQueryEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

func volcengineQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func volcengineSigningKey(secretKey, shortDate, region, service string) []byte {
	kDate := hmacSHA256([]byte(secretKey), []byte(shortDate))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
