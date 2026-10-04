package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedact(t *testing.T) {
	in := "Authorization: Bearer sk-abcdefghijklmnop\nx-goog-api-key: AIzaSyTestKeyValue"
	out := Redact(in)
	assert.NotContains(t, out, "sk-abcdefghijklmnop")
	assert.NotContains(t, out, "AIzaSyTestKeyValue")
	assert.Contains(t, out, "***")
}

func TestRetryOn429(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New()
	c.RetryWait = time.Millisecond
	b, code, err := c.Do(context.Background(), http.MethodGet, srv.URL, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Contains(t, string(b), "ok")
	assert.Equal(t, int32(3), n.Load())
}

func TestRetryOn5xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	c := New()
	c.RetryWait = time.Millisecond
	b, code, err := c.Do(context.Background(), http.MethodPost, srv.URL, []byte(`{}`), map[string]string{
		"Content-Type": "application/json",
	})
	require.NoError(t, err)
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", string(b))
}

func TestNoRetryOn400(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	c := New()
	c.RetryWait = time.Millisecond
	_, code, err := c.Do(context.Background(), http.MethodGet, srv.URL, nil, nil)
	require.Error(t, err)
	assert.Equal(t, 400, code)
	assert.Equal(t, int32(1), n.Load())
}

func TestRedactHeaderLine(t *testing.T) {
	// Header values must be redacted together with their name: a bare value
	// like a new-style "AQ." Gemini key matches no value-only pattern.
	assert.Equal(t, "x-goog-api-key: ***", Redact("x-goog-api-key: AQ.Ab8RN6secretsecret"))
	assert.Equal(t, "Authorization: Bearer ***", Redact("Authorization: Bearer abc.def"))
}

func TestDoOnceNoRetryOn5xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	c := New()
	c.RetryWait = time.Millisecond
	_, code, err := c.DoOnce(context.Background(), http.MethodPost, srv.URL, []byte("{}"), nil)
	require.Error(t, err)
	assert.Equal(t, 504, code)
	assert.Equal(t, int32(1), n.Load(), "a billed generation must not be re-posted")
}

func TestDoOnceRetriesOn429(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New()
	c.RetryWait = time.Millisecond
	_, code, err := c.DoOnce(context.Background(), http.MethodPost, srv.URL, []byte("{}"), nil)
	require.NoError(t, err)
	assert.Equal(t, 200, code)
}
