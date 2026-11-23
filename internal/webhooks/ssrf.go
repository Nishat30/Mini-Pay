// Package webhooks implements reliable merchant event notification, HMAC signing,
// backoff retry schedules, and SSRF protection for MiniPay.
package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	ErrInvalidScheme   = errors.New("webhook URL must use https scheme")
	ErrUserInfoBlocked = errors.New("webhook URL must not contain user credentials")
	ErrPrivateIP       = errors.New("webhook destination resolves to a private or restricted IP address")
	ErrMissingHost     = errors.New("webhook URL must contain a valid hostname")
)

// Private and reserved IP blocks blocked by SSRF protection.
var blockedCIDRs []*net.IPNet

func init() {
	ranges := []string{
		// IPv4 Private & Loopback & Link-local
		"127.0.0.0/8",      // Loopback
		"10.0.0.0/8",       // RFC 1918 Private
		"172.16.0.0/12",    // RFC 1918 Private
		"192.168.0.0/16",   // RFC 1918 Private
		"169.254.0.0/16",   // Link-local / Cloud metadata (AWS, GCP, Azure)
		"100.64.0.0/10",    // Shared Address Space / CGNAT
		"0.0.0.0/8",        // Broadcast / This host
		"192.0.0.0/24",     // IETF Protocol Assignments
		"198.18.0.0/15",    // Benchmarking
		"224.0.0.0/4",      // Multicast
		"240.0.0.0/4",      // Reserved

		// IPv6 Private & Loopback & Link-local
		"::1/128",          // Loopback
		"fc00::/7",         // Unique Local Address
		"fe80::/10",        // Link-local
		"::/128",           // Unspecified
	}

	for _, r := range ranges {
		_, ipNet, err := net.ParseCIDR(r)
		if err == nil {
			blockedCIDRs = append(blockedCIDRs, ipNet)
		}
	}
}

// SecurityPolicy defines SSRF restrictions for webhook delivery.
type SecurityPolicy struct {
	AllowPrivate  bool // Allows 127.0.0.1 and RFC1918 (used only in local dev/tests)
	AllowInsecure bool // Allows http:// instead of https:// (used only in local dev/tests)
}

// IsPrivateIP checks if an IP belongs to private, loopback, or metadata networks.
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL performs initial static checks on a proposed webhook URL.
func ValidateURL(rawURL string, policy SecurityPolicy) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("malformed URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" {
		if !policy.AllowInsecure || scheme != "http" {
			return nil, ErrInvalidScheme
		}
	}

	if u.User != nil {
		return nil, ErrUserInfoBlocked
	}

	host := u.Hostname()
	if host == "" {
		return nil, ErrMissingHost
	}

	// If host is a direct IP address, validate it against blocked ranges
	if ip := net.ParseIP(host); ip != nil {
		if !policy.AllowPrivate && IsPrivateIP(ip) {
			return nil, fmt.Errorf("%w: direct IP %s is blocked", ErrPrivateIP, ip.String())
		}
	}

	return u, nil
}

// NewSafeHTTPClient constructs an http.Client whose dialer guards against DNS rebinding
// and blocks attempts to establish connections to internal IP ranges at socket creation time.
func NewSafeHTTPClient(policy SecurityPolicy, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if policy.AllowPrivate {
				return nil
			}

			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}

			ip := net.ParseIP(host)
			if ip != nil && IsPrivateIP(ip) {
				return fmt.Errorf("%w: dial to %s blocked", ErrPrivateIP, ip.String())
			}

			return nil
		},
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if !policy.AllowPrivate {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}

				// Resolve IP addresses and verify none are in prohibited CIDRs
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("no IP addresses found for host %s", host)
				}

				for _, ip := range ips {
					if IsPrivateIP(ip) {
						return nil, fmt.Errorf("%w: host %s resolves to private IP %s", ErrPrivateIP, host, ip.String())
					}
				}

				// Connect to the first validated IP
				addr = net.JoinHostPort(ips[0].String(), port)
			}

			return dialer.DialContext(ctx, network, addr)
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
