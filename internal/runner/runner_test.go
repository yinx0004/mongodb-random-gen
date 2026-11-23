package runner

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"gopkg.in/yaml.v3"

	"github.com/yinx0004/mongodb-random-gen/internal/gen"
)

// memSink records documents; it can fail or block on demand.
type memSink struct {
	mu       sync.Mutex
	docs     []bson.D
	writes   atomic.Int64
	failAt   int64 // return an error on this write (1-based); 0 = never
	failEach int   // report this many failed docs per write
	onWrite  func(n int64)
}

func (m *memSink) Begin(string, bool) error { return nil }
func (m *memSink) Close() error             { return nil }
func (m *memSink) Write(ctx context.Context, docs []any) (int, error) {
	n := m.writes.Add(1)
	if m.onWrite != nil {
		m.onWrite(n)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if m.failAt > 0 && n == m.failAt {
		return 0, errors.New("boom")
	}
	m.mu.Lock()
	for _, d := range docs {
		m.docs = append(m.docs, d.(bson.D))
	}
	m.mu.Unlock()
	return m.failEach, nil
}

const tmpl = `
name: name
phone: phone
age: 18..60
level: [a 70%, b, c]
createdAt: 2023-01-01..2024-01-01
profile:
  city: city
  bio: text 5..20
`

func compile(t *testing.T, mode gen.LocaleMode) *gen.Doc {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(tmpl), &n); err != nil {
		t.Fatal(err)
	}
	d, err := gen.CompileDoc(n.Content[0], "c", mode)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExactCount(t *testing.T) {
	doc := compile(t, gen.ModeZh)
	for _, total := range []int{0, 1, 999, PartitionSize, PartitionSize + 1, 123_457} {
		for _, w := range []int{1, 3, 8} {
			s := &memSink{}
			res, err := Run(context.Background(), s, doc, total, Options{Workers: w, Batch: 700, Seed: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(s.docs) != total || res.Done != int64(total) {
				t.Fatalf("total=%d workers=%d: got %d docs, Done=%d", total, w, len(s.docs), res.Done)
			}
		}
	}
}

// The same seed yields the same documents whatever the worker count or batch size.
func TestDeterministicAcrossWorkers(t *testing.T) {
	doc := compile(t, gen.ModeMixed)
	fingerprint := func(seed uint64, workers, batch int) []string {
		s := &memSink{}
		if _, err := Run(context.Background(), s, doc, 25_000, Options{Workers: workers, Batch: batch, Seed: seed}); err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(s.docs))
		for i, d := range s.docs {
			b, err := bson.MarshalExtJSON(d[1:], false, false) // drop the random ObjectId
			if err != nil {
				t.Fatal(err)
			}
			out[i] = string(b)
		}
		slices.Sort(out)
		return out
	}
	a, b := fingerprint(7, 1, 1000), fingerprint(7, 8, 333)
	if !slices.Equal(a, b) {
		t.Fatal("same seed produced different data with different workers/batch")
	}
	if slices.Equal(a, fingerprint(8, 8, 1000)) {
		t.Fatal("different seeds produced the same data")
	}
}

func TestWriteErrorStopsWorkers(t *testing.T) {
	doc := compile(t, gen.ModeZh)
	s := &memSink{failAt: 5}
	res, err := Run(context.Background(), s, doc, 1_000_000, Options{Workers: 8, Batch: 100, Seed: 1})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("want boom, got %v", err)
	}
	if res.Done >= 1_000_000 || s.writes.Load() > 100 {
		t.Fatalf("workers kept going after the error: done=%d writes=%d", res.Done, s.writes.Load())
	}
}

func TestCancel(t *testing.T) {
	doc := compile(t, gen.ModeZh)
	ctx, cancel := context.WithCancel(context.Background())
	s := &memSink{onWrite: func(n int64) {
		if n == 10 {
			cancel()
		}
	}}
	res, err := Run(ctx, s, doc, 1_000_000, Options{Workers: 4, Batch: 100, Seed: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if res.Done >= 1_000_000 {
		t.Fatal("did not stop")
	}
	// In-flight batches finish, so the count matches what the sink received.
	if res.Done != int64(len(s.docs)) {
		t.Fatalf("Done=%d but sink has %d docs", res.Done, len(s.docs))
	}
}

func TestFailedCountAndProgress(t *testing.T) {
	doc := compile(t, gen.ModeZh)
	s := &memSink{failEach: 1}
	var last atomic.Int64
	res, err := Run(context.Background(), s, doc, 5_000, Options{Workers: 2, Batch: 1000, Seed: 1,
		Progress: func(d int64) { last.Store(d) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 5 {
		t.Fatalf("failed = %d, want 5", res.Failed)
	}
	if last.Load() != 5_000 {
		t.Fatalf("final progress = %d, want 5000", last.Load())
	}
}
