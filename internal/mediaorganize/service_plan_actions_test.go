package mediaorganize

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"litepan/internal/domain"
)

func newPlanTestService(t *testing.T, taskID string) (*Service, *lifecycleTaskRepo) {
	t.Helper()
	repo := &lifecycleTaskRepo{tasks: map[string]*domain.MediaOrganizeTask{
		taskID: {
			ID:        taskID,
			AccountID: 1,
			Config:    json.RawMessage(`{"action_type":"rename","rename_marker":"off"}`),
			Status:    domain.MediaOrganizeStatusIdle,
		},
	}}
	svc := NewService(ServiceOptions{
		Repo:    repo,
		DataDir: t.TempDir(),
		Planner: &blockingPlanner{started: make(chan struct{}), release: make(chan struct{})},
	})
	return svc, repo
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestDeletePlanActionsOnlyRemovesEligibleAndSavesWhenNeeded(t *testing.T) {
	const taskID = "plan-actions"
	svc, _ := newPlanTestService(t, taskID)

	plan := &Plan{TaskID: taskID, Actions: []PlanAction{
		{ID: "a1", Kind: ActionKindRelocate},
		{ID: "a2", Kind: ActionKindRelocate, Status: "done"},
		{ID: "a3", Kind: "ensure_dir"},
		{ID: "a4", Kind: ActionKindRelocate},
	}}
	if err := svc.savePlan(taskID, plan); err != nil {
		t.Fatalf("savePlan: %v", err)
	}

	res, err := svc.DeletePlanActions(taskID, []string{"a1", "a2", "a3", "a4"})
	if err != nil {
		t.Fatalf("DeletePlanActions: %v", err)
	}
	removed, _ := res["removed"].([]string)
	if len(removed) != 2 || removed[0] != "a1" || removed[1] != "a4" {
		t.Fatalf("removed = %v, want [a1 a4]（顺序应跟随 plan.Actions）", removed)
	}
	skipped, _ := res["skipped"].([]string)
	if len(skipped) != 2 || !containsString(skipped, "a2") || !containsString(skipped, "a3") {
		t.Fatalf("skipped = %v, want {a2,a3}", skipped)
	}

	saved, err := svc.loadPlan(taskID)
	if err != nil {
		t.Fatalf("loadPlan: %v", err)
	}
	if len(saved.Actions) != 2 || saved.Actions[0].ID != "a2" || saved.Actions[1].ID != "a3" {
		t.Fatalf("落盘计划应只保留 a2/a3，实际 %+v", saved.Actions)
	}

	// 全部不可删：removed 为空，且不得重写计划文件（状态/内容都不变）
	path := svc.planPath(taskID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取计划文件: %v", err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	res2, err := svc.DeletePlanActions(taskID, []string{"a2", "a3"})
	if err != nil {
		t.Fatalf("DeletePlanActions(不可删): %v", err)
	}
	if r, _ := res2["removed"].([]string); len(r) != 0 {
		t.Fatalf("removed 应为空，实际 %v", r)
	}
	after, _ := os.ReadFile(path)
	infoAfter, _ := os.Stat(path)
	if string(before) != string(after) {
		t.Fatal("removed 为空时不应重写计划文件（内容已变）")
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("removed 为空时不应重写计划文件（mtime 已变）")
	}
}

func TestPrepareRunRejectsRunningTaskAndReleasesAfterFinish(t *testing.T) {
	const taskID = "running-guard"
	svc, _ := newPlanTestService(t, taskID)
	ctx := context.Background()

	if !svc.beginRun(taskID, 0) {
		t.Fatal("beginRun 应成功")
	}
	t.Cleanup(func() { svc.finishRun(taskID) })

	calls := []struct {
		name string
		fn   func(context.Context, string) (map[string]any, error)
	}{
		{"ApplyTask", svc.ApplyTask},
		{"RunTask", svc.RunTask},
	}
	for _, c := range calls {
		if _, err := c.fn(ctx, taskID); err == nil || !strings.Contains(err.Error(), "正在执行") {
			t.Errorf("%s 在任务运行中应被拒绝，err=%v", c.name, err)
		}
	}

	svc.finishRun(taskID)
	if _, err := svc.ApplyTask(ctx, taskID); err == nil {
		t.Error("ApplyTask 在无计划时应报错")
	} else if strings.Contains(err.Error(), "正在执行") {
		t.Errorf("运行登记释放后不应再报执行中，err=%v", err)
	}

	// 不存在的任务
	if _, err := svc.ApplyTask(ctx, "missing-task"); err == nil {
		t.Error("不存在的任务应报错")
	} else if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("不存在的任务错误信息异常: %v", err)
	}
}
