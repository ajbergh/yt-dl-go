package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestOutboundAddressAndRedirectGuards(t *testing.T) {
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
