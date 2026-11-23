// Package runner generates one collection with a pool of workers.
//
// The documents are split into fixed-size partitions, and partition p always
// uses the random stream (seed, p). Workers take partitions from a queue, so
// the generated data depends only on the seed, never on the worker count or
// on scheduling (only the insertion order changes).
package runner

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yinx0004/mongodb-random-gen/internal/gen"
	"github.com/yinx0004/mongodb-random-gen/internal/sink"
)

// PartitionSize is the number of documents per partition. Changing it changes
// which documents a given seed produces, so treat it as part of the format.
const PartitionSize = 10_000

type Options struct {
	Workers int
	Batch   int
	Seed    uint64
	// Progress, if set, is called about every interval with the number of
	// documents handed to the sink so far, and once more at the end.
	Progress         func(done int64)
	ProgressInterval time.Duration
}

type Result struct {
	Done   int64 // documents sent to the sink (including failed ones)
	Failed int64 // documents the sink reported as not inserted
}

// Run writes total documents generated from doc into s, which must already
// have had Begin called. On error or cancellation it stops all workers and
// returns what was done so far.
func Run(ctx context.Context, s sink.Sink, doc *gen.Doc, total int, opt Options) (Result, error) {
	if opt.Workers < 1 {
		opt.Workers = 1
	}
	if opt.Batch < 1 {
		opt.Batch = 1000
	}
	if opt.ProgressInterval <= 0 {
		opt.ProgressInterval = 200 * time.Millisecond
	}

	parent := ctx
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// In-flight writes are never cancelled: on Ctrl-C or error the workers
	// stop starting new batches but let the current ones finish, so Done is
	// exactly what reached the sink.
	writeCtx := context.WithoutCancel(parent)

	var done, failed atomic.Int64
	var firstErr error
	var errOnce sync.Once
	fail := func(err error) {
		errOnce.Do(func() { firstErr = err })
		cancel()
	}

	parts := (total + PartitionSize - 1) / PartitionSize
	queue := make(chan int)
	var wg sync.WaitGroup
	for range min(opt.Workers, max(parts, 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]any, 0, opt.Batch)
			for p := range queue {
				stream := doc.Stream(opt.Seed, uint64(p))
				lo, hi := p*PartitionSize, min((p+1)*PartitionSize, total)
				for i := lo; i < hi; {
					if ctx.Err() != nil {
						return
					}
					n := min(opt.Batch, hi-i)
					buf = buf[:0]
					for range n {
						buf = append(buf, stream.Next())
					}
					f, err := s.Write(writeCtx, buf)
					if err != nil {
						fail(err)
						return
					}
					failed.Add(int64(f))
					done.Add(int64(n))
					i += n
				}
			}
		}()
	}

	stopProgress := make(chan struct{})
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		if opt.Progress == nil {
			<-stopProgress
			return
		}
		t := time.NewTicker(opt.ProgressInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				opt.Progress(done.Load())
			case <-stopProgress:
				opt.Progress(done.Load())
				return
			}
		}
	}()

feed:
	for p := range parts {
		select {
		case queue <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	close(stopProgress)
	<-progressDone

	res := Result{Done: done.Load(), Failed: failed.Load()}
	// If the caller cancelled (e.g. Ctrl-C), report that rather than the
	// "context canceled" errors of the writes it interrupted.
	if err := parent.Err(); err != nil {
		return res, err
	}
	return res, firstErr
}
