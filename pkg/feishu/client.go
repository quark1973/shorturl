package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultAPIBase = "https://open.feishu.cn/open-apis"

type Config struct {
	AppID     string
	AppSecret string
	APIBase   string
}

type Client struct {
	cfg        Config
	httpClient *http.Client

	mu          sync.Mutex
	tenantToken string
	tokenExpire time.Time
}

func NewClient(cfg Config) *Client {
	if cfg.APIBase == "" {
		cfg.APIBase = defaultAPIBase
	}

	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) ReplyText(ctx context.Context, messageID, text string) error {
	if messageID == "" {
		return fmt.Errorf("message id is empty")
	}

	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}

	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	body := map[string]string{
		"msg_type": "text",
		"content":  string(content),
	}

	endpoint := fmt.Sprintf("%s/im/v1/messages/%s/reply", strings.TrimRight(c.cfg.APIBase, "/"), messageID)
	return c.post(ctx, endpoint, token, body, nil)
}

func (c *Client) tenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.tenantToken != "" && time.Now().Before(c.tokenExpire) {
		token := c.tenantToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	if c.cfg.AppID == "" || c.cfg.AppSecret == "" {
		return "", fmt.Errorf("feishu app id or app secret is empty")
	}

	var resp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	endpoint := strings.TrimRight(c.cfg.APIBase, "/") + "/auth/v3/tenant_access_token/internal"
	err := c.post(ctx, endpoint, "", map[string]string{
		"app_id":     c.cfg.AppID,
		"app_secret": c.cfg.AppSecret,
	}, &resp)
	if err != nil {
		return "", err
	}
	if resp.TenantAccessToken == "" {
		return "", fmt.Errorf("empty tenant access token")
	}

	expire := resp.Expire
	if expire <= 300 {
		expire = 7200
	}

	c.mu.Lock()
	c.tenantToken = resp.TenantAccessToken
	c.tokenExpire = time.Now().Add(time.Duration(expire-300) * time.Second)
	c.mu.Unlock()

	return resp.TenantAccessToken, nil
}

func (c *Client) post(ctx context.Context, endpoint, token string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("feishu api status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var base struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBody, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		return fmt.Errorf("feishu api code=%d msg=%s", base.Code, base.Msg)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return err
		}
	}

	return nil
}
