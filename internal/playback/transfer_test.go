package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestTransferSettingsInheritDownloadSettings(t *testing.T) {
	for _, concurrency := range []int{0, 1, 2, 3, 4} {
		chunk, workers := transferSettings(domain.DownloadInfo{ChunkSize: 10 << 20, Concurrency: concurrency})
		if chunk != 10<<20 || workers != max(1, concurrency) {
			t.Fatalf("应沿用驱动下载参数 %d：%d × %d", concurrency, chunk, workers)
		}
	}
	chunk, workers := transferSettings(domain.DownloadInfo{})
	if chunk != defaultPartSize || workers != 1 {
		t.Fatalf("缺少驱动声明时应使用通用默认值：%d × %d", chunk, workers)
	}
	chunk, workers = transferSettings(domain.DownloadInfo{Concurrency: 2, TransferChunkSize: 64 << 20, TransferConcurrency: 3})
	if chunk != 64<<20 || workers != 3 {
		t.Fatalf("未采用驱动的传输声明：%d × %d", chunk, workers)
	}
}

func transferTestFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "download.bin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func transferTestResult(server *httptest.Server, size, chunk int64) Resolved {
	return Resolved{File: domain.FileItem{Size: size}, Link: domain.DownloadInfo{
		URL: server.URL, Size: size, TransferChunkSize: chunk, TransferConcurrency: 2,
	}}
}

func TestTransferResumesInterruptedPartWithoutRefreshing(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<18)
	server, requests := newRangeTestServer(t, data, true)
	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	f := transferTestFile(t)
	var downloaded, previousPrefix int64
	res := transferTestResult(server, int64(len(data)), 1<<20)
	prefix, err := svc.DownloadOriginal(context.Background(), f, 1, "file", res, 0, int64(len(data))-1, func(n, prefix int64) error {
		downloaded += n
		if prefix < previousPrefix {
			t.Errorf("连续断点倒退：%d -> %d", previousPrefix, prefix)
		}
		previousPrefix = prefix
		return nil
	})
	if err != nil || prefix != int64(len(data)) || downloaded != prefix {
		t.Fatalf("prefix=%d downloaded=%d err=%v", prefix, downloaded, err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("断流重试后内容不一致：%v", err)
	}
	ranges := requests.snapshot()
	if len(ranges) != 5 {
		t.Fatalf("应为四片加一次续传：%v", ranges)
	}
	var resumed bool
	for _, value := range ranges {
		var start, end int64
		_, _ = fmt.Sscanf(value, "bytes=%d-%d", &start, &end)
		if start%(1<<20) != 0 {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("没有从分片内断点继续：%v", ranges)
	}
}

func TestTransferRetryableStatusesDoNotRefresh(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(status)
					return
				}
				w.Header().Set("Content-Range", "bytes 0-5/6")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("abcdef"))
			}))
			defer server.Close()
			// 没有解析服务；若错误地换链，本测试会立即失败。
			svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
			started := time.Now()
			prefix, err := svc.DownloadOriginal(context.Background(), transferTestFile(t), 1, "file", transferTestResult(server, 6, 128), 0, 5, nil)
			if err != nil || prefix != 6 || calls.Load() != 2 {
				t.Fatalf("prefix=%d calls=%d err=%v", prefix, calls.Load(), err)
			}
			if status == http.StatusTooManyRequests && time.Since(started) < time.Second {
				t.Fatal("未遵守 Retry-After")
			}
		})
	}
}

type transferRefreshDriver struct {
	url   string
	calls atomic.Int32
}

func (d *transferRefreshDriver) Config() driver.Config      { return driver.Config{Name: "mock"} }
func (d *transferRefreshDriver) GetAddition() any           { return &struct{}{} }
func (d *transferRefreshDriver) Init(context.Context) error { return nil }
func (d *transferRefreshDriver) Drop(context.Context) error { return nil }
func (d *transferRefreshDriver) Ping(context.Context) error { return nil }
func (d *transferRefreshDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (d *transferRefreshDriver) ResolveDownload(context.Context, driver.DownloadRequest) (*domain.DownloadInfo, error) {
	d.calls.Add(1)
	return &domain.DownloadInfo{URL: d.url, Size: 6, Headers: http.Header{"Authorization": {"fresh"}}}, nil
}

type transferProvider struct{ drv *transferRefreshDriver }

func (p transferProvider) Get(context.Context, int64) (driver.Driver, error) { return p.drv, nil }

func TestTransferRefreshCoalescesSameURLAndHasFixedLimit(t *testing.T) {
	for _, alwaysReject := range []bool{false, true} {
		t.Run(fmt.Sprint(alwaysReject), func(t *testing.T) {
			var stale atomic.Int32
			bothStarted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" {
					if stale.Add(1) == 2 {
						close(bothStarted)
					}
					<-bothStarted
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if alwaysReject {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				start, end, _ := parseSingleRange(r.Header.Get("Range"), 6)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/6", start, end))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("abcdef")[start : end+1])
			}))
			defer server.Close()
			drv := &transferRefreshDriver{url: server.URL}
			svc := NewService(driverexec.New(transferProvider{drv}, nil), nil)
			prefix, err := svc.DownloadOriginal(context.Background(), transferTestFile(t), 1, "file", transferTestResult(server, 6, 3), 0, 5, nil)
			if alwaysReject {
				if err == nil || drv.calls.Load() != transferRefreshes {
					t.Fatalf("应固定最多 %d 次换链：calls=%d err=%v", transferRefreshes, drv.calls.Load(), err)
				}
			} else if err != nil || prefix != 6 || drv.calls.Load() != 1 {
				t.Fatalf("同 URL 重签并发未合并：prefix=%d calls=%d err=%v", prefix, drv.calls.Load(), err)
			}
		})
	}
}

func TestTransferCancellationKeepsContiguousPrefix(t *testing.T) {
	const chunk = 1 << 20
	const cut = 64 << 10
	data := bytes.Repeat([]byte("a"), 2*chunk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstWritten := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, _ := parseSingleRange(r.Header.Get("Range"), int64(len(data)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		if start == 0 {
			_, _ = w.Write(data[:cut])
			w.(http.Flusher).Flush()
			close(firstWritten)
			<-r.Context().Done()
			return
		}
		<-firstWritten
		_, _ = w.Write(data[start : end+1])
	}))
	defer server.Close()
	svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
	f := transferTestFile(t)
	var total int64
	prefix, err := svc.DownloadOriginal(ctx, f, 1, "file", transferTestResult(server, int64(len(data)), chunk), 0, int64(len(data))-1, func(n, _ int64) error {
		total += n
		if total == chunk+cut {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || prefix != cut {
		t.Fatalf("prefix=%d want=%d err=%v", prefix, cut, err)
	}
	info, _ := f.Stat()
	if info.Size() != int64(len(data)) {
		t.Fatalf("测试未覆盖乱序尾部：size=%d", info.Size())
	}
}

func TestTransferAccountConcurrencyAcrossTasks(t *testing.T) {
	for _, tc := range []struct{ concurrency, tasks int }{{2, 1}, {3, 1}, {3, 3}} {
		t.Run(fmt.Sprintf("concurrency=%d/tasks=%d", tc.concurrency, tc.tasks), func(t *testing.T) {
			var active, peak atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := active.Add(1)
				defer active.Add(-1)
				for p := peak.Load(); current > p && !peak.CompareAndSwap(p, current); p = peak.Load() {
				}
				time.Sleep(20 * time.Millisecond)
				start, end, _ := parseSingleRange(r.Header.Get("Range"), 6)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/6", start, end))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("abcdef")[start : end+1])
			}))
			defer server.Close()
			svc := &Service{clientHTTP1: server.Client(), clientH2: server.Client()}
			var wg sync.WaitGroup
			res := transferTestResult(server, 6, 2)
			res.Link.TransferConcurrency = 0
			res.Link.Concurrency = tc.concurrency
			for range tc.tasks {
				f := transferTestFile(t)
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := svc.DownloadOriginal(context.Background(), f, 1, "file", res, 0, 5, nil); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if peak.Load() != int32(tc.concurrency) {
				t.Fatalf("单任务和账号上限均应沿用驱动并发：peak=%d want=%d", peak.Load(), tc.concurrency)
			}
		})
	}
}
