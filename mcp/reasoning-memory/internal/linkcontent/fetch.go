package linkcontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Fetcher interface {
	Fetch(ctx context.Context, raw string) (*FetchResult, error)
}

// Close releases resources owned by a fetcher. Implementations without resources may omit it.
type FetcherCloser interface {
	Fetcher
	Close() error
}

type verifiedConn struct {
	net.Conn
	allowed []net.IP
}

func (c *verifiedConn) Close() error { return c.Conn.Close() }

type FetchResult struct {
	StatusCode    int
	ContentType   string
	Body          []byte
	FinalURL      string
	FetchedAt     time.Time
	ContentLength int64
	Truncated     bool
}

type httpFetcher struct {
	client      *http.Client
	maxRedirect int
	maxBytes    int64
	allow       map[string]bool
	dialer      *net.Dialer
}

func NewHTTPFetcher(cfg Config, allowedContentTypes []string) Fetcher {
	allowed := make(map[string]bool, len(allowedContentTypes))
	for _, ct := range allowedContentTypes {
		if ct == "" {
			continue
		}
		allowed[strings.ToLower(strings.TrimSpace(ct))] = true
	}
	dialer := &net.Dialer{Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil {
				if !ipAllowed(ip) {
					return nil, ErrBlockedHost
				}
				return dialer.DialContext(ctx, network, addr)
			}
			resolved, err := ResolveAndValidate(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range resolved {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr != nil {
					continue
				}
				if verifyErr := VerifyConnectedHost(conn.RemoteAddr().String(), resolved); verifyErr != nil {
					_ = conn.Close()
					return nil, verifyErr
				}
				return &verifiedConn{Conn: conn, allowed: resolved}, nil
			}
			return nil, ErrUnresolvedHost
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Timeout:   time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cfg.MaxRedirects {
				return ErrTooManyRedirects
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return ErrBlockedScheme
			}
			req.Header.Del("Referer")
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return err
			}
			if ip := net.ParseIP(req.URL.Hostname()); ip != nil {
				if !ipAllowed(ip) {
					return ErrBlockedHost
				}
			} else {
				if _, err := ResolveAndValidate(req.Context(), req.URL.Hostname()); err != nil {
					return err
				}
			}
			return nil
		},
	}
	return &httpFetcher{
		client:      client,
		maxRedirect: cfg.MaxRedirects,
		maxBytes:    int64(cfg.MaxResponseBytes),
		allow:       allowed,
		dialer:      dialer,
	}
}

func (f *httpFetcher) Close() error {
	if f.client != nil {
		f.client.CloseIdleConnections()
	}
	return nil
}

func (f *httpFetcher) Fetch(ctx context.Context, raw string) (*FetchResult, error) {
	if _, err := ValidateURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "reasoning-memory/1.0 (+link-ingestion)")
	req.Header.Set("Accept", "text/html, text/plain;q=0.9")
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrContextCanceled
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrHTTPStatus
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if mediaType, _, _ := mime.ParseMediaType(ct); mediaType != "" {
		ct = mediaType
	}
	if !f.allow[strings.ToLower(ct)] {
		return nil, ErrUnsupportedContent
	}

	limited := io.LimitReader(resp.Body, f.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	truncated := int64(len(body)) > f.maxBytes
	if truncated {
		body = body[:f.maxBytes]
	}
	finalURL := raw
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return &FetchResult{
		StatusCode:    resp.StatusCode,
		ContentType:   ct,
		Body:          body,
		FinalURL:      finalURL,
		FetchedAt:     time.Now().UTC(),
		ContentLength: int64(len(body)),
		Truncated:     truncated,
	}, nil
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func NormalizeURL(raw string) (string, error) {
	u, err := ValidateURL(raw)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func SafeSourceURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	u.User = nil
	query := u.Query()
	for key := range query {
		if sensitiveQueryKey(key) {
			query[key] = []string{"[REDACTED]"}
		}
	}
	u.RawQuery = query.Encode()
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func sensitiveQueryKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
	for _, marker := range []string{"accesskey", "apikey", "authorization", "credential", "jwt", "password", "secret", "signature", "token"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
