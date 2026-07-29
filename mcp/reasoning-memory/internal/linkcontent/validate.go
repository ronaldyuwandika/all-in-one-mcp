package linkcontent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrBlockedScheme      = errors.New("link ingestion: scheme not http(s)")
	ErrCredentialsInURL   = errors.New("link ingestion: url must not include credentials")
	ErrBlockedHost        = errors.New("link ingestion: host is loopback, private, link-local, multicast, or metadata")
	ErrInvalidHost        = errors.New("link ingestion: invalid host")
	ErrUnresolvedHost     = errors.New("link ingestion: host could not be resolved")
	ErrHostMismatch       = errors.New("link ingestion: connected host does not match resolved address")
	ErrTooManyRedirects   = errors.New("link ingestion: too many redirects")
	ErrContextCanceled    = errors.New("link ingestion: context cancelled")
	ErrUnsupportedContent = errors.New("link ingestion: unsupported content type")
	ErrHTTPStatus         = errors.New("link ingestion: non-success HTTP status")
)

func ValidateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidHost, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrBlockedScheme
	}
	if u.User != nil {
		return nil, ErrCredentialsInURL
	}
	host := u.Hostname()
	if host == "" {
		return nil, ErrInvalidHost
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return nil, ErrInvalidHost
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ipAllowed(ip) {
			return nil, ErrBlockedHost
		}
	}
	return u, nil
}

func ipAllowed(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return false
		}
	}
	return true
}

func ResolveAndValidate(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := (&net.Resolver{}).LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, ErrUnresolvedHost
	}
	filtered := make([]net.IP, 0, len(ips))
	for _, ipa := range ips {
		if ipAllowed(ipa.IP) {
			filtered = append(filtered, ipa.IP)
		}
	}
	if len(filtered) == 0 {
		return nil, ErrBlockedHost
	}
	return filtered, nil
}

func VerifyConnectedHost(rawAddr string, allowed []net.IP) error {
	host, _, err := net.SplitHostPort(rawAddr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidHost, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ErrHostMismatch
	}
	for _, allowedIP := range allowed {
		if ip.Equal(allowedIP) {
			return nil
		}
	}
	return ErrHostMismatch
}
