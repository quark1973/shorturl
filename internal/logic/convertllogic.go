// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package logic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"shorturl/internal/svc"
	"shorturl/internal/types"
	"shorturl/model"
	"shorturl/pkg/base62"
	"shorturl/pkg/connect"
	"shorturl/pkg/md5"
	"shorturl/pkg/shortcache"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type COnvertlLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCOnvertlLogic(ctx context.Context, svcCtx *svc.ServiceContext) *COnvertlLogic {
	return &COnvertlLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *COnvertlLogic) COnvertl(req *types.ConvertRequest) (*types.ConvertResponse, error) {
	// 1. 校验输入
	// 1.1 数据不能为空：由 handler 层 validator 根据 api 中的 validate tag 处理
	// 1.2 输入网址必须可访问
	if ok := connect.Get(req.LongUrl); !ok {
		return nil, errors.New("无效链接")
	}

	// 1.3 判断该长链接之前是否已经转链
	// 1.3.1 给长链接生成 md5
	md5Value := md5.Sum([]byte(req.LongUrl))
	// 1.3.2 拿 md5 去数据库查询是否已存在，保证同一个长链接返回同一个短链
	existed, err := l.svcCtx.ShortUrlModel.FindOneByMd5(l.ctx, sql.NullString{
		String: md5Value,
		Valid:  true,
	})
	if err == nil {
		l.cacheShortURL(existed.Surl.String, req.LongUrl)
		return &types.ConvertResponse{ShortUrl: l.buildShortURL(existed.Surl.String)}, nil
	}
	if err != sqlx.ErrNotFound {
		logx.Errorw("ShortUrlModel.FindOneByMd5 failed", logx.LogField{Key: "err", Value: err.Error()})
		return nil, err
	}

	// 1.4 输入不能是已经生成过的短链接，避免循环转链
	if err := l.checkNotShortUrl(req.LongUrl); err != nil {
		return nil, err
	}

	// 2. 取号：基于美团 Leaf 号段模式实现的发号器
	//    内存里缓存一段可用 ID，用完后再通过 sequence 表申请下一段，减少 DB 压力
	seq, err := l.svcCtx.IDGenerator.Next(l.ctx)
	if err != nil {
		logx.Errorw("IDGenerator.Next failed", logx.LogField{Key: "err", Value: err.Error()})
		return nil, err
	}

	// 3. 号码转短链码：将递增 ID 转为 base62，得到更短的 URL code
	shortCode := base62.Encode(seq)

	// 4. 存储长短链映射关系
	_, err = l.svcCtx.ShortUrlModel.Insert(l.ctx, &model.ShortUrlMap{
		CreateBy: "system",
		IsDel:    0,
		Lurl:     sql.NullString{String: req.LongUrl, Valid: true},
		Md5:      sql.NullString{String: md5Value, Valid: true},
		Surl:     sql.NullString{String: shortCode, Valid: true},
	})
	if err != nil {
		logx.Errorw("ShortUrlModel.Insert failed", logx.LogField{Key: "err", Value: err.Error()})
		return nil, err
	}

	// 4.1 写 Redis 缓存和 Bloom，失败只记录日志，不影响主流程
	l.cacheShortURL(shortCode, req.LongUrl)

	// 5. 返回响应
	// 5.1 返回短域名加短链码
	return &types.ConvertResponse{ShortUrl: l.buildShortURL(shortCode)}, nil
}

func (l *COnvertlLogic) cacheShortURL(shortCode, longURL string) {
	if shortCode == "" || longURL == "" {
		return
	}

	key := shortcache.MappingKey(shortCode)
	err := l.svcCtx.RedisClient.Set(l.ctx, key, longURL, shortcache.MappingTTL(l.svcCtx.Config.ShortUrlCacheTTL)).Err()
	if err != nil {
		logx.Errorw("Redis Set failed", logx.LogField{Key: "err", Value: err.Error()})
	}

	if err := l.svcCtx.BloomFilter.Add(l.ctx, shortCode); err != nil {
		logx.Errorw("BloomFilter.Add failed", logx.LogField{Key: "err", Value: err.Error()})
	}
}

func (l *COnvertlLogic) buildShortURL(shortCode string) string {
	shortDomain := strings.TrimRight(l.svcCtx.Config.ShortDomain, "/")
	if shortDomain == "" {
		return shortCode
	}

	return shortDomain + "/" + shortCode
}

func (l *COnvertlLogic) checkNotShortUrl(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		logx.Errorw("url.Parse failed", logx.LogField{Key: "err", Value: err.Error()})
		return err
	}

	shortCode := path.Base(parsedURL.Path)
	if shortCode == "." || shortCode == "/" || shortCode == "" {
		return nil
	}

	_, err = l.svcCtx.ShortUrlModel.FindOneBySurl(l.ctx, sql.NullString{
		String: shortCode,
		Valid:  true,
	})
	if err == nil {
		return fmt.Errorf("input url is already a short url: %s", shortCode)
	}
	if err != sqlx.ErrNotFound {
		logx.Errorw("ShortUrlModel.FindOneBySurl failed", logx.LogField{Key: "err", Value: err.Error()})
		return err
	}

	return nil
}
