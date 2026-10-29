package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
)

const lastKnownGoodSetting = "network.last_known_good_ips"

type Settings interface {
	Setting(context.Context, string) (string, bool, error)
	SetSetting(context.Context, string, string) error
}

type lookupFunc func(context.Context, string) ([]net.IPAddr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

type ipCache struct {
	mu       sync.RWMutex
	values   map[string]string
	settings Settings
}

func loadIPCache(ctx context.Context, settings Settings) (*ipCache, error) {
	c := &ipCache{values: map[string]string{}, settings: settings}
	if settings == nil {
		return c, nil
	}
	raw, ok, err := settings.Setting(ctx, lastKnownGoodSetting)
	if err != nil {
		return nil, err
	}
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.values); err != nil {
			return nil, fmt.Errorf("decode %s: %w", lastKnownGoodSetting, err)
		}
	}
	return c, nil
}

func (c *ipCache) IP(host string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.values[host]
}

func (c *ipCache) remember(ctx context.Context, host, ip string) error {
	c.mu.Lock()
	if c.values[host] == ip {
		c.mu.Unlock()
		return nil
	}
	c.values[host] = ip
	raw, err := json.Marshal(c.values)
	if err == nil && c.settings != nil {
		err = c.settings.SetSetting(context.WithoutCancel(ctx), lastKnownGoodSetting, string(raw))
	}
	c.mu.Unlock()
	return err
}

type fallbackDialer struct {
	lookups []lookupFunc
	cache   *ipCache
	dialIP  dialFunc
}

func newFallbackDialer(lookups []lookupFunc, cache *ipCache, dialIP dialFunc) *fallbackDialer {
	return &fallbackDialer{lookups: lookups, cache: cache, dialIP: dialIP}
}

func (d *fallbackDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return d.dialIP(ctx, network, address)
	}
	var failures []error
	tried := map[string]bool{}
	for _, lookup := range d.lookups {
		ips, lookupErr := lookup(ctx, host)
		if lookupErr != nil {
			failures = append(failures, lookupErr)
			continue
		}
		for _, addr := range ips {
			ip := addr.IP.String()
			if ip == "" || tried[ip] {
				continue
			}
			tried[ip] = true
			conn, dialErr := d.dialIP(ctx, network, net.JoinHostPort(ip, port))
			if dialErr == nil {
				// A settings write must never turn a successful network connection
				// into an outage; the in-memory cache is already updated.
				_ = d.cache.remember(ctx, host, ip)
				return conn, nil
			}
			failures = append(failures, dialErr)
		}
	}
	if ip := d.cache.IP(host); ip != "" && !tried[ip] {
		conn, dialErr := d.dialIP(ctx, network, net.JoinHostPort(ip, port))
		if dialErr == nil {
			return conn, nil
		}
		failures = append(failures, dialErr)
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	return nil, fmt.Errorf("dial %s: %w", host, errors.Join(failures...))
}

// NewHTTPClient returns a client whose transport resolves with the system DNS,
// then each configured fallback resolver, then the persisted last-known-good IP.
func NewHTTPClient(ctx context.Context, fallbackDNS []string, settings Settings) (*http.Client, error) {
	cache, err := loadIPCache(ctx, settings)
	if err != nil {
		return nil, err
	}
	lookups := []lookupFunc{net.DefaultResolver.LookupIPAddr}
	for _, server := range fallbackDNS {
		server := server
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server)
		}}
		lookups = append(lookups, resolver.LookupIPAddr)
	}
	base := &net.Dialer{}
	dialer := newFallbackDialer(lookups, cache, base.DialContext)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport}, nil
}
