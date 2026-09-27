package proxybase

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 上游为 HTTPS（且支持 HTTP/2）时，WebSocket 隧道同样必须可用：
// 生产环境的 EmbyURL 常是 https，Transport 又是 DefaultTransport.Clone()（ForceAttemptHTTP2=true）。
func TestUpgradeProxyTunnelsWebSocketOverHTTPSUpstream(t *testing.T) {
	probe := &upgradeProbe{}
	upstream := httptest.NewUnstartedServer(upgradeEchoHandler(probe))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()

	target, err := url.Parse(upstream.URL + "/embywebsocket?api_key=***&deviceId=kodi")
	if err != nil {
		t.Fatal(err)
	}
	if target.Scheme != "https" {
		t.Fatalf("上游 scheme=%q，期望 https", target.Scheme)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NewUpgradeProxy(target, transport, nil).ServeHTTP(w, r)
	}))
	defer proxy.Close()

	conn, br, status, _ := wsHandshake(t, proxy.URL, "/embywebsocket?api_key=***&deviceId=kodi")
	defer conn.Close()

	if !strings.Contains(status, "101") {
		t.Fatalf("状态行=%q，期望 101（HTTPS 上游升级失败）", status)
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
	_, _, _, hits := probe.snapshot()
	if hits != 1 {
		t.Fatalf("上游被访问 %d 次", hits)
	}
}

// upgradeEchoHandler 返回一个模拟 WebSocket 端点的处理器：101 握手后回显每一行。
func upgradeEchoHandler(probe *upgradeProbe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if probe != nil {
			probe.record(r)
		}
		if !IsUpgradeRequest(r) {
			http.Error(w, "not an upgrade", http.StatusBadRequest)
			return
		}
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
		accept := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Accept"))
		if accept == "" {
			accept = "test-accept"
		}
		if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"); err != nil {
			return
		}
		_ = buf.Flush()
		for {
			line, err := buf.ReadString('\n')
			if err != nil {
				return
			}
			if _, err := buf.WriteString("echo:" + line); err != nil {
				return
			}
			if err := buf.Flush(); err != nil {
				return
			}
		}
	}
}
