// network.go restricts outbound media requests to public HTTPS destinations,
// validates DNS results, pins connections to checked IP addresses, and checks
// redirect targets before following them.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var errOutbound = errors.New("outbound destination is not a permitted public HTTPS endpoint")
var errProxyConfig = errors.New("HTTPS_PROXY must be a valid HTTP or HTTPS proxy URL")

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

func resolvePublicAddresses(ctx context.Context, host string, lookup func(context.Context, string) ([]netip.Addr, error)) ([]netip.Addr, error) {
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		var lookupErr error
		addresses, lookupErr = lookup(ctx, host)
		if lookupErr != nil {
			return nil, errOutbound
		}
	}
	if len(addresses) == 0 {
		return nil, errOutbound
	}
	for index, ip := range addresses {
		if !publicAddress(ip) {
			return nil, errOutbound
		}
		addresses[index] = ip.Unmap()
	}
	return addresses, nil
}

func (d guardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || strings.Contains(host, "%") {
		return nil, errOutbound
	}
	addresses, err := resolvePublicAddresses(ctx, host, d.lookup)
	if err != nil {
		return nil, err
	}
	// Dial the checked IP, not the hostname: no second DNS lookup/rebinding window.
	for _, ip := range addresses {
		conn, err := d.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("public HTTPS connection could not be established")
}

type proxyTunnelDialer struct {
	proxyURL *url.URL
	lookup   func(context.Context, string) ([]netip.Addr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
	tls      *tls.Config
}

func parseHTTPSProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errProxyConfig
	}
	if port := u.Port(); port != "" {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return nil, errProxyConfig
		}
	} else if u.Scheme == "https" {
		u.Host = net.JoinHostPort(u.Hostname(), "443")
	} else {
		u.Host = net.JoinHostPort(u.Hostname(), "80")
	}
	return u, nil
}

func httpsProxyFromEnvironment() (*url.URL, error) {
	raw := os.Getenv("HTTPS_PROXY")
	if strings.TrimSpace(raw) == "" {
		raw = os.Getenv("https_proxy")
	}
	return parseHTTPSProxy(raw)
}

func (d proxyTunnelDialer) connectTarget(ctx context.Context, network string, targetIP netip.Addr) (net.Conn, error) {
	conn, err := d.dial(ctx, network, d.proxyURL.Host)
	if err != nil {
		return nil, err
	}
	if d.proxyURL.Scheme == "https" {
		proxyTLS := tls.Client(conn, &tls.Config{
			ServerName: d.proxyURL.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
		})
		if err := proxyTLS.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = proxyTLS
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	connectAddress := net.JoinHostPort(targetIP.String(), "443")
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", connectAddress, connectAddress); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if d.proxyURL.User != nil {
		username := d.proxyURL.User.Username()
		password, _ := d.proxyURL.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		if _, err := fmt.Fprintf(conn, "Proxy-Authorization: Basic %s\r\n", credentials); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if _, err := io.WriteString(conn, "\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	status := strings.Fields(statusLine)
	if len(status) < 2 || (status[0] != "HTTP/1.1" && status[0] != "HTTP/1.0") || status[1] != "200" {
		_ = conn.Close()
		return nil, errors.New("HTTPS proxy CONNECT was rejected")
	}
	if _, err := textproto.NewReader(reader).ReadMIMEHeader(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedNetConn{Conn: conn, reader: reader}, nil
}

func (d proxyTunnelDialer) DialTLSContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	targetHost, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || strings.Contains(targetHost, "%") {
		return nil, errOutbound
	}
	addresses, err := resolvePublicAddresses(ctx, targetHost, d.lookup)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: targetHost, NextProtos: []string{"h2", "http/1.1"}}
	if d.tls != nil {
		tlsConfig = d.tls.Clone()
		tlsConfig.ServerName = targetHost
	}
	tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	var lastErr error
	for _, ip := range addresses {
		tunnel, connectErr := d.connectTarget(ctx, network, ip)
		if connectErr != nil {
			lastErr = connectErr
			continue
		}
		targetTLS := tls.Client(tunnel, tlsConfig)
		if handshakeErr := targetTLS.HandshakeContext(ctx); handshakeErr != nil {
			_ = tunnel.Close()
			lastErr = handshakeErr
			continue
		}
		return targetTLS, nil
	}
	if lastErr == nil {
		lastErr = errOutbound
	}
	return nil, lastErr
}

type bufferedNetConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedNetConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}

type failedRoundTripper struct {
	err error
}

func (t failedRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
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
	lookup := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	guard := guardedDialer{
		lookup: lookup,
		dial:   dialer.DialContext,
	}
	proxyURL, proxyErr := httpsProxyFromEnvironment()
	var transport http.RoundTripper
	if proxyErr != nil {
		transport = failedRoundTripper{err: proxyErr}
	} else if proxyURL != nil {
		proxyDialer := proxyTunnelDialer{proxyURL: proxyURL, lookup: lookup, dial: dialer.DialContext, tls: &tls.Config{MinVersion: tls.VersionTLS12}}
		transport = &http.Transport{
			Proxy: nil, DialTLSContext: proxyDialer.DialTLSContext, ForceAttemptHTTP2: true,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
			ExpectContinueTimeout: time.Second, IdleConnTimeout: 90 * time.Second,
			MaxIdleConns: 8, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4,
		}
	} else {
		transport = &http.Transport{
			Proxy: nil, DialContext: guard.DialContext, ForceAttemptHTTP2: true,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
			ExpectContinueTimeout: time.Second, IdleConnTimeout: 90 * time.Second,
			MaxIdleConns: 8, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4,
		}
	}
	return &http.Client{
		Transport: &guardedTransport{base: transport}, Timeout: timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many HTTPS redirects")
			}
			return checkOutboundURL(r.URL)
		},
	}
}
