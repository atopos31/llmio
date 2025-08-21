package providers

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type clientCache struct {
	mu      sync.RWMutex
	clients map[time.Duration]*http.Client
}

var cache = &clientCache{
	clients: make(map[time.Duration]*http.Client),
}

var dialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}

// GetClient returns an http.Client with the specified responseHeaderTimeout.
// If a client with the same timeout already exists, it returns the cached one.
// Otherwise, it creates a new client and caches it.
func GetClient(responseHeaderTimeout time.Duration) *http.Client {
	cache.mu.RLock()
	if client, exists := cache.clients[responseHeaderTimeout]; exists {
		cache.mu.RUnlock()
		return client
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Double-check after acquiring write lock
	if client, exists := cache.clients[responseHeaderTimeout]; exists {
		return client
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
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

	cache.clients[responseHeaderTimeout] = client
	return client
}
