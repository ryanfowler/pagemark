package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestPublicAddressPolicy(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"100.64.0.1", false},
		{"169.254.169.254", false},
		{"172.16.0.1", false},
		{"192.168.0.1", false},
		{"::1", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"ff02::1", false},
		{"::ffff:127.0.0.1", false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			address := netip.MustParseAddr(test.address)
			if got := isPublicAddress(address); got != test.public {
				t.Fatalf("isPublicAddress(%s) = %v, want %v", address, got, test.public)
			}
		})
	}
}

func TestPublicDialerRejectsPrivateLiteral(t *testing.T) {
	dialer := &publicDialer{resolver: net.DefaultResolver}
	_, err := dialer.DialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandClientRejectsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("private server received a request")
	}))
	defer server.Close()

	_, err := newHTTPClient().Get(server.URL)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandClientDoesNotUseEnvironmentProxy(t *testing.T) {
	client := newHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("client can use an environment proxy")
	}
}
