package leaf

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"shorturl/model"
)

type fakeSequenceModel struct {
	mu         sync.Mutex
	max        uint64
	allocCalls int
}

func (m *fakeSequenceModel) AllocSegment(_ context.Context, _ string, step uint64) (uint64, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if step == 0 {
		step = 100
	}
	start := m.max + 1
	m.max += step
	m.allocCalls++
	return start, m.max, nil
}

func (m *fakeSequenceModel) Insert(context.Context, *model.Sequence) (sql.Result, error) {
	return nil, nil
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

func TestSegmentGeneratorConcurrentUnique(t *testing.T) {
	const (
		workers       = 64
		idsPerWorker  = 200
		expectedCount = workers * idsPerWorker
	)

	seqModel := &fakeSequenceModel{}
	generator := NewSegmentGenerator(seqModel, "short_url", 128)
	ids := make(chan uint64, expectedCount)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerWorker; j++ {
				id, err := generator.Next(context.Background())
				if err != nil {
					t.Errorf("Next() error = %v", err)
					return
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[uint64]struct{}, expectedCount)
	for id := range ids {
		if id == 0 {
			t.Fatal("generated id should not be zero")
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicated id: %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != expectedCount {
		t.Fatalf("generated %d ids, want %d", len(seen), expectedCount)
	}
}

func TestSegmentGeneratorPreloadsNextBuffer(t *testing.T) {
	seqModel := &fakeSequenceModel{}
	generator := NewSegmentGenerator(seqModel, "short_url", 100)

	for i := 0; i < 80; i++ {
		if _, err := generator.Next(context.Background()); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		seqModel.mu.Lock()
		calls := seqModel.allocCalls
		seqModel.mu.Unlock()
		if calls >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("alloc calls = %d, want at least 2 after preload threshold", calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func BenchmarkSegmentGeneratorNext(b *testing.B) {
	generator := NewSegmentGenerator(&fakeSequenceModel{}, "short_url", 1000)
	ctx := context.Background()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := generator.Next(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
