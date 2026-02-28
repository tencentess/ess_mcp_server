package signer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TC3SignRequest 腾讯云 TC3-HMAC-SHA256 签名参数
type TC3SignRequest struct {
	SecretId  string
	SecretKey string
	Service   string // 如 "ess"
	Host      string // 如 "ess.tencentcloudapi.com"
	Action    string // 如 "CreateFlowEvidenceReport"
	Version   string // 如 "2020-11-11"
	Region    string // 可选
	Payload   string // JSON body
	Timestamp int64
}

// SignAndBuildRequest 签名并构建 HTTP 请求
func SignAndBuildRequest(params TC3SignRequest) (*http.Request, error) {
	if params.Timestamp == 0 {
		params.Timestamp = time.Now().Unix()
	}

	// 日期
	date := time.Unix(params.Timestamp, 0).UTC().Format("2006-01-02")
	timestampStr := fmt.Sprintf("%d", params.Timestamp)

	// Step 1: 拼接规范请求串
	httpRequestMethod := "POST"
	canonicalURI := "/"
	canonicalQueryString := ""
	contentType := "application/json; charset=utf-8"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		contentType, params.Host, strings.ToLower(params.Action))
	signedHeaders := "content-type;host;x-tc-action"

	hashedPayload := sha256Hex(params.Payload)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		httpRequestMethod, canonicalURI, canonicalQueryString,
		canonicalHeaders, signedHeaders, hashedPayload)

	// Step 2: 拼接待签名字符串
	algorithm := "TC3-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/tc3_request", date, params.Service)
	hashedCanonicalRequest := sha256Hex(canonicalRequest)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		algorithm, timestampStr, credentialScope, hashedCanonicalRequest)

	// Step 3: 计算签名
	secretDate := hmacSHA256([]byte("TC3"+params.SecretKey), date)
	secretService := hmacSHA256(secretDate, params.Service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	// Step 4: 拼接 Authorization
	authorization := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, params.SecretId, credentialScope, signedHeaders, signature)

	// 构建 HTTP 请求
	url := fmt.Sprintf("https://%s/", params.Host)
	req, err := http.NewRequest("POST", url, strings.NewReader(params.Payload))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", params.Host)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("X-TC-Action", params.Action)
	req.Header.Set("X-TC-Version", params.Version)
	req.Header.Set("X-TC-Timestamp", timestampStr)
	if params.Region != "" {
		req.Header.Set("X-TC-Region", params.Region)
	}

	return req, nil
}

func sha256Hex(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}
