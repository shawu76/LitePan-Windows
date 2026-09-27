package mediaorganize

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"litepan/internal/domain"
)

type lifecycleTaskRepo struct {
	mu    sync.Mutex
	tasks map[string]*domain.MediaOrganizeTask
}

func (r *lifecycleTaskRepo) Create(_ context.Context, task *domain.MediaOrganizeTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *task
	r.tasks[task.ID] = &copy
	return nil
}

func (r *lifecycleTaskRepo) Update(_ context.Context, task *domain.MediaOrganizeTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[task.ID]; ok {
		copy := *task
		r.tasks[task.ID] = &copy
	}
	return nil
}

func (r *lifecycleTaskRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	return nil
}

func (r *lifecycleTaskRepo) Get(_ context.Context, id string) (*domain.MediaOrganizeTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[id]
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "任务不存在")
	}
	copy := *task
	return &copy, nil
}

func (r *lifecycleTaskRepo) List(context.Context) ([]*domain.MediaOrganizeTask, error) {
	return nil, nil
}

func (r *lifecycleTaskRepo) ListByAccount(context.Context, int64) ([]*domain.MediaOrganizeTask, error) {
	return nil, nil
}

type blockingPlanner struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingPlanner) Build(_ context.Context, taskID string, _ *domain.MediaOrganizeTask, _ map[string]any, _ map[string]any, _ PlannerHooks) (*Plan, error) {
	close(p.started)
	<-p.release
	return &Plan{TaskID: taskID, Diagnostics: map[string]any{}}, nil
}

func TestDeleteTaskDuringPlanningRemovesTaskAndPlan(t *testing.T) {
	const taskID = "planning-delete"
	repo := &lifecycleTaskRepo{tasks: map[string]*domain.MediaOrganizeTask{
		taskID: {
			ID:        taskID,
			AccountID: 1,
			Config:    json.RawMessage(`{"action_type":"rename","rename_marker":"off"}`),
			Status:    domain.MediaOrganizeStatusIdle,
		},
	}}
	planner := &blockingPlanner{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(ServiceOptions{Repo: repo, DataDir: t.TempDir(), Planner: planner})

	planDone := make(chan error, 1)
	go func() {
		_, err := svc.PlanTask(context.Background(), taskID)
		planDone <- err
	}()
	<-planner.started

	stopping, err := svc.DeleteTask(context.Background(), taskID)
	if err != nil || !stopping {
		t.Fatalf("规划期删除结果 stopping=%v err=%v", stopping, err)
	}
	close(planner.release)
	if err := <-planDone; err != nil {
		t.Fatalf("规划退出失败: %v", err)
	}
	if _, err := repo.Get(context.Background(), taskID); err == nil {
		t.Fatal("规划结束后任务记录仍存在")
	}
	if _, err := os.Stat(svc.planPath(taskID)); !os.IsNotExist(err) {
		t.Fatalf("规划结束后仍存在计划文件: %v", err)
	}
}
