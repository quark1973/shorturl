// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package svc

import (
	"context"
	"time"

	"shorturl/internal/config"
	"shorturl/model"
	"shorturl/pkg/bloomfilter"
	"shorturl/pkg/feishu"
	"shorturl/pkg/leaf"
	"shorturl/pkg/shortcache"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/sync/singleflight"
)

type RedisClient interface {
	Get(ctx context.Context, key string) *goredis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *goredis.StatusCmd
	SetBit(ctx context.Context, key string, offset int64, value int) *goredis.IntCmd
	GetBit(ctx context.Context, key string, offset int64) *goredis.IntCmd
}

type FeishuClient interface {
	ReplyText(ctx context.Context, messageID, text string) error
}

type ServiceContext struct {
	Config        config.Config
	ShortUrlModel model.ShortUrlMapModel
	IDGenerator   *leaf.SegmentGenerator
	RedisClient   RedisClient
	ShortUrlGroup singleflight.Group
	BloomFilter   *bloomfilter.Filter
	BloomReady    bool
	FeishuClient  FeishuClient
}

func NewServiceContext(c config.Config) *ServiceContext {
	conn := sqlx.NewMysql(c.ShortUrlDB.DSN)
	sequenceDSN := c.Sequence.DSN
	if sequenceDSN == "" {
		sequenceDSN = c.ShortUrlDB.DSN
	}
	sequenceConn := sqlx.NewMysql(sequenceDSN)
	sequenceModel := model.NewSequenceModel(sequenceConn)
	addrs := c.Redis.Addrs
	redisClient := goredis.NewUniversalClient(&goredis.UniversalOptions{
		Addrs:         addrs,
		Password:      c.Redis.Password,
		DB:            c.Redis.DB,
		IsClusterMode: c.Redis.Cluster,
	})
	shortUrlModel := model.NewShortUrlMapModel(conn)
	filter := bloomfilter.New(redisClient, shortcache.BloomKey, c.ShortUrlBloomBits)
	bloomReady := warmBloomFilter(context.Background(), shortUrlModel, filter)
	feishuClient := feishu.NewClient(feishu.Config{
		AppID:     c.Feishu.AppID,
		AppSecret: c.Feishu.AppSecret,
		APIBase:   c.Feishu.APIBase,
	})

	return &ServiceContext{
		Config:        c,
		ShortUrlModel: shortUrlModel,
		IDGenerator:   leaf.NewSegmentGenerator(sequenceModel, c.Sequence.BizTag, c.Sequence.Step),
		RedisClient:   redisClient,
		BloomFilter:   filter,
		BloomReady:    bloomReady,
		FeishuClient:  feishuClient,
	}
}

func warmBloomFilter(ctx context.Context, shortUrlModel model.ShortUrlMapModel, filter *bloomfilter.Filter) bool {
	codes, err := shortUrlModel.ListAllSurl(ctx)
	if err != nil {
		logx.Errorw("ShortUrlModel.ListAllSurl failed", logx.LogField{Key: "err", Value: err.Error()})
		return false
	}

	for _, code := range codes {
		if err := filter.Add(ctx, code); err != nil {
			logx.Errorw("BloomFilter.Add failed", logx.LogField{Key: "err", Value: err.Error()})
			return false
		}
	}

	return true
}
