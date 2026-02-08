package providers

import (
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type clientKey struct {
	timeout time.Duration
	proxy   string
}

type clientCache struct {
	mu      sync.RWMutex
	clients map[clientKey]*http.Client
}

var cache = &clientCache{
	clients: make(map[clientKey]*http.Client),
}

var dialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}

// GetClient returns an http.Client with the specified responseHeaderTimeout and proxy.
// If a client with the same timeout and proxy already exists, it returns the cached one.
// Otherwise, it creates a new client and caches it.
func GetClient(responseHeaderTimeout time.Duration, proxyURL string) *http.Client {
	key := clientKey{timeout: responseHeaderTimeout, proxy: proxyURL}

	cache.mu.RLock()
	if client, exists := cache.clients[key]; exists {
		cache.mu.RUnlock()
		return client
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Double-check after acquiring write lock
	if client, exists := cache.clients[key]; exists {
		return client
	}

	proxyFunc := http.ProxyFromEnvironment
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			proxyFunc = http.ProxyURL(u)
		}
	}

	transport := &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // No overall timeout, let ResponseHeaderTimeout control header timing
	}

	cache.clients[key] = client
	return client
}
