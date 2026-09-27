package fnosproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"litepan/internal/playback"
	"litepan/internal/proxybase"
	"litepan/internal/strm"
)

func TestUpdateRequestAcceptsNumericPort(t *testing.T) {
	var in UpdateRequest
	if err := json.Unmarshal([]byte(`{"proxy_port":18997}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Port.String() != "18997" {
		t.Fatalf("端口=%q", in.Port.String())
	}
}

func TestServeLitePanPlaybackResolvesPathReference(t *testing.T) {
	var got playback.Request
	svc := &Service{
		resolvePath: func(_ context.Context, accountID int64, rootID, relativePath string) (string, error) {
			if accountID != 12 || rootID != "root-id" || relativePath != "电影/测试.mkv" {
				t.Fatalf("路径参数不符: account=%d root=%q path=%q", accountID, rootID, relativePath)
			}
			return "file-id", nil
		},
		servePlayback: func(_ http.ResponseWriter, _ *http.Request, req playback.Request, _ playback.Intent) error {
			got = req
			return nil
		},
	}
	playURL := strm.BuildPathPlayURL("http://litepan.test", 12, "root-id", "电影/测试.mkv", "测试.mkv", "token", false, nil)
	if !svc.serveLitePanPlayback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), playURL) {
		t.Fatal("路径型 STRM 未被飞牛反代识别")
	}
	if got.AccountID != 12 || got.FileID != "file-id" {
		t.Fatalf("播放参数=%#v", got)
	}
}

func TestEmbyClientNameRecognizesMediaBrowserPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/Items/demo/PlaybackInfo", nil)
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="Mac", Token="secret"`)
	if got := proxybase.EmbyClientName(req); got != "Infuse" {
		t.Fatalf("客户端名=%q，期望 Infuse", got)
	}
}

// 部分客户端要求媒体流必填字段均为非 null 字符串。
func TestNormalizeEmbyMediaStreams(t *testing.T) {
	tests := []struct {
		name        string
		stream      map[string]any
		wantChanged bool
	}{
		{
			name:        "缺 Title 与 DisplayLanguage",
			stream:      map[string]any{"Type": "Audio", "Language": "chi", "DisplayTitle": "[Mandarin]"},
			wantChanged: true,
		},
		{
			name:        "字段显式为 null",
			stream:      map[string]any{"Type": "Video", "Language": nil, "DisplayLanguage": nil, "Title": nil, "DisplayTitle": nil},
			wantChanged: true,
		},
		{
			name:        "字段齐全无需修改",
			stream:      map[string]any{"Type": "Video", "Language": "", "DisplayLanguage": "", "Title": "", "DisplayTitle": "4K HDR"},
			wantChanged: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms := map[string]any{"MediaStreams": []any{tc.stream}}
			changed := normalizeEmbyMediaStreams(ms)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			for _, field := range embyMediaStreamNonNullFields {
				v, ok := tc.stream[field]
				if !ok {
					t.Errorf("字段 %q 补齐后仍缺失", field)
					continue
				}
				if _, isStr := v.(string); !isStr {
					t.Errorf("字段 %q 补齐后应为 string，实际 %T", field, v)
				}
			}
		})
	}
}

// 验证补齐后的结果可按客户端的严格结构解析。
func TestNormalizeEmbyMediaStreams_JSONParsable(t *testing.T) {
	ms := map[string]any{
		"MediaStreams": []any{
			map[string]any{"Type": "Video", "Codec": "hevc"},
			map[string]any{"Type": "Audio", "Language": "chi", "DisplayTitle": "DTS"},
		},
	}
	normalizeEmbyMediaStreams(ms)

	raw, err := json.Marshal(ms)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		MediaStreams []map[string]json.RawMessage `json:"MediaStreams"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, stream := range decoded.MediaStreams {
		for _, field := range embyMediaStreamNonNullFields {
			v, ok := stream[field]
			if !ok || string(v) == "null" {
				t.Errorf("stream[%d] 字段 %q 缺失或为 null: ok=%v val=%s", i, field, ok, v)
			}
		}
	}
}

func TestNormalizeEmbyMediaStreams_NoStreams(t *testing.T) {
	for _, ms := range []map[string]any{
		nil,
		{},
		{"MediaStreams": "not-an-array"},
		{"MediaStreams": []any{}},
	} {
		if normalizeEmbyMediaStreams(ms) {
			t.Errorf("空/非法 MediaStreams 不应报告修改: %v", ms)
		}
	}
}

func TestProxyRequestRewritesLocation(t *testing.T) {
	var upstreamURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" || r.URL.RawQuery != "next=%2Fhome" {
			t.Errorf("上游请求地址 = %q?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("X-Forwarded-Host") == "" || r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Errorf("缺少标准转发头: host=%q proto=%q", r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"))
		}
		if got := r.Header.Get("Authorization"); got != `MediaBrowser Token="test-token"` {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Location", upstreamURL+"/home")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()
	upstreamURL = upstream.URL

	service := New(Options{})
	var cfg Config
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.proxyRequest(w, r, cfg, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer proxyServer.Close()
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("解析反代地址失败: %v", err)
	}
	cfg = Config{FnosURL: upstream.URL, Port: proxyURL.Port()}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/login?next=%2Fhome", nil)
	if err != nil {
		t.Fatalf("创建反代请求失败: %v", err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="test-token"`)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求反代失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码 = %d，期望 %d", resp.StatusCode, http.StatusFound)
	}
	if got, want := resp.Header.Get("Location"), proxyServer.URL+"/home"; got != want {
		t.Fatalf("Location = %q，期望 %q", got, want)
	}
}

func TestRedirectSTRMStreamUsesLitePanPlayback(t *testing.T) {
	fileID := "file-non-infuse"
	litepanURL := fmt.Sprintf("http://127.0.0.1:5211/api/strm/play/7/%s/t/token/n/demo.mkv", strm.EncodeFileKey(fileID))
	service := New(Options{})
	service.rememberSource("ms4", "item-4", "/movie/demo.strm", litepanURL)
	var gotReq playback.Request
	var gotIntent playback.Intent
	service.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		gotReq = req
		gotIntent = intent
		w.Header().Set("Location", "https://cdn.example/video.mkv")
		w.WriteHeader(http.StatusFound)
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/Videos/item-4/stream?MediaSourceId=ms4", nil)
	req.Header.Set("User-Agent", "SenPlayer/1.0")
	rec := httptest.NewRecorder()

	service.redirectSTRMStream(rec, req, Config{}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码=%d，期望 302", resp.StatusCode)
	}
	if gotReq.AccountID != 7 || gotReq.FileID != fileID {
		t.Fatalf("播放请求解析错误：account=%d file=%q", gotReq.AccountID, gotReq.FileID)
	}
	if gotIntent.FileName != "demo.mkv" || gotIntent.ForceProxy || gotIntent.Inline {
		t.Fatalf("播放意图错误：%+v", gotIntent)
	}
	if got := resp.Header.Get("Location"); got != "https://cdn.example/video.mkv" {
		t.Fatalf("Location=%q", got)
	}
}

func TestRedirectSTRMStreamNonLitePanStillRedirectsOriginalURL(t *testing.T) {
	service := New(Options{})
	service.rememberSource("ms5", "item-5", "/movie/demo.strm", "https://upstream.example/raw.m3u8")
	service.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		t.Fatal("非 LitePan 地址不应进入 playback")
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/Videos/item-5/stream?MediaSourceId=ms5", nil)
	req.Header.Set("User-Agent", "Vidhub/1.0")
	rec := httptest.NewRecorder()

	service.redirectSTRMStream(rec, req, Config{}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码=%d，期望 302", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "https://upstream.example/raw.m3u8"; got != want {
		t.Fatalf("Location=%q，期望 %q", got, want)
	}
}

func TestRedirectSTRMStreamMatchedClientReturnsOriginalDescriptor(t *testing.T) {
	fileID := "file-infuse-descriptor"
	litepanURL := fmt.Sprintf("https://litepan.example:8888/api/strm/play/7/%s/t/token/n/demo.mkv", strm.EncodeFileKey(fileID))
	service := New(Options{})
	service.rememberSource("ms-infuse", "item-infuse", "/movie/demo.strm", litepanURL)
	service.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		t.Fatal("命中的终端读取客户端应先返回 STRM 文本，不应直接进入 playback")
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://media.example:18997/Videos/item-infuse/stream?MediaSourceId=ms-infuse", nil)
	req.Header.Set("User-Agent", "Infuse-Direct/8.5")
	req.Header.Set("Range", "bytes=0-")
	rec := httptest.NewRecorder()
	service.redirectSTRMStream(rec, req, Config{DirectSTRMClients: "Infuse;VidHub", Port: "18997"}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码=%d，期望 206", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := litepanURL + "\n"
	if string(body) != want {
		t.Fatalf("STRM 文本=%q，期望 %q", body, want)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type=%q，期望 text/plain", got)
	}
}

func TestDirectSTRMClientRequestMatchesConfiguredKeywords(t *testing.T) {
	tests := []struct {
		name      string
		clients   string
		client    string
		ua        string
		wantMatch bool
	}{
		{name: "客户端名称命中且忽略大小写", clients: "Infuse;VidHub", client: "infuse", wantMatch: true},
		{name: "UA命中", clients: "Infuse；VidHub", ua: "VidHub/2.1", wantMatch: true},
		{name: "未命中", clients: "Infuse;VidHub", client: "SenPlayer", ua: "SenPlayer/1.0", wantMatch: false},
		{name: "空配置", clients: "", client: "Infuse", ua: "Infuse-Direct/8.5", wantMatch: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/Videos/demo/stream", nil)
			if tc.client != "" {
				req.Header.Set("X-Emby-Client", tc.client)
			}
			req.Header.Set("User-Agent", tc.ua)
			if got := proxybase.MatchesClientKeywords(req, tc.clients); got != tc.wantMatch {
				t.Fatalf("匹配结果=%v，期望 %v", got, tc.wantMatch)
			}
		})
	}
}

func TestNormalizeClientKeywords(t *testing.T) {
	if got, want := proxybase.NormalizeClientKeywords(" Infuse；VidHub;infuse;; "), "Infuse;VidHub"; got != want {
		t.Fatalf("规范化结果=%q，期望 %q", got, want)
	}
}

func TestRedirectSTRMStreamBrokenLitePanURLNoLongerFallsBackTo302(t *testing.T) {
	service := New(Options{})
	service.rememberSource("ms6", "item-6", "/movie/demo.strm", "http://127.0.0.1:5211/api/strm/play/broken")
	service.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		t.Fatal("坏 LitePan URL 不应进入 playback")
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/Videos/item-6/stream?MediaSourceId=ms6", nil)
	req.Header.Set("User-Agent", "Vidhub/1.0")
	rec := httptest.NewRecorder()

	service.redirectSTRMStream(rec, req, Config{}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态码=%d，期望 502", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "" {
		t.Fatalf("Location=%q，期望为空", got)
	}
}

func TestRedirectSTRMStreamLitePanURLWithSpacesStillUsesPlayback(t *testing.T) {
	fileID := "file-with-spaces"
	fileName := "10间敢死队 (2026) [2160p].mkv"
	litepanURL := fmt.Sprintf("http://127.0.0.1:5211/api/strm/play/7/%s/t/token/n/%s", strm.EncodeFileKey(fileID), url.PathEscape(fileName))
	service := New(Options{})
	service.rememberSource("ms8", "item-8", "/movie/demo.strm", litepanURL)
	var gotReq playback.Request
	var gotIntent playback.Intent
	service.servePlayback = func(w http.ResponseWriter, r *http.Request, req playback.Request, intent playback.Intent) error {
		gotReq = req
		gotIntent = intent
		w.WriteHeader(http.StatusFound)
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/Videos/item-8/stream?MediaSourceId=ms8", nil)
	rec := httptest.NewRecorder()
	service.redirectSTRMStream(rec, req, Config{}, strings.TrimPrefix(req.URL.RequestURI(), "/"))

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码=%d，期望 302", resp.StatusCode)
	}
	if gotReq.AccountID != 7 || gotReq.FileID != fileID {
		t.Fatalf("带空格文件名的 LitePan URL 解析错误：account=%d file=%q", gotReq.AccountID, gotReq.FileID)
	}
	if gotIntent.FileName != fileName {
		t.Fatalf("文件名=%q，期望 %q", gotIntent.FileName, fileName)
	}
}

func TestStrmPlayRouteMatchesEscapedUnicodeFilename(t *testing.T) {
	fileName := "蜘蛛侠：崭新之日 - Spider-Man： Brand New Day (2026) [2160p].mp4"
	playPath := fmt.Sprintf(
		"/api/strm/play/10/%s/t/token/n/%s",
		strm.EncodeFileKey("file-spaces"),
		url.PathEscape(fileName),
	)
	req := httptest.NewRequest(http.MethodGet, playPath, nil)
	if proxybase.StrmPlayPathRE.MatchString(req.URL.Path) {
		t.Fatal("测试前提失效：解码后含空格的 URL.Path 不应匹配")
	}
	if !proxybase.StrmPlayPathRE.MatchString(req.URL.EscapedPath()) {
		t.Fatalf("编码路径未匹配: %q", req.URL.EscapedPath())
	}
}
