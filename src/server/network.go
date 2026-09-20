// network.go restricts outbound media requests to public HTTPS destinations,
// validates DNS results, pins connections to checked IP addresses, and checks
// redirect targets before following them.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var errOutbound = errors.New("Outbound destination is not a permitted public HTTPS endpoint")

var reservedNetworks = func() []netip.Prefix {
	var result []netip.Prefix
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/32", "2001:2::/48", "2001:10::/28", "2001:20::/28",
		"2001:db8::/32", "2002::/16", "fec0::/10",
	} {
		result = append(result, netip.MustParsePrefix(cidr))
	}
	return result
}()

// publicAddress reports whether ip is globally routable rather than private,
// reserved, loopback, or link-local.
func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range reservedNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// checkOutboundURL validates the HTTPS URL shape and rejects literal private,
// reserved, or local hostnames. DNS-resolved addresses are checked by the dialer.
func checkOutboundURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return errOutbound
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.Contains(host, "%") {
		return errOutbound
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicAddress(ip) {
			return errOutbound
		}
		return nil
	}
	if !strings.Contains(host, ".") {
		return errOutbound
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return errOutbound
		}
	}
	return nil
}

type guardedDialer struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func (d guardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || strings.Contains(host, "%") {
		return nil, errOutbound
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = d.lookup(ctx, host)
		if err != nil {
			return nil, errOutbound
		}
	}
	if len(addresses) == 0 {
		return nil, errOutbound
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return nil, errOutbound
		}
	}
	// Dial the checked IP, not the hostname: no second DNS lookup/rebinding window.
	for _, ip := range addresses {
		conn, err := d.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("Public HTTPS connection could not be established")
}

type guardedTransport struct {
	base http.RoundTripper
}

func (t *guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := checkOutboundURL(r.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(r)
}

// nativeHTTPClient builds a bounded client whose dialer, redirects, and
// transport all enforce the outbound URL and address checks above.
func nativeHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	guard := guardedDialer{
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dial: dialer.DialContext,
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: guard.DialContext, ForceAttemptHTTP2: true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: time.Second, IdleConnTimeout: 90 * time.Second,
		MaxIdleConns: 8, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4,
	}
	return &http.Client{
		Transport: &guardedTransport{base: transport}, Timeout: timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("Too many HTTPS redirects")
			}
			return checkOutboundURL(r.URL)
		},
	}
}
