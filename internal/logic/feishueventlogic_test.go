package logic

import (
	"context"
	"testing"

	"shorturl/internal/config"
	"shorturl/internal/svc"
)

func TestFeishuEventURLVerification(t *testing.T) {
	ctx := &svc.ServiceContext{
		Config: config.Config{},
	}
	ctx.Config.Feishu.VerificationToken = "verify-token"

	body := []byte(`{"type":"url_verification","challenge":"challenge-code","token":"verify-token"}`)
	resp, err := NewFeishuEventLogic(context.Background(), ctx).Handle(body)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := resp["challenge"]; got != "challenge-code" {
		t.Fatalf("challenge = %v, want challenge-code", got)
	}
}

func TestFeishuEventURLVerificationRejectsInvalidToken(t *testing.T) {
	ctx := &svc.ServiceContext{
		Config: config.Config{},
	}
	ctx.Config.Feishu.VerificationToken = "verify-token"

	body := []byte(`{"type":"url_verification","challenge":"challenge-code","token":"bad-token"}`)
	_, err := NewFeishuEventLogic(context.Background(), ctx).Handle(body)
	if err == nil {
		t.Fatal("Handle() error = nil, want invalid token error")
	}
}

func TestExtractLongURLsSkipsGeneratedShortDomain(t *testing.T) {
	text := "请转 https://example.com/a?x=1，已短链 https://qimi.cn/abc，还有 https://example.com/a?x=1"

	urls := extractLongURLs(text, "qimi.cn/")
	if len(urls) != 1 {
		t.Fatalf("len(urls) = %d, want 1: %#v", len(urls), urls)
	}
	if urls[0] != "https://example.com/a?x=1" {
		t.Fatalf("urls[0] = %q", urls[0])
	}
}

func TestParseFeishuText(t *testing.T) {
	text, err := parseFeishuText(`{"text":"帮我转 https://example.com"}`)
	if err != nil {
		t.Fatalf("parseFeishuText() error = %v", err)
	}
	if text != "帮我转 https://example.com" {
		t.Fatalf("text = %q", text)
	}
}
