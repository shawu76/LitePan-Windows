// Package proxybase 提供 Emby / 飞牛影视反代共用的只读辅助：STRM play URL 解析、URL 规范化与超时常量。
package proxybase

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// StrmPlayPathRE 匹配 LitePan STRM play URL（与 internal/strm 播放链接格式一致）。
var StrmPlayPathRE = strm.PlayPathRE

// StrmPathPlayPathRE 匹配延迟按路径解析的 LitePan STRM URL。
var StrmPathPlayPathRE = strm.PathPlayPathRE

type STRMReference = strm.PlayReference

// ParseLitePanSTRMReference 同时解析文件 ID 与路径两种 STRM 地址。
func ParseLitePanSTRMReference(value string) (STRMReference, bool) {
	return strm.ParsePlayReference(value)
}

func IsLitePanSTRMPath(value string) bool {
	pathValue := LitePanPath(value)
	return StrmPlayPathRE.MatchString(pathValue) || StrmPathPlayPathRE.MatchString(pathValue)
}

// HopByHopHeaderNames 是转发时需剥离的 hop-by-hop 头；升级请求要保留 connection/upgrade。
var HopByHopHeaderNames = map[string]struct{}{
	"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {},
	"te": {}, "trailers": {}, "transfer-encoding": {}, "upgrade": {}, "host": {},
}

// IsUpgradeRequest 判断是否为 HTTP 升级请求（WebSocket 等）。
func IsUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Upgrade")) != ""
}

// NewUpgradeProxy 构造升级请求（WebSocket）用的反向代理，靠 Go 原生 ReverseProxy 保留 Upgrade/Connection
// 并在上游返回 101 时 Hijack 做双向转发；target 需含完整路径与 query，transport 为 nil 时用默认。
func NewUpgradeProxy(target *url.URL, transport http.RoundTripper, log *slog.Logger) *httputil.ReverseProxy {
	if target == nil {
		return nil
	}
	return &httputil.ReverseProxy{
		Transport: transport,
		// -1 表示不缓冲，立即回写（升级隧道必须逐字节转发）。
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			// 直接设定上游地址：不能走 SetURL，否则会把请求路径再拼一次。
			out.URL.Scheme = target.Scheme
			out.URL.Host = target.Host
			out.URL.Path = target.Path
			out.URL.RawPath = target.RawPath
			out.URL.RawQuery = target.RawQuery
			out.Host = target.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if log != nil {
				log.Warn("反代升级请求失败", "path", r.URL.Path, "error", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// TestRequestTimeout 是反代连通性测试的上游请求超时。
const TestRequestTimeout = 20 * time.Second

// LitePanPath 从 STRM 播放地址中提取路径部分（去掉 host 与 query）。
func LitePanPath(value string) string {
	text := CleanWrappedURL(value)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		u, err := url.Parse(text)
		if err != nil {
			return ""
		}
		return u.EscapedPath()
	}
	pathOnly, _, _ := strings.Cut(text, "?")
	return pathOnly
}

// CleanWrappedURL 去掉字符串外层包裹字符（引号/反引号/空格）。
func CleanWrappedURL(value string) string {
	text := strings.TrimSpace(value)
	for {
		trimmed := strings.Trim(text, "`\"' ")
		if trimmed == text {
			return trimmed
		}
		text = strings.TrimSpace(trimmed)
	}
}

// NormalizeOptionalPort 校验并规范化可选端口号，空值返回空。
func NormalizeOptionalPort(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return "", domain.Errorf(domain.CodeValidation, "反代端口必须是 1-65535")
	}
	return strconv.Itoa(n), nil
}

// PublicBase 根据请求头与反代端口构造对外 base URL。
func PublicBase(r *http.Request, port string) string {
	if r == nil {
		if port == "" {
			return ""
		}
		return "http://127.0.0.1:" + port
	}
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if port != "" {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = net.JoinHostPort(h, port)
		} else {
			host = net.JoinHostPort(strings.Split(host, ":")[0], port)
		}
	}
	return scheme + "://" + host
}

// EmbyClientName 从媒体服务器请求头中提取客户端名称。
func EmbyClientName(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{"X-Emby-Authorization", "Authorization"} {
		raw := strings.TrimSpace(r.Header.Get(key))
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if len(part) >= len("MediaBrowser ") && strings.EqualFold(part[:len("MediaBrowser ")], "MediaBrowser ") {
				part = strings.TrimSpace(part[len("MediaBrowser "):])
			}
			if len(part) >= len("Client=") && strings.EqualFold(part[:len("Client=")], "Client=") {
				return strings.Trim(part[len("Client="):], `"' `)
			}
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Emby-Client"))
}

// NormalizeClientKeywords 规范化用分号分隔的客户端关键字列表。
func NormalizeClientKeywords(value string) string {
	seen := make(map[string]struct{})
	keywords := make([]string, 0)
	for _, keyword := range splitClientKeywords(value) {
		key := strings.ToLower(keyword)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keywords = append(keywords, keyword)
	}
	return strings.Join(keywords, ";")
}

// MatchesClientKeywords 判断请求的客户端名称或 User-Agent 是否命中关键字列表。
func MatchesClientKeywords(r *http.Request, value string) bool {
	if r == nil {
		return false
	}
	return MatchesClientText(value, EmbyClientName(r), r.UserAgent())
}

// MatchesClientText 判断客户端标识文本是否命中关键字，用于只拿得到 User-Agent 的解析钩子。
func MatchesClientText(value string, candidates ...string) bool {
	haystack := strings.ToLower(strings.Join(candidates, "\n"))
	for _, keyword := range splitClientKeywords(value) {
		if strings.Contains(haystack, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// ServeSTRMDescriptor 把原 STRM 地址作为单行文本交给播放终端。
func ServeSTRMDescriptor(w http.ResponseWriter, r *http.Request, playURL string) {
	data := []byte(strings.TrimSpace(playURL) + "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "media.strm", time.Time{}, bytes.NewReader(data))
}

func splitClientKeywords(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '；' })
	keywords := make([]string, 0, len(parts))
	for _, part := range parts {
		if keyword := strings.TrimSpace(part); keyword != "" {
			keywords = append(keywords, keyword)
		}
	}
	return keywords
}
