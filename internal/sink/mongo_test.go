package sink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yinx0004/mongodb-random-gen/internal/i18n"
)

func init() { i18n.Zh = true }

type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

func TestRetryable(t *testing.T) {
	yes := []error{
		context.DeadlineExceeded,
		fmt.Errorf("connection handshake: %w", context.DeadlineExceeded),
		fmt.Errorf("read tcp: %w", netTimeout{}),
		mongo.CommandError{Code: 91, Labels: []string{"RetryableWriteError"}},
		mongo.CommandError{Labels: []string{"NetworkError"}},
		errors.New("server selection error: context deadline exceeded"),
	}
	no := []error{
		nil,
		context.Canceled,
		errors.New("boom"),
		mongo.CommandError{Code: 13, Message: "not authorized"},
	}
	for _, e := range yes {
		if !Retryable(e) {
			t.Errorf("%v should be retryable", e)
		}
	}
	for _, e := range no {
		if Retryable(e) {
			t.Errorf("%v should not be retryable", e)
		}
	}
}

func TestIsDuplicateID(t *testing.T) {
	if !isDuplicateID(mongo.WriteError{Code: 11000, Message: "E11000 duplicate key error collection: a.b index: _id_ dup key: { _id: 1 }"}) {
		t.Error("_id duplicate (4.2+ format) not detected")
	}
	if !isDuplicateID(mongo.WriteError{Code: 11000, Message: "E11000 duplicate key error collection: a.b index: _id_ dup key: { : 1 }"}) {
		t.Error("_id duplicate (4.0 format) not detected")
	}
	if isDuplicateID(mongo.WriteError{Code: 11000, Message: "E11000 duplicate key error collection: a.b index: email_1 dup key: { email: \"x\" }"}) {
		t.Error("other unique index must not count as an _id duplicate")
	}
}

func TestBackoff(t *testing.T) {
	for attempt, base := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second} {
		for range 50 {
			d := backoff(attempt)
			if d < base*8/10 || d > base*12/10 {
				t.Fatalf("attempt %d: %v not within ±20%% of %v", attempt, d, base)
			}
		}
	}
}

func TestHint(t *testing.T) {
	uri := "mongodb://u:p@h:3067/?connectTimeoutMS=2000&socketTimeoutMS=5000"
	handshake := mongo.CommandError{Labels: []string{"NetworkError"}, Wrapped: fmt.Errorf("connection() error occurred during connection handshake: %w", context.DeadlineExceeded)}
	h := Hint(uri, handshake)
	if !strings.Contains(h, "connectTimeoutMS=2000") || strings.Contains(h, "socketTimeoutMS") || !strings.Contains(h, "-w 4") {
		t.Errorf("handshake timeout should blame connectTimeoutMS only:\n%s", h)
	}

	readTimeout := fmt.Errorf("incomplete read of message header: %w", netTimeout{})
	h = Hint(uri, readTimeout)
	if !strings.Contains(h, "socketTimeoutMS=5000") || strings.Contains(h, "connectTimeoutMS") {
		t.Errorf("read timeout should blame socketTimeoutMS only:\n%s", h)
	}

	eof := mongo.CommandError{Labels: []string{"NetworkError"}, Message: "connection closed unexpectedly by the other side: EOF"}
	h = Hint(uri, eof)
	if strings.Contains(h, "TimeoutMS") || !strings.Contains(h, "对端关闭") {
		t.Errorf("a dropped connection is not a timeout setting problem:\n%s", h)
	}

	if h := Hint("mongodb://h/?connectTimeoutMS=30000", handshake); strings.Contains(h, "connectTimeoutMS") {
		t.Errorf("30s connect timeout should not be blamed:\n%s", h)
	}
	if Hint(uri, errors.New("not authorized")) != "" {
		t.Error("non-network errors get no hint")
	}
}

func TestConnectHint(t *testing.T) {
	// The first hello of the monitoring connection never says "handshake",
	// but it is still bounded by connectTimeoutMS.
	err := errors.New("server selection error: context deadline exceeded, current topology: { Last error: connection(h:1[-5]) incomplete read of message header: context deadline exceeded: client timed out waiting for server response: read tcp: i/o timeout }")
	got := ErrConnect("mongodb://u:pw@h:1/?connectTimeoutMS=2000", err).Error()
	if !strings.Contains(got, "connectTimeoutMS=2000 太小") || strings.Contains(got, "-w 4") || strings.Contains(got, "pw@") {
		t.Errorf("connect hint wrong:\n%s", got)
	}
	if got := ErrConnect("mongodb://h:1/", errors.New("server selection error: connection refused")).Error(); !strings.Contains(got, "--uri") {
		t.Errorf("plain connect failure should keep the generic hint:\n%s", got)
	}
}

func TestRetryErrorUnwraps(t *testing.T) {
	err := error(&RetryError{Retries: 5, Err: context.DeadlineExceeded})
	if !errors.Is(err, context.DeadlineExceeded) || err.Error() != context.DeadlineExceeded.Error() {
		t.Fatal("RetryError must wrap the underlying error")
	}
}

func TestLegacyHeartbeat(t *testing.T) {
	cases := map[string]bool{"3.6.23": true, "4.0.28": true, "4.2.25": true, "4.4.0": false, "5.0.1": false, "8.0.4": false, "": false, "garbage": false}
	for v, want := range cases {
		if got := legacyHeartbeat(v); got != want {
			t.Errorf("legacyHeartbeat(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestConnectTimeoutWarning(t *testing.T) {
	w := ConnectTimeoutWarning("mongodb://u:p@h/?connectTimeoutMS=2000", "4.0.28")
	if !strings.Contains(w, "connectTimeoutMS=2000") || !strings.Contains(w, "4.0.28") || !strings.Contains(w, "心跳") {
		t.Errorf("4.0 warning should mention the heartbeat:\n%s", w)
	}
	w = ConnectTimeoutWarning("mongodb://h/?connectTimeoutMS=2000", "7.0.1")
	if w == "" || strings.Contains(w, "心跳") {
		t.Errorf("7.0 warning should not mention the heartbeat:\n%s", w)
	}
	for _, uri := range []string{"mongodb://h/", "mongodb://h/?connectTimeoutMS=10000", "mongodb://h/?connectTimeoutMS=0", "mongodb://h/?CONNECTTIMEOUTMS=30000"} {
		if w := ConnectTimeoutWarning(uri, "4.0.28"); w != "" {
			t.Errorf("%s should not warn:\n%s", uri, w)
		}
	}
	if ConnectTimeoutWarning("mongodb://h/?CONNECTTIMEOUTMS=500", "") == "" {
		t.Error("option names are case-insensitive")
	}
}
