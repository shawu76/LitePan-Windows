package mediaorganize

import (
	"encoding/json"
	"reflect"
	"testing"

	"litepan/internal/domain"
)

type rootDirectoryStub map[string][]string

func (s rootDirectoryStub) RootDirectories(mediaType string) []string {
	return append([]string(nil), s[mediaType]...)
}

func mustTaskConfig(t *testing.T, config map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTargetPathCandidatesIncludesClassificationRootForMove(t *testing.T) {
	svc := NewService(ServiceOptions{Classification: rootDirectoryStub{"tv": {"电视剧"}}})
	task := &domain.MediaOrganizeTask{Config: mustTaskConfig(t, map[string]any{
		"target_root": "/影视",
		"action_type": "move",
		"media_type":  "tv",
	})}

	got, err := svc.TargetPathCandidates(task)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/影视", "/影视/电视剧"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("候选落点 = %#v, want %#v", got, want)
	}
}

func TestTargetPathCandidatesDoesNotClassifyRename(t *testing.T) {
	svc := NewService(ServiceOptions{Classification: rootDirectoryStub{"tv": {"电视剧"}}})
	task := &domain.MediaOrganizeTask{Config: mustTaskConfig(t, map[string]any{
		"target_directory": "/影视",
		"action_type":      "rename",
		"media_type":       "tv",
	})}

	got, err := svc.TargetPathCandidates(task)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/影视"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("重命名任务不应附加分类目录: %#v", got)
	}
}
