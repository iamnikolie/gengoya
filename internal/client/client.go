package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Client is a shared HTTP client with long timeout (image gen is slow),
// 429/5xx exponential backoff, and optional verbose dumps with key redaction.
type Client struct {
	http      *http.Client
	Verbose   bool
	RetryWait time.Duration
}

// New returns a Client with a ~300s timeout (image gen can be slow).
func New() *Client {
	return &Client{
		http:      &http.Client{Timeout: 300 * time.Second},
		RetryWait: time.Second,
	}
}

var (
	bearerRE = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)(\S+)`)
	apiKeyRE = regexp.MustCompile(`(?i)(x-goog-api-key:\s*)(\S+)`)
	skRE     = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{8,})\b`)
	aizaRE   = regexp.MustCompile(`\b(AIza[A-Za-z0-9_-]{8,})\b`)
)

// Redact masks API keys in verbose log text.
func Redact(s string) string {
	s = bearerRE.ReplaceAllString(s, `${1}***`)
	s = apiKeyRE.ReplaceAllString(s, `${1}***`)
	s = skRE.ReplaceAllString(s, "sk-***")
	s = aizaRE.ReplaceAllString(s, "AIza***")
	return s
}

// Do performs method against fullURL with optional body and headers.
// Retries on HTTP 429 and 5xx up to 3 times with exponential backoff.
func (c *Client) Do(ctx context.Context, method, fullURL string, body []byte, headers map[string]string) ([]byte, int, error) {
	b, status, _, err := c.do(ctx, method, fullURL, body, headers, false, true)
	return b, status, err
}

// DoOnce is Do for a billed, non-idempotent call (image generation or edit, a
// video start, a blocking video generation): it retries 429 only. A 5xx may
// arrive after the server already did and billed the work, and a retry would
// pay for it twice. Do (with 5xx retries) is for reads.
func (c *Client) DoOnce(ctx context.Context, method, fullURL string, body []byte, headers map[string]string) ([]byte, int, error) {
	b, status, _, err := c.do(ctx, method, fullURL, body, headers, false, false)
	return b, status, err
}

// DoBinary is like Do but for binary payloads (generated video): the response
// body is never dumped in verbose mode, and the response Content-Type is
// returned so the caller can pick a file extension.
func (c *Client) DoBinary(ctx context.Context, method, fullURL string, headers map[string]string) ([]byte, string, error) {
	b, _, contentType, err := c.do(ctx, method, fullURL, nil, headers, true, true)
	return b, contentType, err
}

func (c *Client) do(ctx context.Context, method, fullURL string, body []byte, headers map[string]string, binary, retry5xx bool) ([]byte, int, string, error) {
	const maxRetries = 3
	wait := c.RetryWait
	if wait <= 0 {
		wait = time.Second
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var br io.Reader
		if body != nil {
			br = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, br)
		if err != nil {
			return nil, 0, "", err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		if c.Verbose {
			fmt.Fprintf(os.Stderr, "→ %s %s\n", method, fullURL)
			for k, v := range headers {
				fmt.Fprintln(os.Stderr, Redact(k+": "+v))
			}
			if body != nil {
				fmt.Fprintf(os.Stderr, "%s\n", Redact(string(body)))
			}
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, 0, "", err
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		contentType := resp.Header.Get("Content-Type")

		if c.Verbose {
			if binary && resp.StatusCode < 400 {
				fmt.Fprintf(os.Stderr, "← HTTP %d %s (%d bytes, body omitted)\n", resp.StatusCode, contentType, len(b))
			} else {
				fmt.Fprintf(os.Stderr, "← HTTP %d\n%s\n", resp.StatusCode, Redact(string(b)))
			}
		}

		retryable := resp.StatusCode == http.StatusTooManyRequests || (retry5xx && resp.StatusCode >= 500)
		if retryable {
			if attempt == maxRetries {
				return b, resp.StatusCode, contentType, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
			}
			select {
			case <-ctx.Done():
				return nil, 0, "", ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
			continue
		}
		if resp.StatusCode >= 400 {
			return b, resp.StatusCode, contentType, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		}
		return b, resp.StatusCode, contentType, nil
	}
	return nil, 0, "", fmt.Errorf("unreachable")
}

// DoMultipart is like DoOnce (multipart uploads here are billed image edits)
// but sends a multipart body with a pre-set Content-Type (including boundary).
// Body is consumed as-is.
func (c *Client) DoMultipart(ctx context.Context, method, fullURL string, body []byte, contentType string, headers map[string]string) ([]byte, int, error) {
	h := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		h[k] = v
	}
	h["Content-Type"] = contentType
	return c.DoOnce(ctx, method, fullURL, body, h)
}
