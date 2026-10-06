package playback

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
)

// 按偏移生成内容，无需创建数 GB 的测试文件。
type rangePatternReader struct{}

func (rangePatternReader) ReadAt(p []byte, off int64) (int, error) {
	for i := range p {
		p[i] = byte((off + int64(i)) % 251)
	}
	return len(p), nil
}

func TestProxyPlaybackRanges(t *testing.T) {
	const size = int64(8<<30 + 17)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "movie.iso", time.Time{}, io.NewSectionReader(rangePatternReader{}, 0, size))
	}))
	t.Cleanup(upstream.Close)
	svc := &Service{clientHTTP1: upstream.Client(), clientH2: upstream.Client()}
	res := Resolved{Link: domain.DownloadInfo{URL: upstream.URL, Size: size, ChunkSize: 4096, Concurrency: 3}}
	res.File.ID, res.File.Name, res.File.Size = "iso", "movie.iso", size

	tests := []struct {
		name, method, header string
		status               int
		ranges               [][2]int64
	}{
		{"普通跳转", "GET", "bytes=1024-2047", 206, [][2]int64{{1024, 2047}}},
		{"超过4GB并跨分片", "GET", "bytes=5368709120-5368725503", 206, [][2]int64{{5368709120, 5368725503}}},
		{"末尾开放范围", "GET", fmt.Sprintf("bytes=%d-", size-64), 206, [][2]int64{{size - 64, size - 1}}},
		{"后缀范围", "GET", "bytes=-128", 206, [][2]int64{{size - 128, size - 1}}},
		{"截断末尾", "GET", fmt.Sprintf("bytes=%d-%d", size-32, size+10), 206, [][2]int64{{size - 32, size - 1}}},
		{"同时读取头尾", "GET", "bytes=0-31,-64", 206, [][2]int64{{0, 31}, {size - 64, size - 1}}},
		{"多段乱序跳转", "GET", "bytes=5368709120-5368709151,1024-1087", 206, [][2]int64{{5368709120, 5368709151}, {1024, 1087}}},
		{"忽略无交集分段", "GET", fmt.Sprintf("bytes=%d-,10-19", size), 206, [][2]int64{{10, 19}}},
		{"越界开放范围", "GET", fmt.Sprintf("bytes=%d-", size), 416, nil},
		{"越界闭合范围", "GET", fmt.Sprintf("bytes=%d-%d", size+10, size+20), 416, nil},
		{"倒置范围", "GET", "bytes=100-50", 416, nil},
		{"HEAD忽略Range", "HEAD", "bytes=0-31,-64", 200, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/movie.iso", nil)
			r.Header.Set("Range", tt.header)
			w := httptest.NewRecorder()
			if err := svc.serveStream(w, r, Request{AccountID: 1, FileID: "iso"}, res, "movie.iso", "test", Intent{}); err != nil {
				t.Fatal(err)
			}
			if w.Code != tt.status {
				t.Fatalf("状态=%d，期望 %d；响应头=%v", w.Code, tt.status, w.Header())
			}
			if tt.status == 416 {
				if got := w.Header().Get("Content-Range"); got != fmt.Sprintf("bytes */%d", size) {
					t.Fatalf("越界响应 Content-Range=%q", got)
				}
				return
			}
			if tt.method == "HEAD" {
				if w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.FormatInt(size, 10) {
					t.Fatal("HEAD 应返回总大小且没有正文")
				}
				return
			}
			if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
				t.Fatalf("声明长度 %q 与正文长度 %d 不一致", w.Header().Get("Content-Length"), w.Body.Len())
			}
			check := func(header string, body []byte, span [2]int64) {
				t.Helper()
				wantHeader := fmt.Sprintf("bytes %d-%d/%d", span[0], span[1], size)
				want := make([]byte, span[1]-span[0]+1)
				_, _ = (rangePatternReader{}).ReadAt(want, span[0])
				if header != wantHeader || !bytes.Equal(body, want) {
					t.Fatalf("范围正文不匹配：Content-Range=%q，期望 %q", header, wantHeader)
				}
			}
			if len(tt.ranges) == 1 {
				check(w.Header().Get("Content-Range"), w.Body.Bytes(), tt.ranges[0])
				return
			}
			mediaType, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
			if err != nil || mediaType != "multipart/byteranges" || w.Header().Get("Content-Range") != "" {
				t.Fatalf("多段响应头错误：%v", w.Header())
			}
			reader := multipart.NewReader(w.Body, params["boundary"])
			for _, span := range tt.ranges {
				part, err := reader.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				check(part.Header.Get("Content-Range"), body, span)
			}
			if _, err := reader.NextPart(); err != io.EOF {
				t.Fatalf("多段结束标记错误：%v", err)
			}
		})
	}
}

func TestSTRMMultipleRangesStillRedirect(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/movie.iso", nil)
	r.Header.Set("Range", "bytes=0-31,-64")
	w := httptest.NewRecorder()
	res := Resolved{Mode: domain.DownloadRedirect, Link: domain.DownloadInfo{URL: "https://cdn.example/movie.iso"}}
	if PickAction(res.Mode, res.Link, Intent{}) != ActionRedirect {
		t.Fatal("STRM 重定向模式不应改为代理")
	}
	writeRedirect(w, r, res, Intent{})
	if w.Code != http.StatusFound || w.Header().Get("Location") != res.Link.URL {
		t.Fatalf("重定向行为变化：%d %v", w.Code, w.Header())
	}
}

func TestProxyPlaybackRangeFallback(t *testing.T) {
	data := []byte("0123456789")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "movie.iso", time.Time{}, bytes.NewReader(data))
	}))
	defer upstream.Close()
	svc := &Service{clientHTTP1: upstream.Client(), clientH2: upstream.Client()}
	res := Resolved{Link: domain.DownloadInfo{URL: upstream.URL, Size: int64(len(data)), ChunkSize: 4, Concurrency: 3}}
	for _, header := range []string{"", "bytes=0-9,0-9", "bytes=" + strings.Repeat("0-0,", 32) + "0-0"} {
		r := httptest.NewRequest(http.MethodGet, "/movie.iso", nil)
		r.Header.Set("Range", header)
		w := httptest.NewRecorder()
		if err := svc.serveStream(w, r, Request{AccountID: 1}, res, "movie.iso", "test", Intent{}); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), data) || w.Header().Get("Content-Length") != "10" {
			t.Fatalf("完整读取或过量 Range 回退错误：header=%q status=%d body=%q", header, w.Code, w.Body.String())
		}
	}
}
