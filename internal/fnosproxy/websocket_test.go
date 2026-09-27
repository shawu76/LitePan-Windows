package fnosproxy

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"litepan/internal/settings"
	"litepan/internal/store"
)

func testFnosProxyService(t *testing.T, fnosURL string) *Service {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settingsSvc, err := settings.New(ctx, store.New(db).Configs)
	if err != nil {
		t.Fatal(err)
	}
	if err := settingsSvc.Update(ctx, map[string]string{
		settings.KeyFnosEnabled:   "true",
		settings.KeyFnosURL:       fnosURL,
		settings.KeyFnosProxyPort: "18997",
	}); err != nil {
		t.Fatal(err)
	}
	return New(Options{Settings: settingsSvc})
}

// 飞牛客户端同样会用 WebSocket 做实时通道，反代需要完成 101 升级。
func TestHandleTunnelsWebSocketUpgrade(t *testing.T) {
	var mu sync.Mutex
	var upstreamPath, upstreamQuery string
	upstreamUpgrade := ""
	hits := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		upstreamPath = r.URL.Path
		upstreamQuery = r.URL.RawQuery
		upstreamUpgrade = r.Header.Get("Upgrade")
		mu.Unlock()

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: test-accept\r\n\r\n")
		_ = buf.Flush()
		for {
			line, err := buf.ReadString('\n')
			if err != nil {
				return
			}
			if _, err := buf.WriteString("echo:" + line); err != nil {
				return
			}
			_ = buf.Flush()
		}
	}))
	defer upstream.Close()

	svc := testFnosProxyService(t, upstream.URL)
	proxy := httptest.NewServer(http.HandlerFunc(svc.handle))
	defer proxy.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatalf("连接反代失败: %v", err)
	}
	defer conn.Close()
	handshake := "GET /socket?api_key=test-key&deviceId=kodi HTTP/1.1\r\n" +
		"Host: litepan.test:18997\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(handshake)); err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("读取状态行失败: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("状态行=%q，期望 101", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取响应头失败: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

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

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("上游被访问 %d 次", hits)
	}
	if upstreamPath != "/socket" || upstreamQuery != "api_key=test-key&deviceId=kodi" {
		t.Fatalf("上游路径=%q query=%q", upstreamPath, upstreamQuery)
	}
	if !strings.EqualFold(upstreamUpgrade, "websocket") {
		t.Fatalf("上游 Upgrade=%q，应保留升级头", upstreamUpgrade)
	}
}
