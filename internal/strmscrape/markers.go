package strmscrape

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	pendingMarkerName        = ".litepan-scrape-pending"
	manualCompleteMarkerName = ".litepan-scrape-complete"

	PendingRunning    = "running"
	PendingUpdating   = "updating"
	PendingIncomplete = "incomplete"
	PendingDoubt      = "doubt"
	// PendingDone 表示刮削已完结、无需继续追更；该状态只用来携带可选资源结论。
	PendingDone = "done"
	// PendingEnded 表示用户「设为完结」：不再自动刮削该目录，需手动重新刮削。
	PendingEnded = "ended"

	TVStateEnded    = "ended"
	TVStateUpdating = "updating"
)

// scrapeState 只落在 .litepan-scrape-pending，完结且无需记忆时删除该文件；
// 一个作品最多只有一个 .litepan 标记，「TMDB 没有可选资源」的结论也记在这里（status=done）。
type scrapeState struct {
	Status  string `json:"status,omitempty"` // running|updating|incomplete|doubt|done|ended
	EpLocal int    `json:"ep_local,omitempty"`
	EpTMDB  int    `json:"ep_tmdb,omitempty"`
	// 只记“TMDB 没有”不记“下载失败”，后者下轮要继续重试；手动刮削会重新请求并按最新结果覆写。
	NoBackdrop bool `json:"no_backdrop,omitempty"`
	NoLogo     bool `json:"no_logo,omitempty"`
	NoActors   bool `json:"no_actors,omitempty"`
}

func (s scrapeState) hasOptionalGap() bool {
	return s.NoBackdrop || s.NoLogo || s.NoActors
}

// terminal 表示不再是待刮削状态：done=已完结（带可选资源结论）、ended=用户设为完结。
func isTerminalState(status string) bool {
	return status == PendingDone || status == PendingEnded
}

// manualCompleteState 表示用户确认无需继续匹配 TMDB，不能靠“缺少 pending”推断，否则本地作品会反复待刮削。
type manualCompleteState struct {
	MediaType string `json:"media_type,omitempty"`
}

func workMarkerPath(g workGroup, name string) string {
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return stem + name
	}
	return filepath.Join(g.absDir, name)
}

func pendingMarkerPath(g workGroup) string {
	return workMarkerPath(g, pendingMarkerName)
}

// syncOptionalAssetState 按本次 TMDB 结果更新可选资源结论，与 pending 共用同一文件：真实 pending 不动，
// 终态按最新结果覆写；只在开关打开时记录，actorSkipped 表示 NFO 异常未补写演员也要记下结论。
func syncOptionalAssetState(g workGroup, cfg Settings, info tmdbInfo, actorSkipped bool) {
	st, ok := readPendingState(g)
	if ok && !isTerminalState(st.Status) {
		return
	}
	st.Status = PendingDone
	st.NoBackdrop = cfg.Fanart && strings.TrimSpace(info.BackdropPath) == ""
	st.NoLogo = cfg.ClearLogo && strings.TrimSpace(info.LogoPath) == ""
	st.NoActors = cfg.Actors && (len(info.Actors) == 0 || actorSkipped)
	if st.hasOptionalGap() {
		_ = writePendingState(g, st)
		return
	}
	clearPendingMarker(g)
}

func manualCompleteMarkerPath(g workGroup) string {
	return workMarkerPath(g, manualCompleteMarkerName)
}

func clearPendingMarker(g workGroup) {
	_ = os.Remove(pendingMarkerPath(g))
}

func clearEpisodePendingIfDisabled(g workGroup) {
	pending, ok := readPendingState(g)
	if !ok {
		return
	}
	if pending.Status == PendingUpdating || pending.Status == PendingIncomplete {
		clearPendingMarker(g)
	}
}

func writeManualComplete(g workGroup, mediaType string) error {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != MediaTypeTV && mediaType != MediaTypeMovie {
		mediaType = inferMediaType(g)
	}
	data, err := json.Marshal(manualCompleteState{MediaType: mediaType})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeMarkerFile(manualCompleteMarkerPath(g), data); err != nil {
		return err
	}
	clearPendingMarker(g)
	return nil
}

func readManualComplete(g workGroup) (manualCompleteState, bool) {
	data, err := os.ReadFile(manualCompleteMarkerPath(g))
	if err != nil {
		return manualCompleteState{}, false
	}
	var st manualCompleteState
	if json.Unmarshal(data, &st) != nil {
		return manualCompleteState{MediaType: inferMediaType(g)}, true
	}
	return st, true
}

func clearManualComplete(g workGroup) {
	_ = os.Remove(manualCompleteMarkerPath(g))
}

// scrapeMetadataKeywordRe 匹配常见刮削元数据图片名（海报/背景/缩略图等）。
var scrapeMetadataKeywordRe = regexp.MustCompile(`(?i)\b(poster|backdrop|fanart|folder|thumb|cover|season|banner|logo|landscape|keyart|clearart)\b`)

// isScrapedMetadataFile 判断文件名是否为刮削元数据（.nfo 或常见海报图），.strm 与字幕不算。
func isScrapedMetadataFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".nfo" {
		return true
	}
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return false
	}
	return scrapeMetadataKeywordRe.MatchString(name)
}

// clearScrapedMetadata 取消错误匹配时删除作品目录（含季/集子目录）的 .nfo 与海报图，保留 .strm 和字幕；
// 扁平单文件作品只清理该 strm 对应的元数据，避免误删同目录其它作品。
func clearScrapedMetadata(g workGroup) error {
	// 元数据被清掉后会重新刮削，旧的可选资源结论与终态一并清掉。
	clearPendingMarker(g)
	if g.flatFile != "" {
		return clearFlatScrapedMetadata(g)
	}
	return filepath.WalkDir(g.absDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !isScrapedMetadataFile(d.Name()) {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

// clearFlatScrapedMetadata 清理扁平单文件作品的元数据（stem.nfo 与 stem-*.图片）。
func clearFlatScrapedMetadata(g workGroup) error {
	stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
	if stem == "" {
		return nil
	}
	dir := filepath.Dir(g.flatFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, d := range entries {
		if d.IsDir() {
			continue
		}
		name := d.Name()
		if name == stem+".nfo" || (strings.HasPrefix(name, stem+"-") && isScrapedMetadataFile(name)) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func writePendingState(g workGroup, st scrapeState) error {
	if st.Status == "" {
		st.Status = PendingRunning
	}
	return writeJSONMarker(pendingMarkerPath(g), st)
}

func readPendingState(g workGroup) (scrapeState, bool) {
	return readJSONMarker(pendingMarkerPath(g))
}

func writeMarkerFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func writeJSONMarker(path string, st scrapeState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeMarkerFile(path, data)
}

func readJSONMarker(path string) (scrapeState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scrapeState{}, false
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "pending" {
		return scrapeState{Status: PendingRunning}, true
	}
	var st scrapeState
	if json.Unmarshal([]byte(raw), &st) != nil {
		return scrapeState{Status: PendingRunning}, true
	}
	if st.Status == "" {
		st.Status = PendingRunning
	}
	return st, true
}

// finalizeAfterScrape：按集数/存疑决定保留或删除 pending，并写回 ep_local/ep_tmdb。
func finalizeAfterScrape(g workGroup, mediaType string, epTMDB int, doubt bool) {
	epLocal, epScraped := countTVEpisodeProgress(g)
	st := scrapeState{EpLocal: epLocal, EpTMDB: epTMDB}
	if doubt {
		st.Status = PendingDoubt
		_ = writePendingState(g, st)
		return
	}
	if mediaType != MediaTypeTV || g.flatFile != "" {
		clearPendingMarker(g)
		return
	}
	if epTMDB > 0 && epLocal < epTMDB {
		st.Status = PendingUpdating
		_ = writePendingState(g, st)
		return
	}
	if epTMDB > 0 && epLocal > epTMDB {
		st.Status = PendingIncomplete
		_ = writePendingState(g, st)
		return
	}
	if epLocal > 0 && epScraped < epLocal {
		st.Status = PendingIncomplete
		_ = writePendingState(g, st)
		return
	}
	clearPendingMarker(g)
}

// markWorkNormal 用户「设为完结」时写终态 ended，之后不再自动刮削，需要时用「重新刮削」。
func markWorkNormal(g workGroup, mediaType string) error {
	if !workHasNFO(g, mediaType) || !workHasPoster(g, mediaType) {
		return errRootMetaIncomplete
	}
	st, _ := readPendingState(g)
	st.Status = PendingEnded
	return writePendingState(g, st)
}
