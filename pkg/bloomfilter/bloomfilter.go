package bloomfilter

import (
	"context"
	"hash/fnv"

	goredis "github.com/redis/go-redis/v9"
)

const hashCount = 14

type Filter struct {
	client RedisClient
	key    string
	bits   uint64
}

type RedisClient interface {
	SetBit(ctx context.Context, key string, offset int64, value int) *goredis.IntCmd
	GetBit(ctx context.Context, key string, offset int64) *goredis.IntCmd
}

func New(client RedisClient, key string, bits uint64) *Filter {
	if bits == 0 {
		bits = 20_000_000
	}

	return &Filter{
		client: client,
		key:    key,
		bits:   bits,
	}
}

func (f *Filter) Add(ctx context.Context, data string) error {
	for _, offset := range f.offsets(data) {
		if err := f.client.SetBit(ctx, f.key, int64(offset), 1).Err(); err != nil {
			return err
		}
	}

	return nil
}

func (f *Filter) Exists(ctx context.Context, data string) (bool, error) {
	for _, offset := range f.offsets(data) {
		exists, err := f.client.GetBit(ctx, f.key, int64(offset)).Result()
		if err != nil {
			return false, err
		}
		if exists == 0 {
			return false, nil
		}
	}

	return true, nil
}

func (f *Filter) offsets(data string) []uint64 {
	offsets := make([]uint64, 0, hashCount)
	for i := byte(0); i < hashCount; i++ {
		h := fnv.New64a()
		_, _ = h.Write([]byte{byte(i)})
		_, _ = h.Write([]byte(data))
		offsets = append(offsets, h.Sum64()%f.bits)
	}

	return offsets
}
