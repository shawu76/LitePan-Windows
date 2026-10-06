package mediaorganize

import (
	"path"
	"strings"

	"litepan/internal/domain"
)

// TargetPathCandidates 返回整理任务可能的实际落点。
// 分类整理会在 move 目标根下再创建一级分类目录。
func (s *Service) TargetPathCandidates(task *domain.MediaOrganizeTask) ([]string, error) {
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(stringFromAny(cfg["target_root"]))
	if target == "" {
		target = strings.TrimSpace(stringFromAny(cfg["target_directory"]))
	}
	if target == "" {
		return nil, domain.Errorf(domain.CodeValidation, "整理任务未配置目标目录")
	}

	candidates := []string{target}
	actionType := strings.ToLower(strings.TrimSpace(stringFromAny(cfg["action_type"])))
	if actionType == "" {
		actionType = "move"
	}
	if actionType != "move" || s.classification == nil {
		return candidates, nil
	}
	mediaType := strings.ToLower(strings.TrimSpace(stringFromAny(cfg["media_type"])))
	for _, dir := range s.classification.RootDirectories(mediaType) {
		if name := strings.TrimSpace(dir); name != "" {
			candidates = append(candidates, path.Join(target, name))
		}
	}
	return candidates, nil
}
