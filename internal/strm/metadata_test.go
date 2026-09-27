package strm

import (
	"context"
	"errors"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/playback"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metadataResolverStub struct {
	mu           sync.Mutex
	baseURL      string
	calls        map[string]int
	active       int
	maxActive    int
	resolveDelay time.Duration
}

type metadataLocalResolverStub struct {
	localPath string
	size      int64
}

func (s *metadataLocalResolverStub) Resolve(
	context.Context,
	int64,
	string, string,
	bool,
	bool,
) (playback.Resolved, error) {
	return playback.Resolved{
		Link: domain.DownloadInfo{LocalPath: s.localPath, Size: s.size},
	}, nil
}

func (s *metadataResolverStub) Resolve(
	ctx context.Context,
	_ int64,
	fileID, _ string,
	_ bool,
	_ bool,
) (playback.Resolved, error) {
	s.mu.Lock()
	if s.calls == nil {
		s.calls = make(map[string]int)
	}
	s.calls[fileID]++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if s.resolveDelay > 0 {
		timer := time.NewTimer(s.resolveDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return playback.Resolved{}, ctx.Err()
		case <-timer.C:
		}
	}
	return playback.Resolved{
		Link: domain.DownloadInfo{URL: s.baseURL + "/" + fileID},
	}, nil
}

func (s *metadataResolverStub) callCount(fileID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[fileID]
}

func (s *metadataResolverStub) peakResolveConcurrency() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxActive
}

func TestMetadataSyncerPipelinesSerialResolveAndThreeCDNDownloads(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte(filepath.Base(r.URL.Path)))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	resolver := &metadataResolverStub{baseURL: server.URL, resolveDelay: 5 * time.Millisecond}
	items := make([]metadataItem, 0, 6)
	for i := range 6 {
		id := string(rune('a' + i))
		items = append(items, metadataItem{fileID: id, relPath: id + ".nfo"})
	}
	root := t.TempDir()
	type result struct {
		created int64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, items)
		done <- result{created: created, err: err}
	}()

	for range metadataCDNConcurrency {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("未形成 3 路 CDN 并发")
		}
	}
	select {
	case <-entered:
		t.Fatal("CDN 下载并发超过 3")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.created != int64(len(items)) {
		t.Fatalf("新增数=%d，期望=%d", got.created, len(items))
	}
	if peak.Load() != metadataCDNConcurrency {
		t.Fatalf("CDN 峰值并发=%d，期望=%d", peak.Load(), metadataCDNConcurrency)
	}
	if resolver.peakResolveConcurrency() != 1 {
		t.Fatalf("取直链峰值并发=%d，期望=1", resolver.peakResolveConcurrency())
	}
}

func TestMetadataSyncerDeduplicatesFileIDAndPreservesExistingFiles(t *testing.T) {
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write([]byte("metadata"))
	}))
	defer server.Close()

	root := t.TempDir()
	existing := filepath.Join(root, "existing.nfo")
	if err := os.WriteFile(existing, []byte("scraped"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &metadataResolverStub{baseURL: server.URL}
	items := []metadataItem{
		{fileID: "same", relPath: "one.nfo"},
		{fileID: "same", relPath: "two.nfo"},
		{fileID: "existing", relPath: "existing.nfo"},
	}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, items)
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 {
		t.Fatalf("新增数=%d，期望=2", created)
	}
	if resolver.callCount("same") != 1 || resolver.callCount("existing") != 0 {
		t.Fatalf("取直链次数 same=%d existing=%d", resolver.callCount("same"), resolver.callCount("existing"))
	}
	if downloads.Load() != 1 {
		t.Fatalf("CDN 下载次数=%d，期望=1", downloads.Load())
	}
	if body, err := os.ReadFile(existing); err != nil || string(body) != "scraped" {
		t.Fatalf("已有刮削文件被改写：%q, err=%v", body, err)
	}
}

func TestMetadataSyncerReadsLocalMetadataFile(t *testing.T) {
	body := []byte("[Script Info]\nTitle: Local subtitle\n")
	source := filepath.Join(t.TempDir(), "movie.zh.ass")
	if err := os.WriteFile(source, body, 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	resolver := &metadataLocalResolverStub{localPath: source, size: int64(len(body))}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, []metadataItem{
		{fileID: "subtitle", relPath: "Movie (2026)/Movie (2026).zh.ass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("新增数=%d，期望=1", created)
	}
	got, err := os.ReadFile(filepath.Join(root, "Movie (2026)", "Movie (2026).zh.ass"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("本地元数据内容=%q，期望=%q", got, body)
	}
}

func TestMetadataSyncerSerializesRefreshResolve(t *testing.T) {
	var requestMu sync.Mutex
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests[r.URL.Path]++
		count := requests[r.URL.Path]
		requestMu.Unlock()
		if count == 1 {
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("metadata"))
	}))
	defer server.Close()

	resolver := &metadataResolverStub{baseURL: server.URL, resolveDelay: 10 * time.Millisecond}
	items := []metadataItem{
		{fileID: "one", relPath: "one.nfo"},
		{fileID: "two", relPath: "two.nfo"},
		{fileID: "three", relPath: "three.nfo"},
	}
	created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, t.TempDir(), items)
	if err != nil {
		t.Fatal(err)
	}
	if created != int64(len(items)) {
		t.Fatalf("新增数=%d，期望=%d", created, len(items))
	}
	if resolver.peakResolveConcurrency() != 1 {
		t.Fatalf("刷新取直链峰值并发=%d，期望=1", resolver.peakResolveConcurrency())
	}
	for _, item := range items {
		if got := resolver.callCount(item.fileID); got != 2 {
			t.Fatalf("%s 取直链次数=%d，期望首次+刷新共2次", item.fileID, got)
		}
	}
}

func TestMetadataSyncerDoesNotOverwriteFileCreatedDuringDownload(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte("remote"))
	}))
	defer server.Close()

	root := t.TempDir()
	target := filepath.Join(root, "movie.nfo")
	resolver := &metadataResolverStub{baseURL: server.URL}
	done := make(chan struct {
		created int64
		err     error
	}, 1)
	go func() {
		created, err := (&metadataSyncer{playback: resolver}).syncFiles(t.Context(), 1, root, []metadataItem{
			{fileID: "movie", relPath: "movie.nfo"},
		})
		done <- struct {
			created int64
			err     error
		}{created: created, err: err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("元数据下载未开始")
	}
	if err := os.WriteFile(target, []byte("scraped"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.created != 0 {
		t.Fatalf("新增数=%d，期望不覆盖并发生成的文件", got.created)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "scraped" {
		t.Fatalf("并发刮削文件被覆盖：%q, err=%v", body, err)
	}
}

func TestMetadataSyncerCancellationStopsWorkers(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resolver := &metadataResolverStub{baseURL: server.URL}
	done := make(chan error, 1)
	go func() {
		_, err := (&metadataSyncer{playback: resolver}).syncFiles(ctx, 1, t.TempDir(), []metadataItem{
			{fileID: "cancel", relPath: "cancel.nfo"},
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("元数据下载未开始")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消后错误=%v，期望 context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后下载协程未退出")
	}
}

type metadataFilesStub struct {
	remote         map[string][]domain.FileItem
	uploads        []driver.LocalUploadRequest
	mutationMarked bool
}

func (s *metadataFilesStub) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), s.remote[parentID]...), nil
}

func (s *metadataFilesStub) UploadLocal(ctx context.Context, _ int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	s.mutationMarked = isMetadataSyncMutation(ctx)
	s.uploads = append(s.uploads, req)
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		return nil, err
	}
	return &driver.LocalUploadResult{
		FileID:   "uploaded-" + req.FileName,
		ParentID: req.ParentID,
		FileName: req.FileName,
		Size:     info.Size(),
	}, nil
}

func TestMetadataSyncModesWithSimulatedFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cloud metadata"))
	}))
	defer server.Close()

	tests := []struct {
		name          string
		mode          string
		wantLocal     bool
		wantUploaded  int64
		wantDeleted   int64
		wantLastPhase string
	}{
		{
			name:          "本地元数据补缺只从网盘补缺",
			mode:          MetadataSyncLocalPrimary,
			wantLocal:     true,
			wantLastPhase: ScanPhaseMetadata,
		},
		{
			name:          "网盘元数据为主下载并清理本地多余文件",
			mode:          MetadataSyncCloudPrimary,
			wantLocal:     false,
			wantDeleted:   1,
			wantLastPhase: ScanPhaseMetadataCleanup,
		},
		{
			name:          "本地与云端互补下载并上传双方缺失文件",
			mode:          MetadataSyncBidirectional,
			wantLocal:     true,
			wantUploaded:  1,
			wantLastPhase: ScanPhaseMetadataUpload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "任务", "电影")
			if err := os.MkdirAll(localDir, 0o755); err != nil {
				t.Fatal(err)
			}
			localOnly := filepath.Join(localDir, "local.nfo")
			if err := os.WriteFile(localOnly, []byte("local metadata"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(localDir, "shared.jpg"), []byte("shared"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(localDir, "movie.strm"), []byte("play url"), 0o644); err != nil {
				t.Fatal(err)
			}

			files := &metadataFilesStub{remote: map[string][]domain.FileItem{
				"remote-movie": {
					{ID: "cloud", Name: "cloud.nfo", Size: 14},
					{ID: "shared", Name: "shared.jpg", Size: 6},
				},
			}}
			var phases []string
			result, err := syncMetadata(t.Context(), metadataSyncRequest{
				AccountID:    1,
				Root:         root,
				OutputFolder: "任务",
				Mode:         tt.mode,
				Extensions:   map[string]struct{}{"nfo": {}, "jpg": {}},
				MaxSizeBytes: 10 << 20,
				RemoteItems: []metadataItem{
					{fileID: "cloud", fileName: "cloud.nfo", relPath: filepath.Join("任务", "电影", "cloud.nfo")},
					{fileID: "shared", fileName: "shared.jpg", relPath: filepath.Join("任务", "电影", "shared.jpg")},
				},
				Directories: map[string]metadataDirectory{
					dirKey([]string{"电影"}): {parentID: "remote-movie", relDirs: []string{"电影"}},
				},
				Files:    files,
				Playback: &metadataResolverStub{baseURL: server.URL},
				OnProgress: func(update ScanProgressUpdate) {
					if update.Phase != "" {
						phases = append(phases, update.Phase)
					}
				},
			})
			if err != nil {
				t.Fatalf("同步失败: %v", err)
			}
			if result.Downloaded != 1 {
				t.Fatalf("下载数量=%d，期望=1", result.Downloaded)
			}
			if result.Uploaded != tt.wantUploaded || result.Deleted != tt.wantDeleted {
				t.Fatalf("结果=%+v，期望上传=%d、删除=%d", result, tt.wantUploaded, tt.wantDeleted)
			}
			if _, err := os.Stat(filepath.Join(localDir, "cloud.nfo")); err != nil {
				t.Fatalf("云端缺失元数据未补到本地: %v", err)
			}
			_, localErr := os.Stat(localOnly)
			if (localErr == nil) != tt.wantLocal {
				t.Fatalf("本地独有元数据存在=%v，期望=%v", localErr == nil, tt.wantLocal)
			}
			if _, err := os.Stat(filepath.Join(localDir, "movie.strm")); err != nil {
				t.Fatalf("同步不应处理 STRM 文件: %v", err)
			}
			if tt.wantUploaded > 0 {
				if len(files.uploads) != 1 || files.uploads[0].FileName != "local.nfo" {
					t.Fatalf("上传请求=%+v，期望只上传 local.nfo", files.uploads)
				}
				if !files.mutationMarked {
					t.Fatal("元数据反向上传必须标记为内部事件，避免再次触发 STRM 扫描")
				}
			}
			if len(phases) == 0 || phases[len(phases)-1] != tt.wantLastPhase {
				t.Fatalf("进度阶段=%v，最后阶段期望=%q", phases, tt.wantLastPhase)
			}
		})
	}
}

func TestCloudPrimarySkipsCleanupWhenCloudDownloadIsIncomplete(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	localOnly := filepath.Join(localDir, "local.nfo")
	if err := os.WriteFile(localOnly, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := syncMetadata(t.Context(), metadataSyncRequest{
		Root:         root,
		OutputFolder: "任务",
		Mode:         MetadataSyncCloudPrimary,
		Extensions:   map[string]struct{}{"nfo": {}},
		RemoteItems: []metadataItem{
			{fileID: "missing", relPath: filepath.Join("任务", "电影", "cloud.nfo")},
		},
		Directories: map[string]metadataDirectory{
			dirKey([]string{"电影"}): {parentID: "remote-movie", relDirs: []string{"电影"}},
		},
	})
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("云端下载未完成时不应清理本地，实际删除=%d", result.Deleted)
	}
	if _, err := os.Stat(localOnly); err != nil {
		t.Fatalf("云端下载未完成时本地文件应保留: %v", err)
	}
}

func TestMetadataUploadMutationDoesNotWakeScanner(t *testing.T) {
	svc := NewService(ServiceOptions{})
	svc.OnFileMutated(withMetadataSyncMutation(t.Context()), eventbus.FileMutated{
		AccountID: 9,
		Op:        "create",
	})
	if svc.dirtyAccounts[9] {
		t.Fatal("STRM 自己上传的元数据不应再次唤醒扫描")
	}

	svc.OnFileMutated(t.Context(), eventbus.FileMutated{
		AccountID: 9,
		Op:        "create",
	})
	if !svc.dirtyAccounts[9] {
		t.Fatal("普通文件变更仍应唤醒扫描")
	}
}
