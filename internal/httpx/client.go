package httpx

import (
	"net/http"
	"net/url"
	"time"
)

const (
	DefaultTimeout         = 30 * time.Second
	defaultIdleConnTimeout = 90 * time.Second
)

type ClientOptions struct {
	Timeout            time.Duration
	IdleConnTimeout    time.Duration
	DisableCompression bool
	DisableKeepAlives  bool
	Proxy              func(*http.Request) (*url.URL, error)
}

func NewClient(opts ClientOptions) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	idle := opts.IdleConnTimeout
	if idle <= 0 && !opts.DisableKeepAlives {
		idle = defaultIdleConnTimeout
	}
	if idle > 0 {
		tr.IdleConnTimeout = idle
	}
	if opts.DisableCompression {
		tr.DisableCompression = true
	}
	if opts.DisableKeepAlives {
		tr.DisableKeepAlives = true
		tr.MaxIdleConnsPerHost = 0
	}
	if opts.Proxy != nil {
		tr.Proxy = opts.Proxy
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

// NewStreamingClient 复用普通客户端的连接配置，但不限制整段文件传输时长。
func NewStreamingClient(base *http.Client, responseHeaderTimeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if base != nil {
		if baseTransport, ok := base.Transport.(*http.Transport); ok {
			tr = baseTransport.Clone()
		}
	}
	if responseHeaderTimeout > 0 {
		tr.ResponseHeaderTimeout = responseHeaderTimeout
	}
	return &http.Client{Transport: tr}
}

func CloseClient(c *http.Client) {
	if c == nil {
		return
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		return
	}
	tr.CloseIdleConnections()
}
