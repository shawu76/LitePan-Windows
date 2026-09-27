package file

import (
	"context"
	"strings"
	"testing"
	"time"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type uploadRootDriver struct{}

func (uploadRootDriver) Config() driver.Config      { return driver.Config{Name: "test"} }
func (uploadRootDriver) GetAddition() any           { return struct{}{} }
func (uploadRootDriver) Init(context.Context) error { return nil }
func (uploadRootDriver) Drop(context.Context) error { return nil }
func (uploadRootDriver) Ping(context.Context) error { return nil }
func (uploadRootDriver) ListFiles(context.Context, string) ([]domain.FileItem, error) {
	return nil, nil
}
func (uploadRootDriver) UploadLocalFile(_ context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	return &driver.LocalUploadResult{
		FileID:   "new-file",
		ParentID: "configured-root",
		FileName: req.FileName,
		Size:     128,
	}, nil
}

type uploadRootProvider struct{ drv driver.Driver }

func (p uploadRootProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}

type pathResolveDriver struct {
	lists map[string][]domain.FileItem
	calls map[string]int
	load  func(parentID string, call int) []domain.FileItem
}

func (d *pathResolveDriver) Config() driver.Config      { return driver.Config{Name: "path-test"} }
func (d *pathResolveDriver) GetAddition() any           { return struct{}{} }
func (d *pathResolveDriver) Init(context.Context) error { return nil }
func (d *pathResolveDriver) Drop(context.Context) error { return nil }
func (d *pathResolveDriver) Ping(context.Context) error { return nil }
func (d *pathResolveDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	d.calls[parentID]++
	if d.load != nil {
		return d.load(parentID, d.calls[parentID]), nil
	}
	return d.lists[parentID], nil
}

func TestUploadLocalRefreshesLogicalAndResolvedRootCaches(t *testing.T) {
	const accountID int64 = 7
	c := cache.NewService(cache.Options{MaxItems: 16})
	t.Cleanup(c.Close)
	c.Set(cache.DirKey(accountID, "0"), cache.DirList{{ID: "old-file", Name: "old.mkv"}}, time.Hour)
	c.Set(cache.DirKey(accountID, "configured-root"), cache.DirList{{ID: "stale-file", Name: "stale.mkv"}}, time.Hour)

	svc := NewService(
		driverexec.New(uploadRootProvider{drv: uploadRootDriver{}}, nil),
		c, nil, nil, nil, nil,
	)
	_, err := svc.UploadLocal(context.Background(), accountID, driver.LocalUploadRequest{
		ParentID: "0",
		FileName: "new.mkv",
	})
	if err != nil {
		t.Fatalf("UploadLocal() error = %v", err)
	}

	raw, ok := c.Get(cache.DirKey(accountID, "0"))
	if !ok {
		t.Fatal("logical root cache was removed instead of refreshed")
	}
	items, ok := raw.(cache.DirList)
	if !ok || len(items) != 2 {
		t.Fatalf("logical root cache = %#v", raw)
	}
	found := false
	for _, item := range items {
		found = found || item.ID == "new-file"
	}
	if !found {
		t.Fatalf("logical root cache = %#v", raw)
	}
	if _, ok := c.Get(cache.DirKey(accountID, "configured-root")); ok {
		t.Fatal("resolved root cache was not invalidated")
	}
}

func TestResolvePathUsesDirectoryCache(t *testing.T) {
	const accountID int64 = 9
	drv := &pathResolveDriver{
		lists: map[string][]domain.FileItem{
			"start":  {{ID: "movies", Name: "电影", IsDir: true}},
			"movies": {{ID: "video-1", Name: "测试.mkv"}},
		},
		calls: map[string]int{},
	}
	c := cache.NewService(cache.Options{MaxItems: 16})
	t.Cleanup(c.Close)
	svc := NewService(driverexec.New(uploadRootProvider{drv: drv}, nil), c, nil, nil, nil, nil)

	for range 2 {
		item, err := svc.ResolvePath(context.Background(), accountID, "start", "电影/测试.mkv")
		if err != nil {
			t.Fatalf("ResolvePath() error = %v", err)
		}
		if item.ID != "video-1" {
			t.Fatalf("ResolvePath() ID = %q", item.ID)
		}
	}
	if drv.calls["start"] != 1 || drv.calls["movies"] != 1 {
		t.Fatalf("ListFiles() calls = %#v", drv.calls)
	}
}

func TestResolvePathRejectsTraversalAndDirectoryTarget(t *testing.T) {
	drv := &pathResolveDriver{
		lists: map[string][]domain.FileItem{"start": {{ID: "movies", Name: "电影", IsDir: true}}},
		calls: map[string]int{},
	}
	svc := NewService(driverexec.New(uploadRootProvider{drv: drv}, nil), nil, nil, nil, nil, nil)

	if _, err := svc.ResolvePath(context.Background(), 1, "start", "../电影"); err == nil {
		t.Fatal("ResolvePath() accepted traversal path")
	}
	if _, err := svc.ResolvePath(context.Background(), 1, "start", "电影"); err == nil {
		t.Fatal("ResolvePath() accepted directory target")
	}
}

func TestResolvePathRefreshesOnlyMissedDirectory(t *testing.T) {
	drv := &pathResolveDriver{calls: map[string]int{}}
	drv.load = func(parentID string, call int) []domain.FileItem {
		if parentID == "start" && call == 1 {
			return nil
		}
		if parentID == "start" {
			return []domain.FileItem{{ID: "video-1", Name: "新文件.mkv"}}
		}
		return nil
	}
	c := cache.NewService(cache.Options{MaxItems: 16})
	t.Cleanup(c.Close)
	svc := NewService(driverexec.New(uploadRootProvider{drv: drv}, nil), c, nil, nil, nil, nil)

	item, err := svc.ResolvePath(context.Background(), 1, "start", "新文件.mkv")
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	if item.ID != "video-1" || drv.calls["start"] != 2 {
		t.Fatalf("ResolvePath() item=%#v calls=%#v", item, drv.calls)
	}
}

func TestResolvePathRejectsOversizedPath(t *testing.T) {
	svc := NewService(driverexec.New(uploadRootProvider{drv: &pathResolveDriver{calls: map[string]int{}}}, nil), nil, nil, nil, nil, nil)
	if _, err := svc.ResolvePath(context.Background(), 1, "start", strings.Repeat("a", maxResolvedPathBytes+1)); err == nil {
		t.Fatal("ResolvePath() accepted oversized path")
	}
	if _, err := svc.ResolvePath(context.Background(), 1, "start", strings.Repeat("a/", maxResolvedPathDepth)+"a"); err == nil {
		t.Fatal("ResolvePath() accepted excessive depth")
	}
}
