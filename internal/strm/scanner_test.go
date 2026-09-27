package strm

import (
	"context"
	"fmt"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type metadataTestDriver struct {
	items     map[string][]domain.FileItem
	listCalls map[string]int
}

func (d *metadataTestDriver) Config() driver.Config      { return driver.Config{Name: "metadata-test"} }
func (d *metadataTestDriver) GetAddition() any           { return &struct{}{} }
func (d *metadataTestDriver) Init(context.Context) error { return nil }
func (d *metadataTestDriver) Drop(context.Context) error { return nil }
func (d *metadataTestDriver) Ping(context.Context) error { return nil }
func (d *metadataTestDriver) ListFiles(_ context.Context, parentID string) ([]domain.FileItem, error) {
	if d.listCalls != nil {
		d.listCalls[parentID]++
	}
	return d.items[parentID], nil
}

type metadataTestProvider struct {
	drv driver.Driver
}

func (p metadataTestProvider) Get(context.Context, int64) (driver.Driver, error) {
	return p.drv, nil
}

func TestFilterMetadataItemsMatchesParentMetadataSetting(t *testing.T) {
	t.Parallel()

	items := []metadataItem{
		{relDirs: []string{"媒体库", "电视剧", "Season 1"}, relPath: "任务/媒体库/电视剧/Season 1/episode.nfo", direct: true},
		{relDirs: []string{"媒体库", "电视剧"}, relPath: "任务/媒体库/电视剧/poster.jpg", direct: true},
		{relDirs: []string{"媒体库"}, relPath: "任务/媒体库/library.nfo", direct: true},
		{relDirs: []string{"无媒体目录"}, relPath: "任务/无媒体目录/poster.jpg", direct: true},
	}
	dirHasMedia := map[string]bool{
		dirKey([]string{"媒体库", "电视剧", "Season 1"}): true,
	}
	subtreeHasMedia := make(map[string]bool)
	markSubtreeMedia(subtreeHasMedia, []string{"媒体库", "电视剧", "Season 1"})

	tests := []struct {
		name          string
		parentEnabled bool
		want          []string
	}{
		{
			name:          "开启时包含有媒体的父目录",
			parentEnabled: true,
			want: []string{
				"任务/媒体库/电视剧/Season 1/episode.nfo",
				"任务/媒体库/电视剧/poster.jpg",
				"任务/媒体库/library.nfo",
			},
		},
		{
			name:          "关闭时只包含媒体同目录",
			parentEnabled: false,
			want: []string{
				"任务/媒体库/电视剧/Season 1/episode.nfo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterMetadataItems(items, dirHasMedia, subtreeHasMedia, tt.parentEnabled)
			if len(got) != len(tt.want) {
				t.Fatalf("过滤结果数量=%d，期望=%d，结果=%v", len(got), len(tt.want), metadataPaths(got))
			}
			for i := range tt.want {
				if got[i].relPath != tt.want[i] {
					t.Fatalf("第%d项路径=%q，期望=%q", i, got[i].relPath, tt.want[i])
				}
			}
		})
	}
}

func TestFilterMetadataItemsPrefersDirectItemAfterEligibilityCheck(t *testing.T) {
	t.Parallel()

	const relPath = "任务/电影/影片.iso.nfo"
	items := []metadataItem{
		{fileID: "aligned", relDirs: []string{"电影"}, relPath: relPath, legacyRelPath: "任务/电影/影片.nfo", direct: false},
		{fileID: "direct", relDirs: []string{"电影"}, relPath: relPath, direct: true},
	}
	dirHasMedia := map[string]bool{dirKey([]string{"电影"}): true}

	got := filterMetadataItems(items, dirHasMedia, nil, false)
	if len(got) != 1 {
		t.Fatalf("去重后数量=%d，期望=1", len(got))
	}
	if !got[0].direct {
		t.Fatal("相同目标路径应优先保留直接匹配的元数据")
	}
	if got[0].fileID != "direct" {
		t.Fatalf("保留的文件ID=%q，期望直接匹配项 direct", got[0].fileID)
	}
}

func TestWalkBaseBranchEntryTreatsSkippedLocalSTRMAsSubtreeMedia(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	localSeason := filepath.Join(root, "任务", "电视剧", "Season 1")
	if err := os.MkdirAll(localSeason, 0o755); err != nil {
		t.Fatalf("创建模拟目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localSeason, "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatalf("创建模拟STRM: %v", err)
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"show": {
			{ID: "season-1", Name: "Season 1", IsDir: true},
			{ID: "poster", Name: "poster.jpg", Size: 1024},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{ID: 1, AccountID: 1, OutputFolder: "任务"}
	deps := ScanDeps{Files: files}
	scope := scanScope{parentID: "show", relDirs: []string{"电视剧"}, baseEntry: true}

	harvest := newScanHarvest()

	children, _, err := walkBaseBranchEntry(
		context.Background(), task, deps, scope,
		scanRules{mediaExts: map[string]struct{}{"mkv": {}}, metadataExts: map[string]struct{}{"jpg": {}}, maxMetadataBytes: 10 << 20, syncMetadata: true, outputRelDir: "任务"},
		make(map[string]struct{}), root, &harvest, nil,
	)
	if err != nil {
		t.Fatalf("扫描基础分支: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("本地已有STRM的子树不应重新扫描，children=%d", len(children))
	}
	if _, ok := harvest.state.skippedDirs[dirKey([]string{"电视剧", "Season 1"})]; !ok {
		t.Fatal("本地已有STRM的子树应记录为跳过目录")
	}

	got := filterMetadataItems(harvest.metadataItems, harvest.dirHasMedia, harvest.subtreeHasMedia, true)
	if len(got) != 1 || got[0].relPath != filepath.Join("任务", "电视剧", "poster.jpg") {
		t.Fatalf("开启父目录元数据后应保留海报，结果=%v", metadataPaths(got))
	}
}

func TestWalkBaseBranchEntrySkipsBranchProbeWithoutRepository(t *testing.T) {
	t.Parallel()

	drv := &metadataTestDriver{
		items: map[string][]domain.FileItem{
			"show": {
				{ID: "season-1", Name: "Season 1", IsDir: true},
			},
			"season-1": {
				{ID: "episode-1", Name: "S01E01.mkv", Size: 10 << 20},
			},
		},
		listCalls: make(map[string]int),
	}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{ID: 1, AccountID: 1, OutputFolder: "任务"}
	deps := ScanDeps{Files: files}
	scope := scanScope{parentID: "show", relDirs: []string{"电视剧"}, baseEntry: true}

	harvest := newScanHarvest()

	children, _, err := walkBaseBranchEntry(
		context.Background(), task, deps, scope,
		scanRules{mediaExts: map[string]struct{}{"mkv": {}}, outputRelDir: "任务"},
		make(map[string]struct{}), t.TempDir(), &harvest, nil,
	)
	if err != nil {
		t.Fatalf("扫描基础分支: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("children=%d, want 1", len(children))
	}
	if got := drv.listCalls["show"]; got != 1 {
		t.Fatalf("根目录 List 次数=%d, want 1", got)
	}
	if got := drv.listCalls["season-1"]; got != 0 {
		t.Fatalf("未启用分支仓库时不应预探测子目录，season-1 List 次数=%d", got)
	}
}

func metadataPaths(items []metadataItem) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.relPath)
	}
	return paths
}

func TestScanTaskSkipsOversizedDirectoryWithoutFailingTask(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("界", 86)
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {
			{ID: "long-dir", Name: longDir, IsDir: true},
		},
		"long-dir": {
			{ID: "season-1", Name: "Season 1", IsDir: true},
		},
		"season-1": {
			{ID: "episode", Name: "第01集.mkv", Size: 1024},
			{ID: "iso", Name: "特别篇.iso", Size: 2048},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeFullSync,
		Extensions:   "mkv;iso",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{
		Files:   files,
		StrmDir: t.TempDir(),
		Settings: ScanSettings{
			ISOFilenameEnabled: true,
		},
	}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("超长目录只应跳过，不应导致任务失败：%v", err)
	}
	if result.ScannedCount != 2 {
		t.Fatalf("扫描文件数=%d，期望=2", result.ScannedCount)
	}
	if result.GeneratedCount != 0 {
		t.Fatalf("超长目录下不应生成 STRM，实际=%d", result.GeneratedCount)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("同一超长目录应汇总一个失败项，实际=%d：%v", len(result.Failures), result.Failures)
	}
	wantPath := filepath.ToSlash(filepath.Join("任务", longDir))
	if result.Failures[0].Path != wantPath || result.Failures[0].Reason != pathTooLongDirReason {
		t.Fatalf("失败项=%+v，期望路径=%q", result.Failures[0], wantPath)
	}
}

func TestScanTaskReportsOversizedFileName(t *testing.T) {
	t.Parallel()

	longFile := strings.Repeat("影", 86) + ".mkv"
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {
			{ID: "movie", Name: longFile, Size: 1024},
		},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           2,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: t.TempDir()}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatalf("超长文件名只应跳过，不应导致任务失败：%v", err)
	}
	if len(result.Failures) != 1 || result.Failures[0].Reason != pathTooLongFileReason {
		t.Fatalf("失败项=%+v", result.Failures)
	}
}

func TestValidateMonitorBranchesRejectsTaskRoot(t *testing.T) {
	task := &domain.StrmTask{Path: "/云影音"}
	broken := &domain.StrmBranch{
		ParentID:      "",
		Path:          "",
		RelativePath:  "",
		BranchType:    domain.StrmBranchTypeTemporary,
		RetentionDays: 90,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{broken}); err == nil {
		t.Fatal("指向任务根目录的临时监控分支应被拒绝")
	}

	valid := &domain.StrmBranch{
		ParentID:     "movie-id",
		Path:         "/云影音/电影",
		RelativePath: "电影",
		BranchType:   domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{valid}); err != nil {
		t.Fatalf("有效监控分支不应被拒绝：%v", err)
	}
}

func TestIsStrmUnderSkippedRootSkipProtectsWholeTaskTree(t *testing.T) {
	skipped := map[string]struct{}{"": {}}
	taskFolder := "任务"

	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{name: "root file", rel: "任务/影片.strm", want: true},
		{name: "nested file", rel: "任务/电视剧/Season 1/第01集.strm", want: true},
		{name: "other task", rel: "别的任务/影片.strm", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStrmUnderSkipped(tc.rel, taskFolder, skipped); got != tc.want {
				t.Fatalf("isStrmUnderSkipped(%q)=%v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}

func TestCleanupMissingRemoteChildDirsCountsOnlyStrmFiles(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电视剧", "Season 1")
	if err := os.MkdirAll(filepath.Join(localDir, "extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "poster.jpg"), []byte("poster"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "extras", "E02.strm"), []byte("https://example.test/E02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "extras", "note.txt"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := cleanupMissingRemoteChildDirs(root, "任务", map[string]map[string]struct{}{
		dirKey([]string{"电视剧"}): {},
	}, nil, nil)
	if err != nil {
		t.Fatalf("cleanupMissingRemoteChildDirs() error = %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2 strm files", removed)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("远端已删除目录应被清理，stat err = %v", err)
	}
}

func TestLocalChildDirsWithStrmFindsNestedChildSubtrees(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "任务", "电视剧")
	if err := os.MkdirAll(filepath.Join(base, "Season 1", "extras"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "Season 2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Season 1", "extras", "E01.strm"), []byte("https://example.test/E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Season 2", "E01.strm"), []byte("https://example.test/S02E01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "poster.strm"), []byte("https://example.test/poster"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := localChildDirsWithStrm(root, "任务", []string{"电视剧"})
	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if _, ok := got[SafeName("Season 1")]; !ok {
		t.Fatal("Season 1 应命中本地已有 STRM 的子树集合")
	}
	if _, ok := got[SafeName("Season 2")]; !ok {
		t.Fatal("Season 2 应命中本地已有 STRM 的子树集合")
	}
	if _, ok := got[SafeName("poster.strm")]; ok {
		t.Fatal("父目录直下的 STRM 文件不应被误判成子目录")
	}
}

// TestCleanupProtectReason 覆盖清理范围级空保护与明确删除放行。
func TestScanTaskAllowsExplicitDirectoryDeletionAfterNonEmptyListing(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"任务/keep.strm", "任务/已消失目录/a.strm"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {{ID: "keep-id", Name: "keep.mkv", Size: 1 << 20}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小规模且远端明确消失的目录应正常同步删除：%+v", result)
	}
	if result.RemovedCount != 1 {
		t.Fatalf("应删除消失目录中的 1 个 STRM，实际=%d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "已消失目录")); !os.IsNotExist(err) {
		t.Fatalf("远端明确消失的小目录应被同步删除，stat err=%v", err)
	}
}

func TestScanTaskStillAllowsNormalSmallCleanup(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"任务/keep.strm", "任务/stale.strm"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"library": {{ID: "keep-id", Name: "keep.mkv", Size: 1 << 20}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小量正常清理不应触发保护：%+v", result)
	}
	if result.RemovedCount != 1 {
		t.Fatalf("应清理 1 个过期 STRM，实际=%d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "stale.strm")); !os.IsNotExist(err) {
		t.Fatalf("过期 STRM 应被删除，stat err=%v", err)
	}
}

type safetyBranchRepo struct {
	branches []*domain.StrmBranch
	deleted  []int64
}

func (r *safetyBranchRepo) Create(context.Context, *domain.StrmBranch) (int64, error) {
	return 0, nil
}
func (r *safetyBranchRepo) Update(context.Context, *domain.StrmBranch) error { return nil }
func (r *safetyBranchRepo) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}
func (r *safetyBranchRepo) Get(context.Context, int64) (*domain.StrmBranch, error) {
	return nil, fmt.Errorf("not implemented")
}
func (r *safetyBranchRepo) ListByTask(context.Context, int64) ([]*domain.StrmBranch, error) {
	return r.branches, nil
}
func (r *safetyBranchRepo) DeleteExpired(context.Context, int64) (int, error) { return 0, nil }

func TestManualBranchAllowsEmptyBaseCleanup(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "a.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := &safetyBranchRepo{branches: []*domain.StrmBranch{
		{ID: 1, TaskID: 1, ParentID: "base-id", Path: "/云影音", BranchType: domain.StrmBranchTypeBase},
		{ID: 2, TaskID: 1, ParentID: "movie-id", Path: "/云影音/电影", RelativePath: "电影", Recursive: true, BranchType: domain.StrmBranchTypeTemporary},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"base-id": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 1,
		AccountID:          1,
		Path:               "/云影音",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, Branches: repo, StrmDir: root}, domain.StrmRunModeBranch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("小规模且明确消失的分支不应触发保护：%+v", result)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 2 {
		t.Fatalf("安全检查通过后应删除消失分支记录：deleted=%v", repo.deleted)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("消失分支的本地目录应被同步删除，stat err=%v", err)
	}
}

func TestAutoBranchDeletesMissingBranchAfterNonEmptyBaseListing(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "a.strm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := &safetyBranchRepo{branches: []*domain.StrmBranch{
		{ID: 1, TaskID: 1, ParentID: "base-id", Path: "/云影音", BranchType: domain.StrmBranchTypeBase},
		{ID: 2, TaskID: 1, ParentID: "movie-id", Path: "/云影音/电影", RelativePath: "电影", Recursive: true, BranchType: domain.StrmBranchTypeTemporary},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"base-id": {{ID: "readme-id", Name: "说明.txt", Size: 128}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:                 1,
		AccountID:          1,
		Path:               "/云影音",
		BranchCheckEnabled: true,
		ScanMode:           domain.StrmScanModeIncrementalUpdate,
		Extensions:         "mkv",
		OutputFolder:       "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, Branches: repo, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("基准目录非空且目标分支缺失时应视为明确删除：%+v", result)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 2 {
		t.Fatalf("明确消失的分支记录应在清理后删除：deleted=%v", repo.deleted)
	}
	if _, err := os.Stat(localDir); !os.IsNotExist(err) {
		t.Fatalf("明确消失的分支目录应同步删除，stat err=%v", err)
	}
}

// TestScanTaskManualAllowsEmptyCleanup 手动执行（全部/分支执行）视为用户确认：
// 即使远端识别 0 也放行清理，本地 STRM 正常删除，不触发保护。
func TestScanTaskManualAllowsEmptyCleanup(t *testing.T) {
	root := t.TempDir()
	localFile := filepath.Join(root, "任务", "影片.strm")
	if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localFile, []byte("https://example.test/video"), 0o644); err != nil {
		t.Fatal(err)
	}

	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}

	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("手动执行应视为确认、放行清理：%+v", result)
	}
	if _, err := os.Stat(localFile); !os.IsNotExist(err) {
		t.Fatalf("手动执行应删除本地 STRM，stat err=%v", err)
	}
}

// TestCleanupProtectReasonScale 规模判定：小规模放行，STRM 或目录达阈值即保护。
func TestCleanupProtectReasonScale(t *testing.T) {
	tests := []struct {
		name      string
		imp       cleanupImpact
		wantBlock bool
	}{
		{name: "小规模放行", imp: cleanupImpact{staleStrm: 50, staleDirs: 1}, wantBlock: false},
		{name: "STRM 达阈值", imp: cleanupImpact{staleStrm: strmDeleteThreshold, staleDirs: 0}, wantBlock: true},
		{name: "STRM 接近阈值放行", imp: cleanupImpact{staleStrm: strmDeleteThreshold - 1, staleDirs: 19}, wantBlock: false},
		{name: "目录达阈值", imp: cleanupImpact{staleStrm: 0, staleDirs: dirDeleteThreshold}, wantBlock: true},
		{name: "目录接近阈值放行", imp: cleanupImpact{staleStrm: 10, staleDirs: dirDeleteThreshold - 1}, wantBlock: false},
		{name: "大目录且部分STRM", imp: cleanupImpact{staleStrm: 300, staleDirs: 22}, wantBlock: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := cleanupProtectReason(tc.imp)
			if tc.wantBlock && reason == "" {
				t.Fatal("应阻止清理但未阻止")
			}
			if !tc.wantBlock && reason != "" {
				t.Fatalf("不应阻止清理，得到原因：%s", reason)
			}
		})
	}
}

// TestCollectCleanupImpact 统计待删 STRM（跨范围去重）与待删顶层目录数。
func TestCollectCleanupImpact(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("任务/电影/a.strm")
	write("任务/电影/b.strm")
	write("任务/电视剧/c.strm")
	scopes := []cleanupScope{
		{relDirs: []string{"电影"}, recursive: true},
		{relDirs: []string{"电视剧"}, recursive: true},
		{relDirs: []string{"电影"}, recursive: true}, // 重叠范围不应重复统计
	}
	seen := map[string]struct{}{"任务/电影/a.strm": {}}
	remoteChildren := map[string]map[string]struct{}{
		dirKey(nil): {SafeName("电视剧"): {}},
	}
	imp, err := collectCleanupImpact(root, "任务", scopes, nil, seen, remoteChildren)
	if err != nil {
		t.Fatal(err)
	}
	if imp.staleStrm != 2 {
		t.Fatalf("待删 STRM 应去重为 2，实际 %d", imp.staleStrm)
	}
	if imp.staleDirs != 1 {
		t.Fatalf("待删顶层目录应为 1（电影），实际 %d", imp.staleDirs)
	}
}

// TestScanTaskProtectsLargeEmptyCleanup 自动扫描大批量空结果：本地 1001 个 STRM、远端 0 → 保护，本地保留。
func TestScanTaskProtectsLargeEmptyCleanup(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "任务")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(strmDeleteThreshold)+1; i++ {
		if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("f%04d.strm", i)), []byte("https://example.test/v"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Protected {
		t.Fatal("大批量空结果应触发规模保护")
	}
	if result.RemovedCount != 0 {
		t.Fatalf("保护时应零删除，实际 %d", result.RemovedCount)
	}
	if _, err := os.Stat(filepath.Join(outDir, "f0000.strm")); err != nil {
		t.Fatalf("保护时本地 STRM 应保留：%v", err)
	}
}

// TestScanTaskManualAllowsLargeCleanup 手动执行视为确认：大批量清理放行。
func TestScanTaskManualAllowsLargeCleanup(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "任务")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(strmDeleteThreshold)+1; i++ {
		if err := os.WriteFile(filepath.Join(outDir, fmt.Sprintf("f%04d.strm", i)), []byte("https://example.test/v"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{"library": {}}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		ParentID:     "library",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		Extensions:   "mkv",
		OutputFolder: "任务",
	}
	result, err := ScanTask(context.Background(), task, ScanDeps{Files: files, StrmDir: root}, domain.StrmRunModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.Protected {
		t.Fatalf("手动执行应放行大批量清理：%+v", result)
	}
	if _, err := os.Stat(filepath.Join(outDir, "f0000.strm")); !os.IsNotExist(err) {
		t.Fatalf("手动执行应删除本地 STRM，stat err=%v", err)
	}
}

func TestOversizedPathFailureUsesUTF8BytesAndDirectoryPrefix(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("界", 86) // 258 字节
	relPath := filepath.Join("任务", longDir, "第01集.strm")
	gotPath, gotReason, oversized := oversizedPathFailure(relPath, false)
	if !oversized {
		t.Fatal("超长中文目录应按 UTF-8 字节数识别")
	}
	wantPath := filepath.Join("任务", longDir)
	if gotPath != wantPath {
		t.Fatalf("失败路径=%q，期望=%q", gotPath, wantPath)
	}
	if gotReason != pathTooLongDirReason {
		t.Fatalf("失败原因=%q，期望=%q", gotReason, pathTooLongDirReason)
	}
}

func TestPathComponentLimitAllowsExactly255Bytes(t *testing.T) {
	t.Parallel()

	allowed := strings.Repeat("a", 250) + ".strm"
	if pathHasOversizedComponent(filepath.Join("任务", allowed)) {
		t.Fatal("恰好 255 字节的文件名应允许")
	}
	tooLong := strings.Repeat("a", 251) + ".strm"
	if !pathHasOversizedComponent(filepath.Join("任务", tooLong)) {
		t.Fatal("256 字节的文件名应识别为超限")
	}
}

func TestFailureCollectorDeduplicatesOversizedDirectory(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	failures := NewFailureCollector()
	for _, name := range []string{"第01集.strm", "第02集.strm", "海报.jpg"} {
		kind := ScanFailureStrm
		if strings.HasSuffix(name, ".jpg") {
			kind = ScanFailureMetadata
		}
		addOversizedPathFailure(failures, kind, filepath.Join("任务", longDir, name), false)
	}

	items := failures.Items()
	if len(items) != 2 {
		t.Fatalf("相同超长目录应按类型各汇总一项，实际=%d：%v", len(items), items)
	}
	if items[0].Path != filepath.ToSlash(filepath.Join("任务", longDir)) {
		t.Fatalf("STRM 汇总路径=%q", items[0].Path)
	}
	if items[1].Kind != ScanFailureMetadata {
		t.Fatalf("第二项类型=%q，期望 metadata", items[1].Kind)
	}
}

func TestCleanupFunctionsIgnoreOversizedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	longDir := strings.Repeat("超", 86)
	failures := NewFailureCollector()

	removed, err := cleanupScopedStaleFiles(
		root,
		"任务",
		nil,
		[]cleanupScope{{relDirs: []string{longDir}, recursive: true}},
		nil,
		failures,
	)
	if err != nil {
		t.Fatalf("全量清理不应因超长目录失败：%v", err)
	}
	if removed != 0 {
		t.Fatalf("删除数=%d，期望=0", removed)
	}
	if failures.Len() != 1 {
		t.Fatalf("超长清理目录应记录一次，实际=%d", failures.Len())
	}

	if _, err := cleanupCurrentDirectoryStrm(root, "任务", []string{longDir}, nil, nil); err != nil {
		t.Fatalf("当前目录清理不应因超长目录失败：%v", err)
	}
}

func TestMigrateLegacyISOIgnoresOversizedPath(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	migrated, err := MigrateLegacyISOStrmFile(t.TempDir(), "任务", []string{longDir}, "影片.iso", "file-id", true)
	if err != nil {
		t.Fatalf("ISO 迁移不应访问超长路径：%v", err)
	}
	if migrated {
		t.Fatal("不存在的超长旧文件不应报告已迁移")
	}
}

func TestPendingMetadataRecordsOversizedDirectoryOnce(t *testing.T) {
	t.Parallel()

	longDir := strings.Repeat("长", 86)
	items := []metadataItem{
		{relPath: filepath.Join("任务", longDir, "poster.jpg")},
		{relPath: filepath.Join("任务", longDir, "movie.nfo")},
	}
	failures := NewFailureCollector()
	if got := pendingMetadataItems(t.TempDir(), items, failures); len(got) != 0 {
		t.Fatalf("超长元数据不应进入下载队列：%v", got)
	}
	if failures.Len() != 1 {
		t.Fatalf("同一超长元数据目录应汇总一次，实际=%d", failures.Len())
	}
	if got := failures.Items()[0]; got.Kind != ScanFailureMetadata || got.Reason != pathTooLongDirReason {
		t.Fatalf("元数据失败项=%+v", got)
	}
}

func TestClassifyScanFileSharedRules(t *testing.T) {
	exts := map[string]struct{}{"mkv": {}}
	metaExts := map[string]struct{}{"nfo": {}}
	relDirs := []string{"电视剧", "Season 1"}

	t.Run("media file", func(t *testing.T) {
		got := classifyScanFile("video-1", "第01集.mkv", "任务", 128, relDirs, exts, metaExts, 64, 1024, true)
		if !got.hasMedia || got.hasMetadata {
			t.Fatalf("媒体文件分类错误: %+v", got)
		}
		if got.media.fileID != "video-1" || got.media.fileName != "第01集.mkv" {
			t.Fatalf("媒体文件信息错误: %+v", got.media)
		}
	})

	t.Run("metadata file", func(t *testing.T) {
		got := classifyScanFile("meta-1", "tvshow.nfo", "任务", 32, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || !got.hasMetadata {
			t.Fatalf("元数据文件分类错误: %+v", got)
		}
		if got.metadata.relPath == "" || got.metadata.fileName != "tvshow.nfo" {
			t.Fatalf("元数据文件信息错误: %+v", got.metadata)
		}
	})

	t.Run("small media filtered", func(t *testing.T) {
		got := classifyScanFile("video-2", "第02集.mkv", "任务", 32, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("小媒体文件应被过滤: %+v", got)
		}
	})

	t.Run("large metadata filtered", func(t *testing.T) {
		got := classifyScanFile("meta-2", "movie.nfo", "任务", 2048, relDirs, exts, metaExts, 64, 1024, true)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("过大元数据文件应被过滤: %+v", got)
		}
	})

	t.Run("metadata sync disabled", func(t *testing.T) {
		got := classifyScanFile("meta-3", "movie.nfo", "任务", 32, relDirs, exts, metaExts, 64, 1024, false)
		if got.hasMedia || got.hasMetadata {
			t.Fatalf("关闭元数据同步后应忽略 nfo: %+v", got)
		}
	})
}

func TestCleanupScopedStaleFilesRemovesSameStemSidecarsOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepStrm := filepath.Join(dir, "保留.strm")
	staleStrm := filepath.Join(dir, "过期.strm")
	staleNFO := filepath.Join(dir, "过期.nfo")
	stalePoster := filepath.Join(dir, "过期-poster.jpg")
	staleThumb := filepath.Join(dir, "过期-thumb.jpg")
	folderNFO := filepath.Join(dir, "tvshow.nfo")
	folderPoster := filepath.Join(dir, "poster.jpg")
	keepNFO := filepath.Join(dir, "保留.nfo")
	for _, p := range []string{keepStrm, staleStrm, staleNFO, stalePoster, staleThumb, folderNFO, folderPoster, keepNFO} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]struct{}{
		"任务/电影/保留.strm": {},
	}
	removed, err := cleanupScopedStaleFiles(
		root,
		"任务",
		seen,
		[]cleanupScope{{relDirs: nil, recursive: true}},
		nil,
		NewFailureCollector(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	for _, p := range []string{staleStrm, staleNFO, stalePoster, staleThumb} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", filepath.Base(p))
		}
	}
	for _, p := range []string{keepStrm, keepNFO, folderNFO, folderPoster} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 应保留: %v", filepath.Base(p), err)
		}
	}
}

func TestCleanupMovedMediaSidecars(t *testing.T) {
	for _, keepVersion := range []bool{false, true} {
		t.Run(map[bool]string{false: "移走后删除空目录", true: "保留其他版本字幕"}[keepVersion], func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "任务", "旧目录")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			names := []string{"影片.strm", "影片-fanart.JPG", "影片.ass", "影片.zh-Hans.ass", "影片.en.forced.srt", "影片.idx", "影片.sub"}
			names = append(names, "poster.jpg", "fanart.jpg", "movie.nfo")
			seen := map[string]struct{}{}
			if keepVersion {
				names = append(names, "影片.加长版.strm", "影片.加长版.zh.ass")
				seen["任务/旧目录/影片.加长版.strm"] = struct{}{}
			}
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			n, err := cleanupScopedStaleFiles(root, "任务", seen, []cleanupScope{{recursive: true}}, nil, NewFailureCollector())
			if err != nil || n != 1 {
				t.Fatalf("清理结果 %d, %v", n, err)
			}
			if !keepVersion {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("旧目录仍然存在: %v", err)
				}
			} else {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 5 {
					t.Fatalf("应只保留另一版本及共用海报: %v, %v", entries, err)
				}
			}
		})
	}
}
