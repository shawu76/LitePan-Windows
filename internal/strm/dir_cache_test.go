package strm

import (
	"context"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"testing"
)

func TestDirectoryMutationInvalidatesCachedSubtree(t *testing.T) {
	for _, op := range []string{"rename", "move", "delete"} {
		t.Run(op, func(t *testing.T) {
			cache := newMemDirCache()
			cache.m["show"] = "电视剧/藏锋 (2026)"
			cache.m["season1"] = "电视剧/藏锋 (2026)/Season 01"
			cache.m["episodeExtras"] = "电视剧/藏锋 (2026)/花絮"
			cache.m["other"] = "电视剧/藏锋2 (2026)"
			svc := NewService(ServiceOptions{DirCache: cache})

			event := eventbus.FileMutated{AccountID: 1, Op: op}
			if op == "rename" {
				event.FileID = "show"
			} else {
				event.FileIDs = []string{"show"}
			}
			svc.OnFileMutated(t.Context(), event)

			for _, id := range []string{"show", "season1", "episodeExtras"} {
				if _, ok, _ := cache.Get(t.Context(), 1, id); ok {
					t.Fatalf("%s 后目录及子孙映射应失效，仍存在 %s", op, id)
				}
			}
			if path, ok, _ := cache.Get(t.Context(), 1, "other"); !ok || path != "电视剧/藏锋2 (2026)" {
				t.Fatalf("%s 不应误删同名前缀目录，实际 path=%q ok=%v", op, path, ok)
			}
		})
	}
}

func TestFileMutationDoesNotInvalidateDirectoryCache(t *testing.T) {
	cache := newMemDirCache()
	cache.m["show"] = "电视剧/藏锋 (2026)"
	svc := NewService(ServiceOptions{DirCache: cache})

	svc.OnFileMutated(t.Context(), eventbus.FileMutated{
		AccountID: 1,
		Op:        "rename",
		FileID:    "video-file-id",
	})

	if path, ok, _ := cache.Get(t.Context(), 1, "show"); !ok || path != "电视剧/藏锋 (2026)" {
		t.Fatalf("普通文件改名不应影响目录映射，实际 path=%q ok=%v", path, ok)
	}
}

func TestPruneDirCacheNormalCleanup(t *testing.T) {
	cache := newMemDirCache()
	ctx := context.Background()
	// 预置 3 条映射：dirA 有文件、dirB 有文件、dirC 有文件
	_ = cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "dirA", DirPath: "root/a"},
		{AccountID: 1, DirID: "dirB", DirPath: "root/b"},
		{AccountID: 1, DirID: "dirC", DirPath: "root/c"},
	})
	// 清单只出现 dirA、dirB（dirC 目录被删了）
	entries := []driver.FullListEntry{
		{FileID: "f1", ParentID: "dirA", Name: "1.mkv"},
		{FileID: "f2", ParentID: "dirB", Name: "2.mkv"},
	}
	task := &domain.StrmTask{AccountID: 1, Path: "/root"}
	deps := ScanDeps{DirCache: cache}
	if err := pruneDirCache(ctx, deps, task, entries); err != nil {
		t.Fatal(err)
	}
	// dirC 应被清掉，dirA/dirB 保留
	if _, ok, _ := cache.Get(ctx, 1, "dirC"); ok {
		t.Fatal("dirC 应被清理")
	}
	if _, ok, _ := cache.Get(ctx, 1, "dirA"); !ok {
		t.Fatal("dirA 应保留")
	}
	if _, ok, _ := cache.Get(ctx, 1, "dirB"); !ok {
		t.Fatal("dirB 应保留")
	}
}

func TestPruneDirCacheSkipsShrunkListing(t *testing.T) {
	cache := newMemDirCache()
	ctx := context.Background()
	// 预置 6 条映射
	_ = cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d1", DirPath: "root/x/d1"},
		{AccountID: 1, DirID: "d2", DirPath: "root/x/d2"},
		{AccountID: 1, DirID: "d3", DirPath: "root/x/d3"},
		{AccountID: 1, DirID: "d4", DirPath: "root/x/d4"},
		{AccountID: 1, DirID: "d5", DirPath: "root/x/d5"},
		{AccountID: 1, DirID: "d6", DirPath: "root/x/d6"},
	})
	// 清单严重缩水：只覆盖 2 个目录（< 6/2=3 应该保护）
	entries := []driver.FullListEntry{
		{FileID: "f1", ParentID: "d1", Name: "1.mkv"},
		{FileID: "f2", ParentID: "d2", Name: "2.mkv"},
	}
	task := &domain.StrmTask{AccountID: 1, Path: "/root"}
	deps := ScanDeps{DirCache: cache}
	if err := pruneDirCache(ctx, deps, task, entries); err != nil {
		t.Fatal(err)
	}
	// 缩水保护：所有映射都应保留
	for _, id := range []string{"d1", "d2", "d3", "d4", "d5", "d6"} {
		if _, ok, _ := cache.Get(ctx, 1, id); !ok {
			t.Fatalf("%s 不应被清理（缩水保护）", id)
		}
	}
}

func TestPruneDirCacheSkipsEmptyListing(t *testing.T) {
	cache := newMemDirCache()
	ctx := context.Background()
	_ = cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d1", DirPath: "root/d1"},
	})
	task := &domain.StrmTask{AccountID: 1, Path: "/root"}
	deps := ScanDeps{DirCache: cache}
	// 空清单：直接跳过
	if err := pruneDirCache(ctx, deps, task, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := cache.Get(ctx, 1, "d1"); !ok {
		t.Fatal("空清单不应触发清理")
	}
}

func TestPruneDirCacheKeepsAtThresholdHalf(t *testing.T) {
	cache := newMemDirCache()
	ctx := context.Background()
	// 预置 6 条映射
	_ = cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d1", DirPath: "root/x/d1"},
		{AccountID: 1, DirID: "d2", DirPath: "root/x/d2"},
		{AccountID: 1, DirID: "d3", DirPath: "root/x/d3"},
		{AccountID: 1, DirID: "d4", DirPath: "root/x/d4"},
		{AccountID: 1, DirID: "d5", DirPath: "root/x/d5"},
		{AccountID: 1, DirID: "d6", DirPath: "root/x/d6"},
	})
	// 清单覆盖恰好一半（3 个）：len(seen)=3, len(existing)/2=3，不小于则不保护，正常清理 d4-d6
	entries := []driver.FullListEntry{
		{FileID: "f1", ParentID: "d1", Name: "1.mkv"},
		{FileID: "f2", ParentID: "d2", Name: "2.mkv"},
		{FileID: "f3", ParentID: "d3", Name: "3.mkv"},
	}
	task := &domain.StrmTask{AccountID: 1, Path: "/root"}
	deps := ScanDeps{DirCache: cache}
	if err := pruneDirCache(ctx, deps, task, entries); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := cache.Get(ctx, 1, "d4"); ok {
		t.Fatal("清单覆盖一半时不应触发保护，d4 应被清理")
	}
}

func TestPruneDirCacheProtectsBelowHalfWithOddExistingCount(t *testing.T) {
	cache := newMemDirCache()
	ctx := context.Background()
	_ = cache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{
		{AccountID: 1, DirID: "d1", DirPath: "root/x/d1"},
		{AccountID: 1, DirID: "d2", DirPath: "root/x/d2"},
		{AccountID: 1, DirID: "d3", DirPath: "root/x/d3"},
		{AccountID: 1, DirID: "d4", DirPath: "root/x/d4"},
		{AccountID: 1, DirID: "d5", DirPath: "root/x/d5"},
	})
	entries := []driver.FullListEntry{
		{FileID: "f1", ParentID: "d1", Name: "1.mkv"},
		{FileID: "f2", ParentID: "d2", Name: "2.mkv"},
	}
	task := &domain.StrmTask{AccountID: 1, Path: "/root"}
	if err := pruneDirCache(ctx, ScanDeps{DirCache: cache}, task, entries); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d1", "d2", "d3", "d4", "d5"} {
		if _, ok, _ := cache.Get(ctx, 1, id); !ok {
			t.Fatalf("%s 不应被清理：5 条映射只命中 2 条，仍低于一半", id)
		}
	}
}
