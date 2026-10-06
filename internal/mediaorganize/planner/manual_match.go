package planner

import (
	"fmt"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

type ManualMatchGroup struct {
	GroupUID  string
	MediaKind string
	DirID     string
	DirName   string
	Title     string
	SourceIDs []string
}

func (p *Planner) ReplanMatchedGroup(group ManualMatchGroup, raw map[string]any) (*moplan.Plan, error) {
	if strings.TrimSpace(group.GroupUID) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "当前计划中未找到该作品组")
	}
	if len(raw) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "TMDB 影片信息缺失")
	}
	if p.useTMDB && p.tmdb != nil && p.tmdbAPIKey() != "" {
		p.tmdbAvailable = true
	}
	entries, err := p.collectEntriesForManualMatch(group)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, domain.Errorf(domain.CodeValidation, "当前作品组未找到可重建的媒体文件")
	}
	groups, pending := p.groupEntries(entries)
	for _, ps := range pending {
		p.skip(ps.item, ps.reason)
	}
	alignDefaults := map[groupKey]map[bucketKey]map[string]any{}
	if p.alignMediaTags {
		alignDefaults = p.computeAlignDefaults(groups)
	}
	key, items, ok := locateManualMatchGroup(groups, group)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "当前计划中未找到该作品组")
	}
	bucketDefaults := alignDefaults[key]
	selectedKind := strings.ToLower(strings.TrimSpace(group.MediaKind))
	if selectedKind == "movie" || selectedKind == "tv" {
		// 手动选中的类型优先于自动猜测，否则改选后仍按旧类型重建。
		key.mediaKind = selectedKind
	}
	match := manualTMDBMatchResult(raw, key.mediaKind)
	// 人工选择的 TMDB 作品是最终依据：目录中的年份可能是当季年份，
	// 不能继续覆盖剧集首播年份。
	if match.title != "" {
		key.title = match.title
	}
	if match.year != nil {
		key.setYear(match.year)
	}
	p.recordManualMatchGroup(key, len(items))
	if err := p.planGroupWithMatch(key, items, bucketDefaults, &match, false); err != nil {
		return nil, err
	}
	p.planEmptyDirCleanup()
	return p.finalize(), nil
}

func (p *Planner) collectEntriesForManualMatch(group ManualMatchGroup) ([]batchEntry, error) {
	if group.DirID != "" {
		return p.collectEntriesUnderDir(group.DirID, group.DirName)
	}
	items, err := p.listWithRetry(p.parentID, "根目录")
	if err != nil {
		return nil, err
	}
	entries := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			entries = append(entries, batchEntry{item: item})
		}
	}
	return entries, nil
}

func (p *Planner) collectEntriesUnderDir(dirID, dirName string) ([]batchEntry, error) {
	items, err := p.listWithRetry(dirID, dirName)
	if err != nil {
		// 手动指定的目录读不到时直接报错。
		return nil, err
	}
	ancestors := []rules.Ancestor{{ID: dirID, Name: dirName}}
	p.recordDirMeta(ancestors)
	entries := make([]batchEntry, 0)
	for _, item := range items {
		if p.isMedia(item) {
			entries = append(entries, batchEntry{item: item, ancestors: cloneAncestors(ancestors)})
		}
	}
	for _, child := range items {
		if !child.IsDir {
			continue
		}
		next := append(cloneAncestors(ancestors), rules.Ancestor{ID: child.ID, Name: child.Name})
		if err := p.collectDescendants(child.ID, next, &entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func locateManualMatchGroup(groups map[groupKey][]batchEntry, group ManualMatchGroup) (groupKey, []batchEntry, bool) {
	if len(groups) == 0 {
		return groupKey{}, nil, false
	}
	if key, items, ok := locateManualMatchFiles(groups, group); ok {
		return key, items, true
	}
	for key, items := range groups {
		if groupUIDOf(key) == group.GroupUID {
			return key, items, true
		}
	}
	for key, items := range groups {
		if group.DirID != "" && key.dirID == group.DirID && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	for key, items := range groups {
		if strings.TrimSpace(group.DirName) != "" && key.dirName == group.DirName && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	for key, items := range groups {
		if strings.TrimSpace(group.Title) != "" && strings.TrimSpace(key.title) == strings.TrimSpace(group.Title) && sameMediaKind(key.mediaKind, group.MediaKind) {
			return key, items, true
		}
	}
	if len(groups) == 1 {
		for key, items := range groups {
			return key, items, true
		}
	}
	return groupKey{}, nil, false
}

// locateManualMatchFiles 使用初次计划记录的源文件集重建作品组。
// 人工选择是明确纠错，不应再受目录年份、自动类型或二次分组结果影响。
func locateManualMatchFiles(groups map[groupKey][]batchEntry, group ManualMatchGroup) (groupKey, []batchEntry, bool) {
	if len(group.SourceIDs) == 0 {
		return groupKey{}, nil, false
	}
	wanted := make(map[string]struct{}, len(group.SourceIDs))
	for _, sourceID := range group.SourceIDs {
		if sourceID = strings.TrimSpace(sourceID); sourceID != "" {
			wanted[sourceID] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return groupKey{}, nil, false
	}
	var selectedKey groupKey
	items := make([]batchEntry, 0, len(wanted))
	for key, candidates := range groups {
		for _, entry := range candidates {
			if _, ok := wanted[entry.item.ID]; !ok {
				continue
			}
			if len(items) == 0 {
				selectedKey = key
			}
			items = append(items, entry)
		}
	}
	if len(items) == 0 {
		return groupKey{}, nil, false
	}
	if group.DirID != "" {
		selectedKey.dirID = group.DirID
	}
	if group.DirName != "" {
		selectedKey.dirName = group.DirName
	}
	if group.Title != "" {
		selectedKey.title = group.Title
	}
	return selectedKey, items, true
}

func sameMediaKind(a, b string) bool {
	a = strings.TrimSpace(strings.ToLower(a))
	b = strings.TrimSpace(strings.ToLower(b))
	if a == "" || b == "" {
		return true
	}
	return a == b
}

func manualTMDBMatchResult(raw map[string]any, mediaKind string) tmdbMatchResult {
	groupMediaType := "movie"
	if strings.TrimSpace(strings.ToLower(mediaKind)) == "tv" {
		groupMediaType = "tv"
	}
	tmdbID, tmdbTitle, tmdbOriginal, tmdbYear := rules.ExtractTMDBDisplayFields(raw, groupMediaType)
	title := tmdbTitle
	if title == "" {
		title = tmdbOriginal
	}
	return tmdbMatchResult{
		tmdbID:       tmdbID,
		tmdbTitle:    tmdbTitle,
		tmdbOriginal: tmdbOriginal,
		title:        title,
		year:         tmdbYear,
		raw:          raw,
		confidence:   0.99,
	}
}

func (p *Planner) recordManualMatchGroup(key groupKey, count int) {
	p.diagnostics["groups"] = []map[string]any{{
		"media_kind": key.mediaKind,
		"dir_id":     key.dirID,
		"dir_name":   key.dirName,
		"title":      key.title,
		"count":      count,
		"group_uid":  groupUIDOf(key),
	}}
	p.log(fmt.Sprintf("[计划] 手动匹配重建作品组: 目录=%q | 标题=%q | %d个文件", key.dirName, key.title, count))
}
