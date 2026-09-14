package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(stats *Stats, ambiguous bool, timeout time.Duration) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	fresh := base.Clone()
	fresh.DisableKeepAlives = true
	return &http.Client{Timeout: timeout, Transport: &retryTransport{base: base, fresh: fresh, stats: stats, maxRetries: 2, retryAmbiguous: ambiguous}}
}
func post(t *testing.T, client *http.Client, url string) error {
	t.Helper()
	resp, err := client.Post(url, "application/json", bytes.NewReader([]byte(`{"message":"test"}`)))
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return err
}

func TestConnectionRotation(t *testing.T) {
	for _, tc := range []struct {
		name                                                    string
		closes                                                  int
		allow                                                   bool
		wantAttempts, wantRetries, wantRecovered, wantAmbiguous int64
		fail                                                    bool
	}{
		{"recovered", 1, true, 2, 1, 1, 1, false},
		{"bounded", 10, true, 3, 2, 0, 1, true},
		{"ambiguous_disabled", 1, false, 1, 0, 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()
				if string(body) != `{"message":"test"}` {
					t.Errorf("payload changed: %s", body)
				}
				if attempts.Add(1) <= int64(tc.closes) {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			stats := &Stats{}
			client := testClient(stats, tc.allow, time.Second)
			defer client.CloseIdleConnections()
			err := post(t, client, server.URL)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v", err)
			}
			if attempts.Load() != tc.wantAttempts || stats.retryAttempts != tc.wantRetries || stats.recoveredRequests != tc.wantRecovered || stats.ambiguousDeliveries != tc.wantAmbiguous || stats.connectionErrors != int64(min(tc.closes, 3)) {
				t.Fatalf("attempts=%d stats=%+v", attempts.Load(), stats)
			}
		})
	}
}

func TestGracefulCloseNeedsNoApplicationRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Connection", "close")
		w.WriteHeader(200)
	}))
	defer server.Close()
	stats := &Stats{}
	client := testClient(stats, true, time.Second)
	defer client.CloseIdleConnections()
	for i := 0; i < 20; i++ {
		if err := post(t, client, server.URL); err != nil {
			t.Fatal(err)
		}
	}
	if stats.connectionErrors != 0 || stats.retryAttempts != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestRequestDeadlineSpansRetries(t *testing.T) {
	stats := &Stats{}
	var attempts atomic.Int64
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		select {
		case <-time.After(40 * time.Millisecond):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		httptrace.ContextClientTrace(r.Context()).WroteHeaders()
		return nil, io.EOF
	})
	client := &http.Client{Timeout: 65 * time.Millisecond, Transport: &retryTransport{base: transport, fresh: transport, stats: stats, maxRetries: 2, retryAmbiguous: true}}
	start := time.Now()
	err := post(t, client, "http://example.invalid")
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 2 || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("err=%v attempts=%d elapsed=%s", err, attempts.Load(), time.Since(start))
	}
}

func TestHTTPFailuresAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 429, 500, 503} {
		var attempts atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); w.WriteHeader(status) }))
		stats := &Stats{}
		client := testClient(stats, true, time.Second)
		config := &Config{Endpoint: server.URL, ContentType: "application/json", FormatStyle: "single"}
		err := sendBatch(client, config, 1, stats, &PayloadPool{nginxLogsBytes: [][]byte{[]byte(`{}`)}})
		client.CloseIdleConnections()
		server.Close()
		if err == nil || attempts.Load() != 1 || stats.httpErrors != 1 || stats.retryAttempts != 0 || stats.totalLogs != 1 {
			t.Fatalf("status=%d stats=%+v attempts=%d err=%v", status, stats, attempts.Load(), err)
		}
	}
}

func TestRecoveredBatchCountedOnce(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if attempts.Add(1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	stats := &Stats{}
	client := testClient(stats, true, time.Second)
	defer client.CloseIdleConnections()
	err := sendBatch(client, &Config{Endpoint: server.URL, ContentType: "application/json", FormatStyle: "single"}, 1, stats, &PayloadPool{nginxLogsBytes: [][]byte{[]byte(`{}`)}})
	if err != nil || stats.totalLogs != 1 || stats.successfulLogs != 1 || stats.httpErrors != 0 || stats.recoveredRequests != 1 {
		t.Fatalf("err=%v stats=%+v", err, stats)
	}
}

func TestSafeRetryDoesNotRequireAmbiguousOptIn(t *testing.T) {
	stats := &Stats{}
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) { r.Body.Close(); return nil, io.EOF })
	fresh := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.Body.Close()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})
	client := &http.Client{Transport: &retryTransport{base: base, fresh: fresh, stats: stats, maxRetries: 2}}
	if err := post(t, client, "http://example.invalid"); err != nil {
		t.Fatal(err)
	}
	if stats.retryAttempts != 1 || stats.ambiguousDeliveries != 0 || stats.recoveredRequests != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestTruncatedResponseIsNotRecovered(t *testing.T) {
	var attempts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		conn, _, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		if attempts.Add(1) > 1 {
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort")
		}
	}))
	defer server.Close()
	stats := &Stats{}
	client := testClient(stats, true, time.Second)
	defer client.CloseIdleConnections()
	err := sendBatch(client, &Config{Endpoint: server.URL, ContentType: "application/json", FormatStyle: "single"}, 1, stats, &PayloadPool{nginxLogsBytes: [][]byte{[]byte(`{}`)}})
	if err == nil || stats.totalLogs != 1 || stats.successfulLogs != 0 || stats.httpErrors != 1 || stats.recoveredRequests != 0 || attempts.Load() != 2 {
		t.Fatalf("err=%v stats=%+v attempts=%d", err, stats, attempts.Load())
	}
}

func TestCanceledRequestDoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stats := &Stats{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("transport called after cancellation")
		return nil, io.EOF
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://example.invalid", bytes.NewReader([]byte(`{}`)))
	_, err := (&retryTransport{base: transport, fresh: transport, stats: stats, maxRetries: 2, retryAmbiguous: true}).RoundTrip(req)
	if !errors.Is(err, context.Canceled) || stats.retryAttempts != 0 {
		t.Fatalf("err=%v stats=%+v", err, stats)
	}
}
