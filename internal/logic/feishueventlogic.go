package logic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"shorturl/internal/svc"
	"shorturl/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

const (
	feishuReceiveMessageEvent = "im.message.receive_v1"
	feishuAttrKeyPrefix       = "shorturl:feishu:attr:"
)

var urlRegexp = regexp.MustCompile(`https?://[^\s<>"'，。；、）)]+`)

type FeishuEventLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

type feishuEventCallback struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Header    struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
		TenantKey string `json:"tenant_key"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
			} `json:"sender_id"`
			TenantKey string `json:"tenant_key"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
	} `json:"event"`
}

type feishuMessageContent struct {
	Text string `json:"text"`
}

type feishuURLAttribution struct {
	TenantKey string `json:"tenantKey"`
	ChatID    string `json:"chatId"`
	ChatType  string `json:"chatType"`
	SenderID  string `json:"senderId"`
	MessageID string `json:"messageId"`
	LongURL   string `json:"longUrl"`
	ShortURL  string `json:"shortUrl"`
	CreatedAt int64  `json:"createdAt"`
}

func NewFeishuEventLogic(ctx context.Context, svcCtx *svc.ServiceContext) *FeishuEventLogic {
	return &FeishuEventLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *FeishuEventLogic) Handle(body []byte) (map[string]any, error) {
	var callback feishuEventCallback
	if err := json.Unmarshal(body, &callback); err != nil {
		return nil, err
	}

	if callback.Challenge != "" || callback.Type == "url_verification" {
		if err := l.verifyToken(callback); err != nil {
			return nil, err
		}
		return map[string]any{"challenge": callback.Challenge}, nil
	}

	if err := l.verifyToken(callback); err != nil {
		return nil, err
	}

	if callback.Header.EventType != feishuReceiveMessageEvent {
		return okFeishuEvent(), nil
	}

	if callback.Event.Message.MessageType != "text" {
		return okFeishuEvent(), nil
	}

	text, err := parseFeishuText(callback.Event.Message.Content)
	if err != nil {
		logx.Errorw("parse feishu message content failed", logx.LogField{Key: "err", Value: err.Error()})
		return okFeishuEvent(), nil
	}

	longURLs := extractLongURLs(text, l.svcCtx.Config.ShortDomain)
	if len(longURLs) == 0 {
		return okFeishuEvent(), nil
	}

	reply := l.convertURLsAndBuildReply(callback, longURLs)
	if strings.TrimSpace(reply) == "" || l.svcCtx.FeishuClient == nil {
		return okFeishuEvent(), nil
	}

	if err := l.svcCtx.FeishuClient.ReplyText(l.ctx, callback.Event.Message.MessageID, reply); err != nil {
		logx.Errorw("FeishuClient.ReplyText failed", logx.LogField{Key: "err", Value: err.Error()})
	}

	return okFeishuEvent(), nil
}

func (l *FeishuEventLogic) verifyToken(callback feishuEventCallback) error {
	expected := l.svcCtx.Config.Feishu.VerificationToken
	if expected == "" {
		return nil
	}

	actual := callback.Header.Token
	if actual == "" {
		actual = callback.Token
	}
	if actual != expected {
		return errors.New("invalid feishu verification token")
	}

	return nil
}

func (l *FeishuEventLogic) convertURLsAndBuildReply(callback feishuEventCallback, longURLs []string) string {
	var successLines []string
	var failedLines []string

	convertLogic := NewCOnvertlLogic(l.ctx, l.svcCtx)
	for _, longURL := range longURLs {
		resp, err := convertLogic.COnvertl(&types.ConvertRequest{LongUrl: longURL})
		if err != nil {
			logx.Errorw(
				"convert feishu url failed",
				logx.LogField{Key: "longUrl", Value: longURL},
				logx.LogField{Key: "err", Value: err.Error()},
			)
			failedLines = append(failedLines, fmt.Sprintf("%s -> 生成失败：%s", longURL, err.Error()))
			continue
		}

		successLines = append(successLines, fmt.Sprintf("%s -> %s", longURL, resp.ShortUrl))
		l.cacheFeishuAttribution(callback, longURL, resp.ShortUrl)
	}

	if len(successLines) > 0 {
		reply := "已生成短链接：\n" + strings.Join(successLines, "\n")
		if len(failedLines) > 0 {
			reply += "\n\n以下链接未生成：\n" + strings.Join(failedLines, "\n")
		}
		return reply
	}

	return "未能生成短链接：\n" + strings.Join(failedLines, "\n")
}

func (l *FeishuEventLogic) cacheFeishuAttribution(callback feishuEventCallback, longURL, shortURL string) {
	shortCode := shortCodeFromShortURL(shortURL)
	if shortCode == "" {
		return
	}

	attr := feishuURLAttribution{
		TenantKey: firstNonEmpty(callback.Header.TenantKey, callback.Event.Sender.TenantKey),
		ChatID:    callback.Event.Message.ChatID,
		ChatType:  callback.Event.Message.ChatType,
		SenderID:  firstNonEmpty(callback.Event.Sender.SenderID.OpenID, callback.Event.Sender.SenderID.UserID, callback.Event.Sender.SenderID.UnionID),
		MessageID: callback.Event.Message.MessageID,
		LongURL:   longURL,
		ShortURL:  shortURL,
		CreatedAt: time.Now().Unix(),
	}

	payload, err := json.Marshal(attr)
	if err != nil {
		logx.Errorw("marshal feishu attribution failed", logx.LogField{Key: "err", Value: err.Error()})
		return
	}

	err = l.svcCtx.RedisClient.Set(l.ctx, feishuAttrKeyPrefix+shortCode, string(payload), 30*24*time.Hour).Err()
	if err != nil {
		logx.Errorw("cache feishu attribution failed", logx.LogField{Key: "err", Value: err.Error()})
	}
}

func okFeishuEvent() map[string]any {
	return map[string]any{"code": 0, "msg": "ok"}
}

func parseFeishuText(content string) (string, error) {
	var msg feishuMessageContent
	if err := json.Unmarshal([]byte(content), &msg); err != nil {
		return "", err
	}
	return msg.Text, nil
}

func extractLongURLs(text, shortDomain string) []string {
	matches := urlRegexp.FindAllString(text, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, rawURL := range matches {
		candidate := strings.TrimRight(rawURL, ".,;:!?")
		if candidate == "" || isShortDomainURL(candidate, shortDomain) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func isShortDomainURL(rawURL, shortDomain string) bool {
	if shortDomain == "" {
		return false
	}

	domain := strings.TrimSpace(shortDomain)
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimRight(domain, "/")
	if domain == "" {
		return false
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, domain)
}

func shortCodeFromShortURL(shortURL string) string {
	trimmed := strings.TrimRight(shortURL, "/")
	if trimmed == "" {
		return ""
	}
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return trimmed
	}
	return trimmed[idx+1:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
