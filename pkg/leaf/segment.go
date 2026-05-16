package leaf

import (
	"context"
	"fmt"
	"sync"

	"shorturl/model"

	"github.com/zeromicro/go-zero/core/logx"
)

const preloadPercent = 75

type segmentBuffer struct {
	start uint64
	cur   uint64
	max   uint64
	ready bool
}

func (b *segmentBuffer) reset(start, end uint64) {
	b.start = start
	b.cur = start - 1
	b.max = end
	b.ready = true
}

func (b *segmentBuffer) hasNext() bool {
	return b.ready && b.cur < b.max
}

func (b *segmentBuffer) next() uint64 {
	b.cur++
	return b.cur
}

func (b *segmentBuffer) needPreload() bool {
	if !b.ready || b.max < b.start {
		return false
	}

	total := b.max - b.start + 1
	used := b.cur - b.start + 1
	return used*100 >= total*preloadPercent
}

type SegmentGenerator struct {
	model  model.SequenceModel
	bizTag string
	step   uint64

	mu          sync.Mutex
	cond        *sync.Cond
	current     int
	buffers     [2]segmentBuffer
	loadingNext bool
}

func NewSegmentGenerator(sequenceModel model.SequenceModel, bizTag string, step uint64) *SegmentGenerator {
	g := &SegmentGenerator{
		model:  sequenceModel,
		bizTag: bizTag,
		step:   step,
	}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *SegmentGenerator) Next(ctx context.Context) (uint64, error) {
	for {
		g.mu.Lock()

		current := &g.buffers[g.current]
		if current.hasNext() {
			id := current.next()
			g.preloadNextLocked()
			g.mu.Unlock()
			return id, nil
		}

		if current.ready {
			current.ready = false
		}

		nextIndex := g.nextIndex()
		if g.buffers[nextIndex].ready {
			g.current = nextIndex
			g.mu.Unlock()
			continue
		}

		if g.loadingNext {
			for g.loadingNext && !g.buffers[nextIndex].ready {
				g.cond.Wait()
			}
			if g.buffers[nextIndex].ready {
				g.current = nextIndex
			}
			g.mu.Unlock()
			continue
		}

		target := g.current
		g.loadingNext = true
		g.mu.Unlock()

		start, end, err := g.allocSegment(ctx)

		g.mu.Lock()
		g.loadingNext = false
		if err != nil {
			g.cond.Broadcast()
			g.mu.Unlock()
			return 0, err
		}

		g.buffers[target].reset(start, end)
		g.current = target
		g.cond.Broadcast()
		g.mu.Unlock()
	}
}

func (g *SegmentGenerator) preloadNextLocked() {
	current := &g.buffers[g.current]
	nextIndex := g.nextIndex()
	if !current.needPreload() || g.buffers[nextIndex].ready || g.loadingNext {
		return
	}

	g.loadingNext = true
	go g.loadNext(context.Background(), nextIndex)
}

func (g *SegmentGenerator) loadNext(ctx context.Context, target int) {
	start, end, err := g.allocSegment(ctx)

	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.cond.Broadcast()

	g.loadingNext = false
	if err != nil {
		logx.Errorw("SegmentGenerator.allocSegment failed", logx.LogField{Key: "err", Value: err.Error()})
		return
	}

	if !g.buffers[target].ready {
		g.buffers[target].reset(start, end)
	}
}

func (g *SegmentGenerator) allocSegment(ctx context.Context) (uint64, uint64, error) {
	if g.model == nil {
		return 0, 0, fmt.Errorf("sequence model is nil")
	}

	start, end, err := g.model.AllocSegment(ctx, g.bizTag, g.step)
	if err != nil {
		return 0, 0, err
	}
	if start == 0 || end < start {
		return 0, 0, fmt.Errorf("invalid segment: start=%d end=%d", start, end)
	}

	return start, end, nil
}

func (g *SegmentGenerator) nextIndex() int {
	if g.current == 0 {
		return 1
	}

	return 0
}
