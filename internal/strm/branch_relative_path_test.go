package strm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/file"
	"litepan/internal/store"
)

// recordingBranchRepo 记录被创建的分支，用于断言入库的 RelativePath。
type recordingBranchRepo struct {
	branches []*domain.StrmBranch
	created  []*domain.StrmBranch
}

func (r *recordingBranchRepo) Create(_ context.Context, b *domain.StrmBranch) (int64, error) {
	r.created = append(r.created, b)
	return int64(len(r.created)), nil
}
func (r *recordingBranchRepo) Update(context.Context, *domain.StrmBranch) error { return nil }
func (r *recordingBranchRepo) Delete(context.Context, int64) error              { return nil }
func (r *recordingBranchRepo) Get(context.Context, int64) (*domain.StrmBranch, error) {
	return nil, nil
}
func (r *recordingBranchRepo) ListByTask(context.Context, int64) ([]*domain.StrmBranch, error) {
	return r.branches, nil
}
func (r *recordingBranchRepo) DeleteExpired(context.Context, int64) (int, error) { return 0, nil }

func mustBranchTestTask(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name: "测试账号", DriverType: "localfs", IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StrmTasks.Create(ctx, &domain.StrmTask{
		Name: "云影音", AccountID: accountID, ParentID: "library",
		Path: "/云影音", Status: domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return taskID
}

// 监控分支的相对路径必须按「净化后的目录段」拼。
// 目录名自带斜杠时（如一个名为 abc/def/ghi 的目录），
// 用原始名字拼字符串会把斜杠当成层级，读回来就被拆成多层本地目录。
func TestAutoBranchRelativePathSanitizesSlashDirName(t *testing.T) {
	repo := &recordingBranchRepo{branches: []*domain.StrmBranch{
		{ID: 1, TaskID: 1, ParentID: "base-id", Path: "/云影音", BranchType: domain.StrmBranchTypeBase},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"base-id":  {{ID: "weird-id", Name: "abc/def/ghi", IsDir: true}},
		"weird-id": {{ID: "season-id", Name: "Season 1", IsDir: true}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, Path: "/云影音", ParentID: "base-id",
		BranchCheckEnabled: true, ScanMode: domain.StrmScanModeIncrementalUpdate,
		Extensions: "mkv", OutputFolder: "任务",
	}
	if _, err := ScanTask(context.Background(), task, ScanDeps{
		Files: files, Branches: repo, StrmDir: t.TempDir(),
	}, domain.StrmRunModeAuto); err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应自动创建 1 个监控分支，实际 %d 个：%+v", len(repo.created), repo.created)
	}
	if got := repo.created[0].RelativePath; got != "abc_def_ghi" {
		t.Fatalf("自动分支相对路径=%q，期望 abc_def_ghi（名字里的斜杠不能被当成层级）", got)
	}
}

// 端到端：分支指向一个名字自带斜杠的目录时，本地只能生成一层目录。
func TestBaseBranchSlashDirNameWritesSingleLocalFolder(t *testing.T) {
	root := t.TempDir()
	repo := &recordingBranchRepo{branches: []*domain.StrmBranch{
		{ID: 2, TaskID: 1, ParentID: "weird-id", Path: "/云影音/abc/def/ghi",
			RelativePath: "abc_def_ghi", Recursive: true, BranchType: domain.StrmBranchTypeBase},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"weird-id": {{ID: "f1", Name: "影片.mkv", Size: 1024}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, Path: "/云影音", BranchCheckEnabled: true,
		ScanMode: domain.StrmScanModeIncrementalUpdate, Extensions: "mkv", OutputFolder: "任务",
	}
	res, err := ScanTask(context.Background(), task, ScanDeps{
		Files: files, Branches: repo, StrmDir: root,
	}, domain.StrmRunModeBranch)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if res.GeneratedCount != 1 {
		t.Fatalf("应生成 1 个 STRM，实际 %d", res.GeneratedCount)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "abc_def_ghi", "影片.strm")); err != nil {
		t.Fatalf("应在 任务/abc_def_ghi/ 下生成 STRM：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "abc")); !os.IsNotExist(err) {
		t.Fatalf("不应把目录名里的斜杠当成层级生成 任务/abc，stat err=%v", err)
	}
}

// 普通的多层目录不能被误合并成一层。
func TestCreateBranchKeepsRealDirLayers(t *testing.T) {
	svc, st := testService(t)
	svc.branches = st.StrmBranches
	taskID := mustBranchTestTask(t, st)

	branch, err := svc.CreateBranch(context.Background(), &domain.StrmBranch{
		TaskID: taskID, ParentID: "movies", Path: "/云影音/电影/2024",
		BranchType: domain.StrmBranchTypeBase,
	}, []string{"电影", "2024"})
	if err != nil {
		t.Fatal(err)
	}
	if branch.RelativePath != "电影/2024" {
		t.Fatalf("两层目录的相对路径=%q，期望 电影/2024", branch.RelativePath)
	}
}

// 目录名自带斜杠时，后端应按传上来的目录段数组存相对路径。
func TestCreateBranchUsesRelativeDirSegments(t *testing.T) {
	svc, st := testService(t)
	svc.branches = st.StrmBranches
	taskID := mustBranchTestTask(t, st)

	branch, err := svc.CreateBranch(context.Background(), &domain.StrmBranch{
		TaskID: taskID, ParentID: "weird", Path: "/云影音/abc/def/ghi",
		BranchType: domain.StrmBranchTypeBase,
	}, []string{"abc/def/ghi"})
	if err != nil {
		t.Fatal(err)
	}
	if branch.RelativePath != "abc_def_ghi" {
		t.Fatalf("相对路径=%q，期望 abc_def_ghi（一个目录名里的斜杠不能拆成三层）", branch.RelativePath)
	}

	// 老客户端不传数组时，保持原来的按显示路径相减行为。
	legacy, err := svc.CreateBranch(context.Background(), &domain.StrmBranch{
		TaskID: taskID, ParentID: "one-piece", Path: "/云影音/电视剧/海贼王",
		BranchType: domain.StrmBranchTypeBase,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.RelativePath != "电视剧/海贼王" {
		t.Fatalf("未传目录段时相对路径=%q，期望 电视剧/海贼王", legacy.RelativePath)
	}
}

// 只改保留天数不能把已经按目录段存好的相对路径覆盖掉。
func TestUpdateBranchRetentionKeepsSanitizedRelativePath(t *testing.T) {
	svc, st := testService(t)
	svc.branches = st.StrmBranches
	taskID := mustBranchTestTask(t, st)

	branch, err := svc.CreateBranch(context.Background(), &domain.StrmBranch{
		TaskID: taskID, ParentID: "weird", Path: "/云影音/abc/def/ghi",
		BranchType: domain.StrmBranchTypeBase,
	}, []string{"abc/def/ghi"})
	if err != nil {
		t.Fatal(err)
	}

	days := 7
	updated, err := svc.UpdateBranch(context.Background(), taskID, branch.ID, BranchPatch{RetentionDays: &days}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RelativePath != "abc_def_ghi" {
		t.Fatalf("仅改保留天数后相对路径被改成 %q", updated.RelativePath)
	}
}

// 净化后的相对路径要能通过监控分支校验，否则扫描会被直接停止。
func TestValidateMonitorBranchesAcceptsSanitizedRelativePath(t *testing.T) {
	task := &domain.StrmTask{Path: "/云影音"}

	sanitized := &domain.StrmBranch{
		ParentID: "weird", Path: "/云影音/abc/def/ghi",
		RelativePath: "abc_def_ghi", BranchType: domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{sanitized}); err != nil {
		t.Fatalf("净化后的相对路径应被接受：%v", err)
	}

	layered := &domain.StrmBranch{
		ParentID: "movies", Path: "/云影音/电影/2024",
		RelativePath: "电影/2024", BranchType: domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{layered}); err != nil {
		t.Fatalf("多层目录的相对路径应被接受：%v", err)
	}

	broken := &domain.StrmBranch{
		ParentID: "movies", Path: "/云影音/电影/2024",
		RelativePath: "完全对不上的路径", BranchType: domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{broken}); err == nil {
		t.Fatal("相对路径与显示路径确实不一致时仍应拦住")
	}
}

// 嵌套场景：斜杠目录在分支路径的中间（分支加在上层目录的下面）。
// 期望值 剧集/abc/def/ghi 与库里的 剧集/abc_def_ghi 必须被认为一致，
// 否则修好的分支会被校验拦停、扫描直接不跑。
func TestValidateMonitorBranchesAcceptsNestedSanitizedRelativePath(t *testing.T) {
	task := &domain.StrmTask{Path: "/云影音"}
	nested := &domain.StrmBranch{
		ParentID: "weird-id", Path: "/云影音/剧集/abc/def/ghi",
		RelativePath: "剧集/abc_def_ghi", BranchType: domain.StrmBranchTypeTemporary,
	}
	if err := validateMonitorBranches(task, []*domain.StrmBranch{nested}); err != nil {
		t.Fatalf("嵌套的净化相对路径应被接受：%v", err)
	}
}

// 端到端：临时分支落在斜杠目录下时，扫描不能被校验拦停，且只生成一层目录。
func TestTempBranchUnderSlashDirScansWithoutValidationError(t *testing.T) {
	root := t.TempDir()
	repo := &recordingBranchRepo{branches: []*domain.StrmBranch{
		{ID: 2, TaskID: 1, ParentID: "weird-id", Path: "/云影音/剧集/abc/def/ghi",
			RelativePath: "剧集/abc_def_ghi", Recursive: true,
			BranchType: domain.StrmBranchTypeTemporary, RetentionDays: 30},
	}}
	drv := &metadataTestDriver{items: map[string][]domain.FileItem{
		"weird-id": {{ID: "f1", Name: "影片.mkv", Size: 1024}},
	}}
	files := file.NewService(driverexec.New(metadataTestProvider{drv: drv}, nil), nil, nil, nil, nil, nil)
	task := &domain.StrmTask{
		ID: 1, AccountID: 1, Path: "/云影音", BranchCheckEnabled: true,
		ScanMode: domain.StrmScanModeIncrementalUpdate, Extensions: "mkv", OutputFolder: "任务",
	}
	if _, err := ScanTask(context.Background(), task, ScanDeps{
		Files: files, Branches: repo, StrmDir: root,
	}, domain.StrmRunModeBranch); err != nil {
		t.Fatalf("斜杠目录下的临时分支不应让扫描报错：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "任务", "剧集", "abc_def_ghi", "影片.strm")); err != nil {
		t.Fatalf("应生成 任务/剧集/abc_def_ghi/影片.strm：%v", err)
	}
}
