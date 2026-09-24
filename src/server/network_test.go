package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOutboundAddressAndRedirectGuards(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	for _, raw := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.2.3", "192.168.1.1", "169.254.169.254", "100.100.100.200",
		"0.0.0.0", "192.0.2.1", "198.18.0.1", "224.0.0.1", "255.255.255.255",
		"::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2002:a00:1::", "2001:db8::1",
	} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("non-public destination accepted: %s", raw)
		}
	}
	for _, raw := range []string{"142.250.190.14", "74.125.1.1", "2607:f8b0:4005:80a::200e"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("public Google address blocked: %s", raw)
		}
	}
	client := nativeHTTPClient(time.Minute)
	for _, raw := range []string{"http://youtube.com", "https://127.0.0.1/", "https://[::1]/", "https://metadata.google.internal/", "https://localhost/", "https://public.example:8080/", "https://user@googlevideo.com/"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CheckRedirect(&http.Request{URL: u}, nil); err == nil {
			t.Errorf("unsafe redirect accepted: %s", raw)
		}
	}
	for _, raw := range []string{"https://www.youtube.com/watch", "https://youtubei.googleapis.com/youtubei/v1/player", "https://rr1---sn-example.googlevideo.com/videoplayback?signature=fixture"} {
		u, _ := url.Parse(raw)
		if err := checkOutboundURL(u); err != nil {
			t.Errorf("public library endpoint rejected: %s", raw)
		}
	}
	transport := client.Transport.(*guardedTransport).base.(*http.Transport)
	if transport.Proxy != nil || transport.ResponseHeaderTimeout <= 0 || transport.TLSHandshakeTimeout <= 0 {
		t.Fatal("proxy or timeout policy is unsafe")
	}
}

func TestParseHTTPSProxy(t *testing.T) {
	for _, raw := range []string{"ftp://proxy.example:8080", "http://proxy.example:70000", "http://proxy.example/path", "http://proxy.example/?token=x", "http://"} {
		if _, err := parseHTTPSProxy(raw); !errors.Is(err, errProxyConfig) {
			t.Errorf("parseHTTPSProxy(%q) error = %v, want errProxyConfig", raw, err)
		}
	}
	for raw, want := range map[string]string{
		"http://proxy.example":             "proxy.example:80",
		"https://proxy.example":            "proxy.example:443",
		"http://proxy.example:3128":        "proxy.example:3128",
		"http://user:secret@proxy.example": "proxy.example:80",
	} {
		parsed, err := parseHTTPSProxy(raw)
		if err != nil {
			t.Errorf("parseHTTPSProxy(%q): %v", raw, err)
			continue
		}
		if parsed.Host != want {
			t.Errorf("proxy host = %q, want %q", parsed.Host, want)
		}
	}
}

func TestHTTPSProxyEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "http://lowercase-proxy.example:3128")
	proxyURL, err := httpsProxyFromEnvironment()
	if err != nil || proxyURL == nil || proxyURL.Host != "lowercase-proxy.example:3128" {
		t.Fatalf("lowercase HTTPS proxy not read: proxy=%v err=%v", proxyURL, err)
	}
	t.Setenv("HTTPS_PROXY", "http://upper-proxy.example:8080")
	proxyURL, err = httpsProxyFromEnvironment()
	if err != nil || proxyURL == nil || proxyURL.Host != "upper-proxy.example:8080" {
		t.Fatalf("uppercase HTTPS proxy should take precedence: proxy=%v err=%v", proxyURL, err)
	}
	t.Setenv("HTTPS_PROXY", "invalid")
	if _, err := httpsProxyFromEnvironment(); !errors.Is(err, errProxyConfig) {
		t.Fatalf("invalid proxy should fail closed, got %v", err)
	}
}

func TestProxyConnectUsesCheckedTargetIP(t *testing.T) {
	proxyURL, err := parseHTTPSProxy("http://user:secret@proxy.example:3128")
	if err != nil {
		t.Fatal(err)
	}
	clientConn, proxyConn := net.Pipe()
	dialCalls := 0
	dialer := proxyTunnelDialer{
		proxyURL: proxyURL,
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("142.250.190.14")}, nil
		},
		dial: func(_ context.Context, network, address string) (net.Conn, error) {
			dialCalls++
			if network != "tcp" || address != "proxy.example:3128" {
				t.Errorf("proxy dial = %s %s", network, address)
			}
			return clientConn, nil
		},
	}
	type request struct {
		line    string
		headers textproto.MIMEHeader
	}
	gotRequest := make(chan request, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		reader := bufio.NewReader(proxyConn)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return
		}
		headers, readErr := textproto.NewReader(reader).ReadMIMEHeader()
		if readErr != nil {
			return
		}
		gotRequest <- request{line: strings.TrimSpace(line), headers: headers}
		_, _ = io.WriteString(proxyConn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	}()

	tunnel, err := dialer.connectTarget(context.Background(), "tcp", netip.MustParseAddr("142.250.190.14"))
	if err != nil {
		t.Fatal(err)
	}
	_ = tunnel.Close()
	<-serverDone
	if dialCalls != 1 {
		t.Fatalf("proxy dial calls = %d, want 1", dialCalls)
	}
	req := <-gotRequest
	if req.line != "CONNECT 142.250.190.14:443 HTTP/1.1" {
		t.Fatalf("CONNECT line = %q", req.line)
	}
	if got := req.headers.Get("Proxy-Authorization"); got != "Basic dXNlcjpzZWNyZXQ=" {
		t.Fatalf("proxy authorization header = %q", got)
	}
}

func TestProxyRejectsMixedTargetDNSBeforeConnect(t *testing.T) {
	proxyURL, err := parseHTTPSProxy("http://proxy.example:3128")
	if err != nil {
		t.Fatal(err)
	}
	dialCalls := 0
	dialer := proxyTunnelDialer{
		proxyURL: proxyURL,
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("142.250.190.14"), netip.MustParseAddr("169.254.169.254")}, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("proxy must not be contacted")
		},
	}
	_, err = dialer.DialTLSContext(context.Background(), "tcp", "media.example:443")
	if !errors.Is(err, errOutbound) || dialCalls != 0 {
		t.Fatalf("mixed target DNS reached proxy: err=%v dialCalls=%d", err, dialCalls)
	}
}

func TestProxyRequestUsesTargetHostnameForTLS(t *testing.T) {
	serverName := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverName <- r.TLS.ServerName
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	proxyURL, err := parseHTTPSProxy("http://proxy.example:3128")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	proxyDialer := proxyTunnelDialer{
		proxyURL: proxyURL,
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("142.250.190.14")}, nil
		},
		dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			clientConn, proxyConn := net.Pipe()
			go func() {
				defer proxyConn.Close()
				reader := bufio.NewReader(proxyConn)
				if _, readErr := reader.ReadString('\n'); readErr != nil {
					return
				}
				if _, readErr := textproto.NewReader(reader).ReadMIMEHeader(); readErr != nil {
					return
				}
				if _, writeErr := io.WriteString(proxyConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); writeErr != nil {
					return
				}
				upstream, dialErr := (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				if dialErr != nil {
					return
				}
				defer upstream.Close()
				copyDone := make(chan struct{}, 2)
				go func() {
					_, _ = io.Copy(upstream, proxyConn)
					copyDone <- struct{}{}
				}()
				go func() {
					_, _ = io.Copy(proxyConn, upstream)
					copyDone <- struct{}{}
				}()
				<-copyDone
			}()
			return clientConn, nil
		},
		tls: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	transport := &http.Transport{DialTLSContext: proxyDialer.DialTLSContext, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("https://example.com/resource")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("response status = %d", response.StatusCode)
	}
	if got := <-serverName; got != "example.com" {
		t.Fatalf("TLS SNI = %q, want original target hostname", got)
	}
}

func TestDNSIsValidatedAndPinned(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "mixed-private"}[private], func(t *testing.T) {
			lookups, dials := 0, 0
			guard := guardedDialer{
				lookup: func(context.Context, string) ([]netip.Addr, error) {
					lookups++
					result := []netip.Addr{netip.MustParseAddr("142.250.190.14")}
					if private {
						result = append(result, netip.MustParseAddr("169.254.169.254"))
					}
					return result, nil
				},
				dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
					dials++
					if address != "142.250.190.14:443" {
						t.Errorf("dial was not pinned to the checked public IP: %s", address)
					}
					client, peer := net.Pipe()
					_ = peer.Close()
					return client, nil
				},
			}
			conn, err := guard.DialContext(context.Background(), "tcp", "cdn.googlevideo.com:443")
			if private {
				if !errors.Is(err, errOutbound) || dials != 0 {
					t.Fatal("mixed DNS response reached a connection")
				}
			} else {
				if err != nil || dials != 1 || lookups != 1 {
					t.Fatal("public address was re-resolved or could not be dialed")
				}
				_ = conn.Close()
			}
		})
	}
}
