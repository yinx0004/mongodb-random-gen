package sink

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

// MinServerVersion is the oldest MongoDB server the bundled driver supports.
const MinServerVersion = "4.0"

// MongoOptions tunes the MongoDB sink.
type MongoOptions struct {
	// WriteConcern overrides the URI / server default when non-nil.
	WriteConcern *writeconcern.WriteConcern
	// Workers is the number of concurrent writers; that many connections are
	// opened before writing starts (warm-up).
	Workers int
	// Retries is how many times a batch is retried after a transient error.
	Retries int
	// OnRetry is called before each retry. It may be called concurrently.
	OnRetry func(coll string, attempt int, wait time.Duration, err error)
}

type Mongo struct {
	client *mongo.Client
	db     *mongo.Database
	coll   *mongo.Collection
	opt    MongoOptions

	// ServerVersion is the server's version string, e.g. "4.0.28" ("" if unknown).
	ServerVersion string
}

// ConnectMongo connects, pings, and opens opt.Workers connections up front so
// that connection handshakes do not compete with the bulk load.
func ConnectMongo(uri, db string, opt MongoOptions) (*Mongo, error) {
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second)
	if opt.WriteConcern != nil {
		opts.SetWriteConcern(opt.WriteConcern)
	}
	target := max(opt.Workers, 1)
	if opts.MaxPoolSize != nil && *opts.MaxPoolSize > 0 && *opts.MaxPoolSize < uint64(target) {
		target = int(*opts.MaxPoolSize)
	}
	if opts.MinPoolSize == nil || *opts.MinPoolSize < uint64(target) {
		opts.SetMinPoolSize(uint64(target))
	}
	w := newPoolWatch()
	opts.SetPoolMonitor(&event.PoolMonitor{Event: w.event})

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	w.recordPrimary(true)
	err = client.Ping(ctx, nil)
	w.recordPrimary(false)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	w.wait(target, 30*time.Second)
	return &Mongo{client: client, db: client.Database(db), opt: opt, ServerVersion: serverVersion(client)}, nil
}

func serverVersion(client *mongo.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var info struct {
		Version string `bson:"version"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err != nil {
		return ""
	}
	return info.Version
}

// legacyHeartbeat reports whether the server predates MongoDB 4.4. Those
// servers have no streaming heartbeat, so the driver bounds every heartbeat
// by connectTimeoutMS.
func legacyHeartbeat(version string) bool {
	major, rest, ok := strings.Cut(version, ".")
	if !ok {
		return false
	}
	minorStr, _, _ := strings.Cut(rest, ".")
	ma, err1 := strconv.Atoi(major)
	mi, err2 := strconv.Atoi(minorStr)
	return err1 == nil && err2 == nil && (ma < 4 || ma == 4 && mi < 4)
}

// MinConnectTimeoutMS is the smallest connectTimeoutMS that mgen does not warn about.
const MinConnectTimeoutMS = 10000

// ConnectTimeoutWarning returns a warning when the URI sets a connectTimeoutMS
// small enough to break a bulk load, or "". serverVersion may be empty.
func ConnectTimeoutWarning(uri, serverVersion string) string {
	ms, ok := uriOptions(uri)["connecttimeoutms"]
	if !ok || ms <= 0 || ms >= MinConnectTimeoutMS {
		return ""
	}
	msg := i18n.Tf(
		"⚠ 连接串里 connectTimeoutMS=%d 偏小（建议不小于 %d，或去掉它用默认的 30000）。它限制每条新连接“建连 + 握手 + 认证”的总时间",
		"⚠ connectTimeoutMS=%d in the URI is small (use at least %d, or drop it for the default 30000). It bounds connect + handshake + authentication of every new connection",
		ms, MinConnectTimeoutMS)
	if legacyHeartbeat(serverVersion) {
		msg += i18n.Tf("；当前服务端是 MongoDB %s（低于 4.4），驱动的心跳超时也等于它",
			"; this server is MongoDB %s (older than 4.4), so it is also the heartbeat timeout", serverVersion)
	}
	msg += i18n.T("。服务端繁忙时一旦超时，驱动会清空连接池、重建所有连接，写入可能变慢或出错（mgen 会自动重试）。",
		". When it is exceeded on a busy server, the driver clears the pool and reconnects everything, so writes may slow down or fail (mgen retries them).")
	return msg
}

func (m *Mongo) Begin(coll string, drop bool) error {
	m.coll = m.db.Collection(coll)
	if drop {
		return m.coll.Drop(context.Background())
	}
	return nil
}

// Write inserts docs, retrying transient errors (network, timeouts, failover)
// with backoff. Retrying is safe because every document already carries its
// _id: documents that the failed attempt did insert come back as duplicate
// _id errors on the retry and are counted as written.
func (m *Mongo) Write(ctx context.Context, docs []any) (int, error) {
	for attempt := 0; ; attempt++ {
		failed, err := m.insert(ctx, docs, attempt > 0)
		if err == nil || !Retryable(err) {
			return failed, err
		}
		if attempt >= m.opt.Retries {
			return failed, &RetryError{Retries: attempt, Err: err}
		}
		wait := backoff(attempt)
		if m.opt.OnRetry != nil {
			m.opt.OnRetry(m.coll.Name(), attempt+1, wait, err)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return 0, err
		}
	}
}

func (m *Mongo) insert(ctx context.Context, docs []any, isRetry bool) (int, error) {
	_, err := m.coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err == nil {
		return 0, nil
	}
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) && len(bwe.WriteErrors) > 0 && bwe.WriteConcernError == nil && len(bwe.Labels) == 0 {
		failed := 0
		for _, we := range bwe.WriteErrors {
			if isRetry && isDuplicateID(we.WriteError) {
				continue // inserted by the attempt that failed
			}
			failed++
		}
		return failed, nil
	}
	return 0, err
}

// RetryError is returned when a batch still fails after all retries.
type RetryError struct {
	Retries int
	Err     error
}

func (e *RetryError) Error() string { return e.Err.Error() }
func (e *RetryError) Unwrap() error { return e.Err }

func isDuplicateID(we mongo.WriteError) bool {
	return we.Code == 11000 && strings.Contains(we.Message, "index: _id_ ")
}

// Retryable reports whether a failed batch is worth retrying.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var le mongo.LabeledError
	if errors.As(err, &le) && (le.HasErrorLabel("RetryableWriteError") || le.HasErrorLabel("TransientTransactionError")) {
		return true
	}
	return mongo.IsNetworkError(err) || mongo.IsTimeout(err) ||
		strings.Contains(err.Error(), "server selection error") ||
		strings.Contains(err.Error(), "connection pool")
}

// backoff returns 1s, 2s, 4s, 8s, 15s, 15s ... with ±20% jitter. The default
// 5 retries wait about 30s in total, enough to ride out a replica set election.
func backoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 4)
	d = min(d, 15*time.Second)
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func (m *Mongo) Close() error { return m.client.Disconnect(context.Background()) }

// ---------------------------------------------------------------------------
// Connection warm-up
// ---------------------------------------------------------------------------

// poolWatch follows connection pool events to know when the primary's pool
// holds enough ready connections.
type poolWatch struct {
	mu        sync.Mutex
	recording bool
	primary   string
	live      map[string]int
	failed    map[string]int
	changed   chan struct{}
}

func newPoolWatch() *poolWatch {
	return &poolWatch{live: map[string]int{}, failed: map[string]int{}, changed: make(chan struct{}, 1)}
}

func (w *poolWatch) recordPrimary(on bool) {
	w.mu.Lock()
	w.recording = on
	w.mu.Unlock()
}

func (w *poolWatch) event(e *event.PoolEvent) {
	w.mu.Lock()
	switch e.Type {
	case event.ConnectionCheckedOut:
		if w.recording && w.primary == "" {
			w.primary = e.Address
		}
	case event.ConnectionReady:
		w.live[e.Address]++
	case event.ConnectionClosed:
		if e.Reason == event.ReasonError {
			w.failed[e.Address]++
		} else {
			w.live[e.Address]--
		}
	}
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// wait blocks until the primary has target ready connections, a connection
// to it fails (the writes will then retry and report), or the timeout passes.
func (w *poolWatch) wait(target int, timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		w.mu.Lock()
		p := w.primary
		done := p != "" && (w.live[p] >= target || w.failed[p] > 0)
		w.mu.Unlock()
		if done || p == "" {
			return
		}
		select {
		case <-w.changed:
		case <-deadline:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Errors and hints
// ---------------------------------------------------------------------------

// ErrConnect wraps a connection failure with a hint.
func ErrConnect(uri string, err error) error {
	if strings.Contains(err.Error(), "wire version") {
		return i18n.Errorf("连不上 MongoDB（%s）：服务端版本太旧。mgen 支持 MongoDB %s 及以上\n详细信息：%v",
			"cannot connect to MongoDB (%s): the server is too old. mgen supports MongoDB %s and newer\ndetails: %v",
			RedactURI(uri), MinServerVersion, err)
	}
	msg := i18n.Tf("连不上 MongoDB（%s）：%v", "cannot connect to MongoDB (%s): %v", RedactURI(uri), err)
	if h := hint(uri, err, true); h != "" {
		return fmt.Errorf("%s\n%s", msg, h)
	}
	return fmt.Errorf("%s\n%s", msg, i18n.T("提示：用 --uri 指定地址，或先用 mgen preview 只看效果、用 --out 写到文件",
		"hint: pass --uri, or try `mgen preview` / --out to skip the database"))
}

// Hint explains likely causes of a failed write (network error or timeout),
// pointing at timeout settings in the URI when they match the failure, or
// returns "".
func Hint(uri string, err error) string { return hint(uri, err, false) }

// hint is Hint; connecting means err happened while establishing the first
// connection, where every timeout is bounded by connectTimeoutMS.
func hint(uri string, err error, connecting bool) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	timeout := mongo.IsTimeout(err) || strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded")
	handshake := connecting || strings.Contains(msg, "handshake")
	if !timeout && !handshake && !mongo.IsNetworkError(err) {
		return ""
	}
	q := uriOptions(uri)
	var lines []string
	if ms, ok := q["connecttimeoutms"]; ok && ms > 0 && ms < 10000 && handshake && timeout {
		lines = append(lines, i18n.Tf(
			"连接串里 connectTimeoutMS=%d 太小：它限制的是“建连 + 握手 + 认证”的总时间，服务端繁忙时很容易超时。建议去掉（默认 30000）或调大",
			"connectTimeoutMS=%d in the URI is too small: it bounds connect + handshake + authentication together, which easily times out on a busy server. Remove it (default 30000) or raise it", ms))
	}
	if ms, ok := q["sockettimeoutms"]; ok && ms > 0 && ms < 60000 && timeout && !handshake {
		lines = append(lines, i18n.Tf(
			"连接串里 socketTimeoutMS=%d 偏小：服务端繁忙时，一批写入的响应可能超过它。建议去掉或调大",
			"socketTimeoutMS=%d in the URI is small: a batch insert can take longer on a busy server. Remove it or raise it", ms))
	}
	if ms, ok := q["timeoutms"]; ok && ms > 0 && ms < 60000 && timeout {
		lines = append(lines, i18n.Tf(
			"连接串里 timeoutMS=%d 限制了每个操作（含重试）的总时间，建议去掉或调大",
			"timeoutMS=%d in the URI caps each operation including retries. Remove it or raise it", ms))
	}
	if !timeout && (strings.Contains(msg, "closed unexpectedly") || strings.Contains(msg, "connection reset")) {
		lines = append(lines, i18n.T(
			"连接被对端关闭：网络不稳定，或中间的代理 / 负载均衡 / 防火墙断开了连接。可以用 --retries 加大重试次数",
			"the connection was closed by the other side: an unstable network, or a proxy / load balancer / firewall dropped it. --retries allows more retries"))
	}
	if !connecting {
		lines = append(lines, i18n.T("也可以降低并发（如 -w 4）或减小每批条数（如 --batch 500）",
			"you can also lower concurrency (e.g. -w 4) or the batch size (e.g. --batch 500)"))
	}
	if len(lines) == 0 {
		return ""
	}
	return i18n.T("提示：", "hint: ") + strings.Join(lines, i18n.T("；\n      ", ";\n      "))
}

// uriOptions returns the numeric query options of a URI with lower-case keys.
func uriOptions(uri string) map[string]int {
	out := map[string]int{}
	u, err := url.Parse(uri)
	if err != nil {
		return out
	}
	for k, v := range u.Query() {
		if n, err := strconv.Atoi(v[0]); err == nil {
			out[strings.ToLower(k)] = n
		}
	}
	return out
}
