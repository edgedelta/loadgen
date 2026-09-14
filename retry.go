package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"syscall"
	"time"
)

// retryTransport retries connection failures only. It never retries HTTP status
// errors, cancellation or timeouts. http.Client.Timeout spans this entire call.
// Counters describe attempts visible here; net/http may also retry internally
// when it knows that replay is safe.
type retryTransport struct {
	base, fresh    http.RoundTripper
	stats          *Stats
	maxRetries     int
	retryAmbiguous bool
}

func connectionFailure(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) ||
		err.Error() == "http: server closed idle connection"
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ambiguousRecorded := false
	for attempt := 0; ; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		current := req.Clone(req.Context())
		if attempt > 0 {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("rewind request: %w", err)
			}
			current.Body = body
		}
		// WroteHeaders is conservative: even a partially written request may have
		// reached the server. Do not equate a missing response with non-delivery.
		var wrote atomic.Bool
		trace := &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) },
			WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) }}
		current = current.WithContext(httptrace.WithClientTrace(current.Context(), trace))
		transport := t.base
		if attempt > 0 {
			transport = t.fresh
			t.stats.mu.Lock()
			t.stats.retryAttempts++
			t.stats.mu.Unlock()
		}
		resp, err := transport.RoundTrip(current)
		if err == nil {
			if attempt > 0 && resp.StatusCode < 400 {
				resp.Body = &recoveredBody{ReadCloser: resp.Body, stats: t.stats}
			}
			return resp, nil
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if !connectionFailure(err) {
			return nil, err
		}
		t.stats.mu.Lock()
		t.stats.connectionErrors++
		if wrote.Load() && !ambiguousRecorded {
			t.stats.ambiguousDeliveries++
			ambiguousRecorded = true
		}
		t.stats.mu.Unlock()
		if attempt >= t.maxRetries || req.GetBody == nil || (wrote.Load() && !t.retryAmbiguous) {
			return nil, err
		}
		// Bound reconnect pressure, and include the delay in the original deadline.
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

// A recovered response only counts after its body has been read successfully.
type recoveredBody struct {
	io.ReadCloser
	stats   *Stats
	counted bool
}

func (b *recoveredBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF && !b.counted {
		b.stats.mu.Lock()
		b.stats.recoveredRequests++
		b.stats.mu.Unlock()
		b.counted = true
	}
	return n, err
}

func (t *retryTransport) CloseIdleConnections() {
	for _, transport := range []http.RoundTripper{t.base, t.fresh} {
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}
