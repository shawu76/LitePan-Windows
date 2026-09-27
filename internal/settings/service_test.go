package settings

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

type memoryConfigRepo struct {
	values map[string]string
}

func (r *memoryConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}

func (r *memoryConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memoryConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestStringAllowEmptyDistinguishesUnsetAndExplicitEmpty(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("未设置时=%q，期望默认值 vidhub", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyQuarkTVProxyClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "" {
		t.Fatalf("显式清空后=%q，期望保留空值", got)
	}
	if got := svc.String(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("原 String 语义不应改变，实际=%q", got)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "Infuse" {
		t.Fatalf("飞牛直读客户端未设置时=%q，期望默认值 Infuse", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyFnosDirectSTRMClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "" {
		t.Fatalf("飞牛直读客户端显式清空后=%q，期望保留空值", got)
	}
}

// 信息条开合这类界面偏好：能持久化、默认关闭、且不写「系统设置已更新」日志。
func TestUIBandHiddenPrefsArePersistedAndSilent(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	svc.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))

	for _, key := range []string{KeyUIBandHiddenStrm, KeyUIBandHiddenCache, KeyUIBandHiddenOrganize, KeyUIBandHiddenFuse} {
		spec := svc.byKey[key]
		if spec == nil {
			t.Fatalf("界面偏好 %s 未注册", key)
		}
		if spec.Type != TypeBool || spec.Default != "false" || !spec.Hidden {
			t.Fatalf("%s 声明不符：type=%s default=%s hidden=%v", key, spec.Type, spec.Default, spec.Hidden)
		}
	}

	if err := svc.Update(context.Background(), map[string]string{KeyUIBandHiddenStrm: "true"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Bool(KeyUIBandHiddenStrm); !got {
		t.Fatalf("写入后=%v，期望 true", got)
	}
	if value := repo.values[KeyUIBandHiddenStrm]; value != "true" {
		t.Fatalf("落库值=%q，期望 true", value)
	}
	if logs.Len() != 0 {
		t.Fatalf("界面偏好不应写设置变更日志，实际：%s", logs.String())
	}

	// 普通设置仍应记日志，确认静默逻辑只针对界面偏好。
	if err := svc.Update(context.Background(), map[string]string{KeyLogLevel: "warn"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "系统设置已更新") {
		t.Fatalf("普通设置应写变更日志，实际：%s", logs.String())
	}
}

// Hidden 键默认不出现在快照里，但 SnapshotAll 要带上（界面偏好靠它读回）。
func TestSnapshotAllIncludesHiddenKeys(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{KeyUIBandHiddenStrm: "true", KeyEmbyEnabled: "true"}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	plain := svc.Snapshot()
	for _, item := range plain.Items {
		if item.Key == KeyUIBandHiddenStrm || item.Key == KeyEmbyEnabled {
			t.Fatalf("默认快照不应包含 Hidden 键：%s", item.Key)
		}
	}

	all := svc.SnapshotAll()
	found := map[string]string{}
	for _, item := range all.Items {
		found[item.Key] = item.Value
	}
	if found[KeyUIBandHiddenStrm] != "true" {
		t.Fatalf("SnapshotAll 缺少界面偏好键，实得=%q", found[KeyUIBandHiddenStrm])
	}
	if _, ok := found[KeyEmbyEnabled]; !ok {
		t.Fatal("SnapshotAll 应包含其它 Hidden 键")
	}
}
