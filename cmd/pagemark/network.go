package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

type publicDialer struct {
	resolver *net.Resolver
	dialer   net.Dialer
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&publicDialer{resolver: net.DefaultResolver}).DialContext
	return &http.Client{
		Transport:     transport,
		CheckRedirect: safeRedirect,
	}
}

func (d *publicDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("pagemark: invalid network address: %w", err)
	}
	addresses, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, address := range addresses {
		conn, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr == nil {
		dialErr = errors.New("host has no IP address")
	}
	return nil, fmt.Errorf("pagemark: connect to public host: %w", dialErr)
}

func (d *publicDialer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if !isPublicAddress(address) {
			return nil, errors.New("pagemark: URL resolves to a non-public IP address")
		}
		return []netip.Addr{address}, nil
	}
	addresses, err := d.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("pagemark: resolve host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("pagemark: host has no IP address")
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !isPublicAddress(address) {
			return nil, errors.New("pagemark: URL resolves to a non-public IP address")
		}
		result = append(result, address)
	}
	return result, nil
}

var nonPublicNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" || !address.IsGlobalUnicast() {
		return false
	}
	address = address.Unmap()
	for _, network := range nonPublicNetworks {
		if network.Contains(address) {
			return false
		}
	}
	return true
}
