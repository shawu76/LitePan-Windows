package upload

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/file"
	"litepan/internal/playback"
)

func TestCrossTransferCheckpointProtectsSparseFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "task.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.WriteAt([]byte("abc"), 0)
	_, _ = f.WriteAt([]byte("tail"), 100)
	if err := saveCrossTransferCheckpoint(f, 3); err != nil {
		t.Fatal(err)
	}
	if offset, err := crossTransferResumeOffset(f.Name()); err != nil || offset != 3 {
		t.Fatalf("不能把文件长度当连续断点：offset=%d err=%v", offset, err)
	}
	for _, invalid := range []string{"invalid", "-1", "999"} {
		if err := os.WriteFile(f.Name()+".download", []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if offset, err := crossTransferResumeOffset(f.Name()); err != nil || offset != 0 {
			t.Fatalf("损坏检查点不应信任稀疏长度：offset=%d err=%v", offset, err)
		}
	}
}

func TestCrossTransferRestartDoesNotUploadSparseTail(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Header.Get("Range")
		var start, end int64
		_, _ = fmt.Sscanf(requested, "bytes=%d-%d", &start, &end)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/6", start, end))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("abcdef")[start : end+1])
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "task.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteAt([]byte("abc"), 0)
	_, _ = f.WriteAt([]byte("f"), 5)
	if err := saveCrossTransferCheckpoint(f, 3); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	drv := &rangeDownloadDriver{serverURL: server.URL, size: 6}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	m := NewManager(Options{Exec: exec, Files: file.NewService(exec, nil, nil, nil, nil, nil), Playback: playback.NewService(exec, nil), DataDir: t.TempDir()})
	m.tasks["restart"] = &taskState{Task: Task{TaskID: "restart", AccountID: 1,
		SourceType: SourceTypeCrossTransfer, SourceAccountID: 2, SourceFileID: "source",
		TargetPath: "target", Status: StatusPending, Phase: PhaseDownloading, TotalBytes: 6,
	}, localPath: path}
	if !m.executeCrossTransferDownload(context.Background(), "restart") {
		t.Fatal("重启后恢复失败")
	}
	if requested != "bytes=3-5" {
		t.Fatalf("应从连续断点恢复，不能跳过下载：%q", requested)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "abcdef" {
		t.Fatalf("恢复内容不完整：%q err=%v", data, err)
	}
	if _, err := os.Stat(path + ".download"); !os.IsNotExist(err) {
		t.Fatalf("下载成功后未清理检查点：%v", err)
	}
}

func TestCrossTransferCheckpointSurvivesGarbageCleanup(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	path := filepath.Join(m.TempDir(), "task.bin")
	m.tasks["paused"] = &taskState{Task: Task{SourceType: SourceTypeCrossTransfer, Status: StatusPaused}, localPath: path}
	for _, suffix := range []string{"", ".download", ".download.tmp"} {
		if err := os.WriteFile(path+suffix, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := m.CleanupOrphanTempFiles(0); err != nil || n != 0 {
		t.Fatalf("活跃任务检查点被误清理：n=%d err=%v", n, err)
	}
	m.cleanupLocalSourceAfterDelete(m.tasks["paused"])
	for _, suffix := range []string{"", ".download", ".download.tmp"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("删除任务后仍残留 %s：%v", suffix, err)
		}
	}
}

// 后台断点落盘：运行期间自行落盘，stop 之后不再写入，避免与结束时的收尾落盘互相覆盖。
func TestTransferCheckpointFlushesInBackground(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(make([]byte, 4096), 0); err != nil {
		t.Fatal(err)
	}
	c := newTransferCheckpointEvery(f, 0, 10*time.Millisecond)
	c.update(2048)
	if offset := waitCheckpointOffset(t, path, 2048); offset != 2048 {
		t.Fatalf("后台未落盘：offset=%d", offset)
	}
	c.stop()
	c.update(4096)
	time.Sleep(50 * time.Millisecond)
	if offset, err := crossTransferResumeOffset(path); err != nil || offset != 2048 {
		t.Fatalf("stop 后后台仍在落盘：offset=%d err=%v", offset, err)
	}
}

func waitCheckpointOffset(t *testing.T, path string, want int64) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var offset int64
	for time.Now().Before(deadline) {
		got, err := crossTransferResumeOffset(path)
		if err != nil {
			t.Fatal(err)
		}
		offset = got
		if offset == want {
			return offset
		}
		time.Sleep(5 * time.Millisecond)
	}
	return offset
}

func TestCrossTransferUnknownSizeFailureDoesNotBecomeComplete(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "6")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte("abc"))
			return
		}
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer server.Close()
	drv := &rangeDownloadDriver{serverURL: server.URL}
	exec := driverexec.New(fakeProvider{drv: drv}, nil)
	m := NewManager(Options{Exec: exec, Files: file.NewService(exec, nil, nil, nil, nil, nil), Playback: playback.NewService(exec, nil), DataDir: t.TempDir()})
	path := filepath.Join(t.TempDir(), "unknown.bin")
	m.tasks["unknown"] = &taskState{Task: Task{TaskID: "unknown", AccountID: 1,
		SourceType: SourceTypeCrossTransfer, SourceAccountID: 2, SourceFileID: "source",
		TargetPath: "target", Status: StatusPending, Phase: PhaseDownloading,
	}, localPath: path}
	if m.executeCrossTransferDownload(context.Background(), "unknown") {
		t.Fatal("不完整下载不能完成")
	}
	if total := m.tasks["unknown"].TotalBytes; total != 0 {
		t.Fatalf("不能用失败前的字节数冒充总大小：%d", total)
	}
	m.tasks["unknown"].Status = StatusPending
	if !m.executeCrossTransferDownload(context.Background(), "unknown") || calls.Load() != 2 {
		t.Fatal("重试必须重新下载，不能将残缺临时文件直接上传")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "abcdef" {
		t.Fatalf("重试后内容错误：%q err=%v", data, err)
	}
}
