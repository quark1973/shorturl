package logic

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"shorturl/internal/config"
	"shorturl/internal/svc"
	"shorturl/internal/types"
	"shorturl/model"
	"shorturl/pkg/bloomfilter"
	"shorturl/pkg/leaf"
	"shorturl/pkg/md5"
	"shorturl/pkg/shortcache"
)

type fakeRedisClient struct {
	mu      sync.Mutex
	values  map[string]string
	bits    map[string]map[int64]int
	failGet bool
	failSet bool
}

func newFakeRedisClient() *fakeRedisClient {
	return &fakeRedisClient{
		values: make(map[string]string),
		bits:   make(map[string]map[int64]int),
	}
}

func (r *fakeRedisClient) Get(_ context.Context, key string) *goredis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failGet {
		return goredis.NewStringResult("", errors.New("redis down"))
	}
	value, ok := r.values[key]
	if !ok {
		return goredis.NewStringResult("", goredis.Nil)
	}

	return goredis.NewStringResult(value, nil)
}

func (r *fakeRedisClient) Set(_ context.Context, key string, value interface{}, _ time.Duration) *goredis.StatusCmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failSet {
		return goredis.NewStatusResult("", errors.New("redis down"))
	}
	if text, ok := value.(string); ok {
		r.values[key] = text
	}

	return goredis.NewStatusResult("OK", nil)
}

func (r *fakeRedisClient) SetBit(_ context.Context, key string, offset int64, value int) *goredis.IntCmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failSet {
		return goredis.NewIntResult(0, errors.New("redis down"))
	}
	if _, ok := r.bits[key]; !ok {
		r.bits[key] = make(map[int64]int)
	}
	r.bits[key][offset] = value
	return goredis.NewIntResult(int64(value), nil)
}

func (r *fakeRedisClient) GetBit(_ context.Context, key string, offset int64) *goredis.IntCmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failGet {
		return goredis.NewIntResult(0, errors.New("redis down"))
	}
	return goredis.NewIntResult(int64(r.bits[key][offset]), nil)
}

type fakeShortUrlModel struct {
	mu              sync.Mutex
	bySurl          map[string]*model.ShortUrlMap
	byMd5           map[string]*model.ShortUrlMap
	findBySurlCalls int
	insertCalls     int
	delay           time.Duration
}

func newFakeShortUrlModel() *fakeShortUrlModel {
	return &fakeShortUrlModel{
		bySurl: make(map[string]*model.ShortUrlMap),
		byMd5:  make(map[string]*model.ShortUrlMap),
	}
}

func (m *fakeShortUrlModel) Insert(_ context.Context, data *model.ShortUrlMap) (sql.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.insertCalls++
	m.bySurl[data.Surl.String] = data
	m.byMd5[data.Md5.String] = data
	return fakeResult(1), nil
}

func (m *fakeShortUrlModel) FindOne(_ context.Context, id uint64) (*model.ShortUrlMap, error) {
	return nil, model.ErrNotFound
}

func (m *fakeShortUrlModel) FindOneByMd5(_ context.Context, md5 sql.NullString) (*model.ShortUrlMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	row, ok := m.byMd5[md5.String]
	if !ok {
		return nil, model.ErrNotFound
	}

	return row, nil
}

func (m *fakeShortUrlModel) FindOneBySurl(_ context.Context, surl sql.NullString) (*model.ShortUrlMap, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.findBySurlCalls++
	row, ok := m.bySurl[surl.String]
	if !ok {
		return nil, model.ErrNotFound
	}

	return row, nil
}

func (m *fakeShortUrlModel) Update(context.Context, *model.ShortUrlMap) error {
	return nil
}

func (m *fakeShortUrlModel) Delete(context.Context, uint64) error {
	return nil
}

func (m *fakeShortUrlModel) ListAllSurl(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	codes := make([]string, 0, len(m.bySurl))
	for code := range m.bySurl {
		codes = append(codes, code)
	}

	return codes, nil
}

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) {
	return int64(r), nil
}

func (r fakeResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

type fakeSequenceModel struct {
	mu  sync.Mutex
	max uint64
}

func (m *fakeSequenceModel) AllocSegment(_ context.Context, _ string, step uint64) (uint64, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if step == 0 {
		step = 100
	}
	start := m.max + 1
	m.max += step
	return start, m.max, nil
}

func (m *fakeSequenceModel) Insert(context.Context, *model.Sequence) (sql.Result, error) {
	return fakeResult(1), nil
}

func (m *fakeSequenceModel) FindOne(context.Context, string) (*model.Sequence, error) {
	return nil, model.ErrNotFound
}

func (m *fakeSequenceModel) Update(context.Context, *model.Sequence) error {
	return nil
}

func (m *fakeSequenceModel) Delete(context.Context, string) error {
	return nil
}

func newTestServiceContext(shortUrlModel *fakeShortUrlModel, redisClient *fakeRedisClient) *svc.ServiceContext {
	c := config.Config{
		ShortDomain:       "qimi.cn/",
		ShortUrlCacheTTL:  86400,
		ShortUrlBloomBits: 100_000,
	}
	filter := bloomfilter.New(redisClient, shortcache.BloomKey, c.ShortUrlBloomBits)

	return &svc.ServiceContext{
		Config:        c,
		ShortUrlModel: shortUrlModel,
		IDGenerator:   leaf.NewSegmentGenerator(&fakeSequenceModel{}, "short_url", 100),
		RedisClient:   redisClient,
		BloomFilter:   filter,
	}
}

func TestConvertCachesShortURLAndAddsBloom(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	shortUrlModel := newFakeShortUrlModel()
	redisClient := newFakeRedisClient()
	ctx := newTestServiceContext(shortUrlModel, redisClient)

	resp, err := NewCOnvertlLogic(context.Background(), ctx).COnvertl(&types.ConvertRequest{
		LongUrl: target.URL,
	})
	if err != nil {
		t.Fatalf("COnvertl() error = %v", err)
	}
	if resp.ShortUrl != "qimi.cn/1" {
		t.Fatalf("short url = %q, want %q", resp.ShortUrl, "qimi.cn/1")
	}

	redisClient.mu.Lock()
	cached := redisClient.values[shortcache.MappingKey("1")]
	redisClient.mu.Unlock()
	if cached != target.URL {
		t.Fatalf("cached long url = %q, want %q", cached, target.URL)
	}

	exists, err := ctx.BloomFilter.Exists(context.Background(), "1")
	if err != nil {
		t.Fatalf("BloomFilter.Exists() error = %v", err)
	}
	if !exists {
		t.Fatal("short code should be added to bloom filter")
	}
}

func TestShowUsesRedisCache(t *testing.T) {
	shortUrlModel := newFakeShortUrlModel()
	redisClient := newFakeRedisClient()
	redisClient.values[shortcache.MappingKey("abc")] = "https://example.com"
	ctx := newTestServiceContext(shortUrlModel, redisClient)

	resp, err := NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "abc"})
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if resp.LongUrl != "https://example.com" {
		t.Fatalf("long url = %q", resp.LongUrl)
	}
	if shortUrlModel.findBySurlCalls != 0 {
		t.Fatalf("db calls = %d, want 0", shortUrlModel.findBySurlCalls)
	}
}

func TestShowFallsBackToMysqlWhenRedisDown(t *testing.T) {
	shortUrlModel := newFakeShortUrlModel()
	shortUrlModel.bySurl["abc"] = &model.ShortUrlMap{
		Lurl: sql.NullString{String: "https://example.com", Valid: true},
		Surl: sql.NullString{String: "abc", Valid: true},
	}
	redisClient := newFakeRedisClient()
	redisClient.failGet = true
	redisClient.failSet = true
	ctx := newTestServiceContext(shortUrlModel, redisClient)

	resp, err := NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "abc"})
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if resp.LongUrl != "https://example.com" {
		t.Fatalf("long url = %q", resp.LongUrl)
	}
	if shortUrlModel.findBySurlCalls != 1 {
		t.Fatalf("db calls = %d, want 1", shortUrlModel.findBySurlCalls)
	}
}

func TestShowCachesNullValueForMissingShortURL(t *testing.T) {
	shortUrlModel := newFakeShortUrlModel()
	redisClient := newFakeRedisClient()
	ctx := newTestServiceContext(shortUrlModel, redisClient)

	_, err := NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "missing"})
	if !errors.Is(err, errShortURLNotFound) {
		t.Fatalf("first Show() error = %v, want %v", err, errShortURLNotFound)
	}
	_, err = NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "missing"})
	if !errors.Is(err, errShortURLNotFound) {
		t.Fatalf("second Show() error = %v, want %v", err, errShortURLNotFound)
	}
	if shortUrlModel.findBySurlCalls != 1 {
		t.Fatalf("db calls = %d, want 1 because second request should hit null cache", shortUrlModel.findBySurlCalls)
	}
}

func TestShowSingleflightMergesConcurrentMisses(t *testing.T) {
	shortUrlModel := newFakeShortUrlModel()
	shortUrlModel.delay = 50 * time.Millisecond
	shortUrlModel.bySurl["abc"] = &model.ShortUrlMap{
		Lurl: sql.NullString{String: "https://example.com", Valid: true},
		Surl: sql.NullString{String: "abc", Valid: true},
	}
	redisClient := newFakeRedisClient()
	ctx := newTestServiceContext(shortUrlModel, redisClient)

	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "abc"})
			if err != nil {
				errs <- err
				return
			}
			if resp.LongUrl != "https://example.com" {
				errs <- errors.New("unexpected long url")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if shortUrlModel.findBySurlCalls != 1 {
		t.Fatalf("db calls = %d, want 1", shortUrlModel.findBySurlCalls)
	}
}

func TestShowBloomRejectsDefinitelyMissingShortURL(t *testing.T) {
	shortUrlModel := newFakeShortUrlModel()
	redisClient := newFakeRedisClient()
	ctx := newTestServiceContext(shortUrlModel, redisClient)
	ctx.BloomReady = true

	_, err := NewShowLogic(context.Background(), ctx).Show(&types.ShowRequest{ShortUrl: "missing"})
	if !errors.Is(err, errShortURLNotFound) {
		t.Fatalf("Show() error = %v, want %v", err, errShortURLNotFound)
	}
	if shortUrlModel.findBySurlCalls != 0 {
		t.Fatalf("db calls = %d, want 0", shortUrlModel.findBySurlCalls)
	}
}

func TestRepeatedConvertReturnsSameShortURL(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	shortUrlModel := newFakeShortUrlModel()
	redisClient := newFakeRedisClient()
	ctx := newTestServiceContext(shortUrlModel, redisClient)
	logic := NewCOnvertlLogic(context.Background(), ctx)

	first, err := logic.COnvertl(&types.ConvertRequest{LongUrl: target.URL})
	if err != nil {
		t.Fatalf("first COnvertl() error = %v", err)
	}
	second, err := logic.COnvertl(&types.ConvertRequest{LongUrl: target.URL})
	if err != nil {
		t.Fatalf("second COnvertl() error = %v", err)
	}
	if first.ShortUrl != second.ShortUrl {
		t.Fatalf("short url mismatch: first=%q second=%q", first.ShortUrl, second.ShortUrl)
	}
	if shortUrlModel.insertCalls != 1 {
		t.Fatalf("insert calls = %d, want 1", shortUrlModel.insertCalls)
	}
	if _, ok := shortUrlModel.byMd5[md5.Sum([]byte(target.URL))]; !ok {
		t.Fatal("md5 mapping should be stored")
	}
}
