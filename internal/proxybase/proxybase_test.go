package proxybase

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestIsUpgradeRequest(t *testing.T) {
	if IsUpgradeRequest(nil) {
		t.Fatal("nil 不应判定为升级请求")
	}
	req := httptest.NewRequest(http.MethodGet, "/embywebsocket", nil)
	if IsUpgradeRequest(req) {
		t.Fatal("无 Upgrade 头不应判定为升级请求")
	}
	req.Header.Set("Upgrade", "websocket")
	if !IsUpgradeRequest(req) {
		t.Fatal("带 Upgrade 头应判定为升级请求")
	}
}

type upgradeProbe struct {
	mu     sync.Mutex
	path   string
	query  string
	header http.Header
	hits   int
}

func (p *upgradeProbe) record(r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hits++
	p.path = r.URL.Path
	p.query = r.URL.RawQuery
	p.header = r.Header.Clone()
}

func (p *upgradeProbe) snapshot() (path, query string, header http.Header, hits int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.path, p.query, p.header, p.hits
}

// newUpgradeEchoUpstream 模拟带 WebSocket 端点的上游。
func newUpgradeEchoUpstream(t *testing.T, probe *upgradeProbe) *httptest.Server {
	t.Helper()
	return httptest.NewServer(upgradeEchoHandler(probe))
}

// wsHandshake 以裸 TCP 完成一次 WebSocket 握手，返回连接与响应头/状态行。
func wsHandshake(t *testing.T, serverURL, path string) (net.Conn, *bufio.Reader, string, []string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(serverURL, "http://"))
	if err != nil {
		t.Fatalf("连接代理失败: %v", err)
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: litepan.test\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"X-Emby-Token: test-key\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	var headers []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取响应头失败: %v", err)
		}
		headers = append(headers, strings.TrimRight(line, "\r\n"))
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return conn, br, strings.TrimSpace(status), headers
}

func TestUpgradeProxyTunnelsWebSocket(t *testing.T) {
	probe := &upgradeProbe{}
	upstream := newUpgradeEchoUpstream(t, probe)
	defer upstream.Close()

	target, err := url.Parse(upstream.URL + "/embywebsocket?api_key=k&deviceId=kodi")
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewUpgradeProxy(target, nil, nil).ServeHTTP(w, r)
	}))
	defer proxy.Close()

	conn, br, status, headers := wsHandshake(t, proxy.URL, "/embywebsocket?api_key=k&deviceId=kodi")
	defer conn.Close()

	if !strings.Contains(status, "101") {
		t.Fatalf("状态行=%q，期望 101", status)
	}
	upgradeHeader := ""
	for _, h := range headers {
		if strings.HasPrefix(strings.ToLower(h), "upgrade:") {
			upgradeHeader = h
		}
	}
	if !strings.Contains(strings.ToLower(upgradeHeader), "websocket") {
		t.Fatalf("缺少 Upgrade 响应头: %v", headers)
	}

	// 双向通道可用：写一行能拿回回显。
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("写入隧道失败: %v", err)
	}
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("读取隧道回显失败: %v", err)
	}
	if line != "echo:ping\n" {
		t.Fatalf("回显=%q，希望 echo:ping", line)
	}

	path, query, header, hits := probe.snapshot()
	if hits != 1 {
		t.Fatalf("上游被访问 %d 次", hits)
	}
	if path != "/embywebsocket" || query != "api_key=k&deviceId=kodi" {
		t.Fatalf("上游路径=%q query=%q", path, query)
	}
	if got := header.Get("Upgrade"); !strings.EqualFold(got, "websocket") {
		t.Fatalf("上游收到的 Upgrade=%q（应在隧道中保留）", got)
	}
	if got := header.Get("X-Emby-Token"); got != "test-key" {
		t.Fatalf("认证头未透传: %q", got)
	}
}

func TestUpgradeProxyRejectsNilTarget(t *testing.T) {
	if NewUpgradeProxy(nil, nil, nil) != nil {
		t.Fatal("目标为空时应返回 nil")
	}
}
