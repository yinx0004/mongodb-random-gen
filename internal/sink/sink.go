// Package sink writes generated documents to MongoDB or to a JSON Lines file.
package sink

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Sink receives batches of documents for one collection at a time.
//
// Begin is called while no writes are in flight. After Begin returns, Write
// may be called from many goroutines at once.
type Sink interface {
	Begin(coll string, drop bool) error
	Write(ctx context.Context, docs []any) (failed int, err error)
	Close() error
}

// ---------------------------------------------------------------------------
// JSON Lines (MongoDB relaxed Extended JSON, one document per line)
// ---------------------------------------------------------------------------

type JSONL struct {
	mu     sync.Mutex
	w      *bufio.Writer
	closer io.Closer
}

func NewJSONL(w io.Writer, closer io.Closer) *JSONL {
	return &JSONL{w: bufio.NewWriterSize(w, 1<<20), closer: closer}
}

func (j *JSONL) Begin(string, bool) error { return nil }

// Write encodes outside the lock so concurrent callers encode in parallel.
func (j *JSONL) Write(_ context.Context, docs []any) (int, error) {
	var buf bytes.Buffer
	for _, d := range docs {
		b, err := bson.MarshalExtJSON(d, false, false)
		if err != nil {
			return 0, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err := j.w.Write(buf.Bytes())
	return 0, err
}

func (j *JSONL) Close() error {
	if err := j.w.Flush(); err != nil {
		return err
	}
	if j.closer != nil {
		return j.closer.Close()
	}
	return nil
}

// JSONLDir writes one <collection>.jsonl file per collection into a directory.
type JSONLDir struct {
	Dir   string
	Files []string
	cur   *JSONL
}

func (d *JSONLDir) Begin(coll string, _ bool) error {
	if err := d.closeCur(); err != nil {
		return err
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(d.Dir, coll+".jsonl")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	d.Files = append(d.Files, p)
	d.cur = NewJSONL(f, f)
	return nil
}

func (d *JSONLDir) Write(ctx context.Context, docs []any) (int, error) { return d.cur.Write(ctx, docs) }

func (d *JSONLDir) Close() error { return d.closeCur() }

func (d *JSONLDir) closeCur() error {
	if d.cur == nil {
		return nil
	}
	err := d.cur.Close()
	d.cur = nil
	return err
}

// RedactURI hides the password in a connection string. The credentials end at
// the last '@' before the options, so passwords containing '@' or '/' are
// fully hidden too.
func RedactURI(uri string) string {
	i := strings.Index(uri, "://")
	if i < 0 {
		return uri
	}
	rest := uri[i+3:]
	head, tail := rest, ""
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		head, tail = rest[:q], rest[q:]
	}
	at := strings.LastIndexByte(head, '@')
	if at < 0 {
		return uri
	}
	user, _, _ := strings.Cut(head[:at], ":")
	return uri[:i+3] + user + ":***" + head[at:] + tail
}
