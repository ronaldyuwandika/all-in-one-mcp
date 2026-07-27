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
			dialHost := resolved[0].String()
			return dialer.DialContext(ctx, network, net.JoinHostPort(dialHost, port))
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
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Fragment = ""
	if u.Scheme == "" || u.Host == "" {
		return "", ErrInvalidHost
	}
	return u.String(), nil
}

func SafeSourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
