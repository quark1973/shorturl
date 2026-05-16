// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package logic

import (
	"context"
	"database/sql"
	"errors"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"shorturl/internal/svc"
	"shorturl/internal/types"
	"shorturl/pkg/shortcache"
)

var errShortURLNotFound = errors.New("短链接不存在")

type ShowLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewShowLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ShowLogic {
	return &ShowLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ShowLogic) Show(req *types.ShowRequest) (*types.ShowResponse, error) {
	shortCode := req.ShortUrl

	// 1. Bloom 过滤器挡掉一定不存在的短码，Redis 异常或未初始化完成时跳过 Bloom。
	if l.svcCtx.BloomReady {
		exists, err := l.svcCtx.BloomFilter.Exists(l.ctx, shortCode)
		if err != nil {
			logx.Errorw("BloomFilter.Exists failed", logx.LogField{Key: "err", Value: err.Error()})
		} else if !exists {
			return nil, errShortURLNotFound
		}
	}

	// 2. 先查 Redis，命中正常值直接返回，命中空值直接返回不存在。
	longURL, ok, err := l.getCachedLongURL(shortCode)
	if err != nil {
		return nil, err
	}
	if ok {
		return &types.ShowResponse{LongUrl: longURL}, nil
	}

	// 3. Redis 未命中，用 singleflight 合并同一个短码的并发回源请求。
	value, err, _ := l.svcCtx.ShortUrlGroup.Do(shortCode, func() (interface{}, error) {
		longURL, ok, err := l.getCachedLongURL(shortCode)
		if err != nil {
			return "", err
		}
		if ok {
			return longURL, nil
		}

		row, err := l.svcCtx.ShortUrlModel.FindOneBySurl(l.ctx, sql.NullString{
			String: shortCode,
			Valid:  true,
		})
		if err != nil {
			if err == sqlx.ErrNotFound {
				l.cacheNullShortURL(shortCode)
				return "", errShortURLNotFound
			}

			logx.Errorw("ShortUrlModel.FindOneBySurl failed", logx.LogField{Key: "err", Value: err.Error()})
			return "", err
		}

		dbLongURL := row.Lurl.String
		l.cacheShortURL(shortCode, dbLongURL)
		return dbLongURL, nil
	})
	if err != nil {
		return nil, err
	}

	return &types.ShowResponse{LongUrl: value.(string)}, nil
}

func (l *ShowLogic) getCachedLongURL(shortCode string) (string, bool, error) {
	value, err := l.svcCtx.RedisClient.Get(l.ctx, shortcache.MappingKey(shortCode)).Result()
	if err == nil {
		if value == shortcache.NullValue {
			return "", true, errShortURLNotFound
		}

		return value, true, nil
	}
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}

	logx.Errorw("Redis Get failed", logx.LogField{Key: "err", Value: err.Error()})
	return "", false, nil
}

func (l *ShowLogic) cacheShortURL(shortCode, longURL string) {
	if shortCode == "" || longURL == "" {
		return
	}

	err := l.svcCtx.RedisClient.Set(
		l.ctx,
		shortcache.MappingKey(shortCode),
		longURL,
		shortcache.MappingTTL(l.svcCtx.Config.ShortUrlCacheTTL),
	).Err()
	if err != nil {
		logx.Errorw("Redis Set failed", logx.LogField{Key: "err", Value: err.Error()})
	}

	if err := l.svcCtx.BloomFilter.Add(l.ctx, shortCode); err != nil {
		logx.Errorw("BloomFilter.Add failed", logx.LogField{Key: "err", Value: err.Error()})
	}
}

func (l *ShowLogic) cacheNullShortURL(shortCode string) {
	err := l.svcCtx.RedisClient.Set(
		l.ctx,
		shortcache.MappingKey(shortCode),
		shortcache.NullValue,
		shortcache.NullTTL(),
	).Err()
	if err != nil {
		logx.Errorw("Redis Set null value failed", logx.LogField{Key: "err", Value: err.Error()})
	}
}
