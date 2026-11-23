// Package sink writes generated documents to MongoDB or to a JSON Lines file.
package sink

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

// Sink receives batches of documents for one collection at a time.
type Sink interface {
	Begin(coll string, drop bool) error
	Write(docs []any) (failed int, err error)
	Close() error
}

// ---------------------------------------------------------------------------
// MongoDB
// ---------------------------------------------------------------------------

type Mongo struct {
	client *mongo.Client
	db     *mongo.Database
	coll   *mongo.Collection
}

func ConnectMongo(uri, db string) (*Mongo, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return &Mongo{client: client, db: client.Database(db)}, nil
}

func (m *Mongo) Begin(coll string, drop bool) error {
	m.coll = m.db.Collection(coll)
	if drop {
		return m.coll.Drop(context.Background())
	}
	return nil
}

func (m *Mongo) Write(docs []any) (int, error) {
	_, err := m.coll.InsertMany(context.Background(), docs, options.InsertMany().SetOrdered(false))
	if err == nil {
		return 0, nil
	}
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) && bwe.WriteConcernError == nil && len(bwe.WriteErrors) > 0 {
		return len(bwe.WriteErrors), nil
	}
	return 0, err
}

func (m *Mongo) Close() error { return m.client.Disconnect(context.Background()) }

// ---------------------------------------------------------------------------
// JSON Lines (MongoDB relaxed Extended JSON, one document per line)
// ---------------------------------------------------------------------------

type JSONL struct {
	w      *bufio.Writer
	closer io.Closer
}

func NewJSONL(w io.Writer, closer io.Closer) *JSONL {
	return &JSONL{w: bufio.NewWriterSize(w, 1<<20), closer: closer}
}

func (j *JSONL) Begin(string, bool) error { return nil }

func (j *JSONL) Write(docs []any) (int, error) {
	for _, d := range docs {
		b, err := bson.MarshalExtJSON(d, false, false)
		if err != nil {
			return 0, err
		}
		j.w.Write(b)
		j.w.WriteByte('\n')
	}
	return 0, nil
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

func (d *JSONLDir) Write(docs []any) (int, error) { return d.cur.Write(docs) }

func (d *JSONLDir) Close() error { return d.closeCur() }

func (d *JSONLDir) closeCur() error {
	if d.cur == nil {
		return nil
	}
	err := d.cur.Close()
	d.cur = nil
	return err
}

// MinServerVersion is the oldest MongoDB server the bundled driver supports.
const MinServerVersion = "4.0"

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

// ErrConnect wraps a connection failure with a hint.
func ErrConnect(uri string, err error) error {
	if strings.Contains(err.Error(), "wire version") {
		return i18n.Errorf("连不上 MongoDB（%s）：服务端版本太旧。mgen 支持 MongoDB %s 及以上\n详细信息：%v",
			"cannot connect to MongoDB (%s): the server is too old. mgen supports MongoDB %s and newer\ndetails: %v",
			RedactURI(uri), MinServerVersion, err)
	}
	return i18n.Errorf("连不上 MongoDB（%s）：%v\n提示：用 --uri 指定地址，或先用 mgen preview 只看效果、用 --out 写到文件",
		"cannot connect to MongoDB (%s): %v\nhint: pass --uri, or try `mgen preview` / --out to skip the database", RedactURI(uri), err)
}
