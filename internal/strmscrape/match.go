package strmscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
)

type tmdbInfo struct {
	TMDBID       string
	Title        string
	Original     string
	Year         *int
	Plot         string
	PosterPath   string
	BackdropPath string
	Actors       []tmdbActor
	LogoPath     string
	MediaType    string
	Doubt        bool
	EpisodeCount int // 默认全剧集数；刮削时会按本地已有季收窄
}

type tmdbActor struct {
	Name        string
	Role        string
	ProfilePath string
	Order       int
}

func (s *Service) matchWork(ctx context.Context, client *tmdb.Client, g workGroup) (*tmdbInfo, error) {
	mediaType := inferMediaType(g)
	folderName := workDisplayName(g)
	dirParsed := rules.NormalizeParsedMedia(rules.ParseDirName(folderName))

	var fileParses []rules.ParsedMedia
	for _, e := range g.entries {
		stem := strings.TrimSuffix(filepath.Base(e.absPath), filepath.Ext(e.absPath))
		fileParses = append(fileParses, rules.NormalizeParsedMedia(rules.ParseFilenameStrict(stem+".mkv")))
	}
	if meta, ok := readWorkNFOMeta(g, mediaType); ok && strings.TrimSpace(meta.TMDBID) != "" {
		if info, err := lookupTMDBInfo(ctx, client, meta.TMDBID, mediaType); err == nil {
			return info, nil
		}
	}

	if id := rules.FindTMDBIDInName(folderName); id != "" {
		if info, err := lookupTMDBInfo(ctx, client, id, mediaType); err == nil {
			return info, nil
		}
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(filepath.Base(e.absPath), filepath.Ext(e.absPath))
		if id := rules.FindTMDBIDInName(stem); id != "" {
			if info, err := lookupTMDBInfo(ctx, client, id, mediaType); err == nil {
				return info, nil
			}
		}
	}

	title := strings.TrimSpace(dirParsed.Title)
	year := dirParsed.Year
	if title == "" {
		for _, p := range fileParses {
			if strings.TrimSpace(p.Title) != "" {
				title = strings.TrimSpace(p.Title)
				if year == nil {
					year = p.Year
				}
				break
			}
		}
	}
	if title == "" {
		title = folderName
	}
	if title == "" {
		return nil, fmt.Errorf("无法解析标题")
	}

	info, err := searchTMDBInfo(ctx, client, title, year, mediaType)
	if err != nil && mediaType == MediaTypeTV {
		// 误判成剧集时回退电影搜索
		info, err = searchTMDBInfo(ctx, client, title, year, MediaTypeMovie)
	}
	if err != nil {
		return nil, err
	}
	if info.EpisodeCount == 0 && info.MediaType == MediaTypeTV {
		if raw, lerr := client.Lookup(ctx, info.TMDBID, MediaTypeTV); lerr == nil {
			if full, derr := decodeTMDBInfo(raw, MediaTypeTV); derr == nil && full.EpisodeCount > 0 {
				info.EpisodeCount = full.EpisodeCount
			}
		}
	}
	return info, nil
}

func lookupTMDBInfo(ctx context.Context, client *tmdb.Client, id, mediaType string) (*tmdbInfo, error) {
	order := []string{mediaType}
	if mediaType == MediaTypeTV {
		order = append(order, MediaTypeMovie)
	} else {
		order = append(order, MediaTypeTV)
	}
	var lastErr error
	for _, mt := range order {
		raw, err := client.Lookup(ctx, id, mt)
		if err != nil {
			lastErr = err
			continue
		}
		info, derr := decodeTMDBInfo(raw, mt)
		if derr != nil {
			lastErr = derr
			continue
		}
		return &info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("TMDB 查询失败")
	}
	return nil, lastErr
}

func searchTMDBInfo(ctx context.Context, client *tmdb.Client, title string, year *int, mediaType string) (*tmdbInfo, error) {
	results, err := client.Search(ctx, title, year, mediaType)
	if err != nil {
		return nil, err
	}
	var best map[string]any
	doubt := false
	if year == nil {
		best, doubt = pickTMDBScrapeMatch(rules.RawJSONListToMaps(results), nil, mediaType, title)
	} else {
		// 带年份的第一次查询只接受完全相等；±1 年必须在不限年份的完整候选中判断唯一性。
		best = rules.PickTMDBSearchMatchForYear(rules.RawJSONListToMaps(results), year, mediaType, title)
	}
	if best == nil && year != nil {
		results, err = client.Search(ctx, title, nil, mediaType)
		if err != nil {
			return nil, err
		}
		best, doubt = pickTMDBScrapeMatch(rules.RawJSONListToMaps(results), year, mediaType, title)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("无搜索结果")
	}
	if best == nil {
		if year != nil {
			return nil, fmt.Errorf("没有标题相符且年份为 %d 或唯一相邻年份的结果", *year)
		}
		return nil, fmt.Errorf("没有标题相符的结果")
	}
	info, err := decodeTMDBInfo(mustRaw(best), mediaType)
	if err != nil {
		return nil, err
	}
	info.Doubt = doubt
	return &info, nil
}

func pickTMDBScrapeMatch(results []map[string]any, year *int, mediaType, title string) (map[string]any, bool) {
	if best := rules.PickTMDBSearchMatchForYear(results, year, mediaType, title); best != nil {
		return best, year == nil && len(results) > 1
	}
	if best := rules.PickUniqueTMDBAdjacentYearMatch(results, year, mediaType, title); best != nil {
		return best, true
	}
	return nil, false
}

func (s *Service) writeMatched(ctx context.Context, client *tmdb.Client, g workGroup, info tmdbInfo, overwrite bool) error {
	_, err := s.writeMatchedOpts(ctx, client, g, info, overwrite, s.GetSettings().EpisodeInfo)
	return err
}

func (s *Service) writeMatchedOpts(ctx context.Context, client *tmdb.Client, g workGroup, info tmdbInfo, overwrite, withTVExtras bool) (epTMDB int, err error) {
	cfg := s.GetSettings()
	mediaType := info.MediaType
	if mediaType == "" {
		mediaType = inferMediaType(g)
		info.MediaType = mediaType
	}
	if cfg.Fanart || cfg.Actors || cfg.ClearLogo {
		info, err = enrichTMDBExtras(ctx, client, info, cfg)
		if err != nil {
			return 0, err
		}
	}
	epTMDB = 0
	if withTVExtras {
		epTMDB = info.EpisodeCount
	}
	if withTVExtras && mediaType == MediaTypeTV && g.flatFile == "" && strings.TrimSpace(info.TMDBID) != "" {
		if n, cerr := tmdbEpisodeCountForLocalSeasons(ctx, client, g, info.TMDBID); cerr == nil && n > 0 {
			epTMDB = n
		}
	}
	epLocal, _ := countTVEpisodeProgress(g)
	if err := writePendingState(g, scrapeState{
		Status:  PendingRunning,
		EpLocal: epLocal,
		EpTMDB:  epTMDB,
	}); err != nil {
		return 0, err
	}
	needTVExtras := withTVExtras && mediaType == MediaTypeTV && g.flatFile == "" && strings.TrimSpace(info.TMDBID) != ""
	nfo, poster := workMetaPaths(g, mediaType)
	actors := buildNFOActors(client, info.Actors)
	actorSkipped := false
	// 目标 NFO 不存在或不是标准 NFO（如压制组信息文件）都重写；后者直接覆盖。
	if nfoWriteNeeded(overwrite, nfo) {
		if mediaType == MediaTypeTV {
			if err := writeTVShowNFO(nfo, info.Title, info.TMDBID, info.Plot, info.Year, actors...); err != nil {
				return 0, err
			}
		} else if err := writeMovieNFO(nfo, info.Title, info.TMDBID, info.Plot, info.Year, actors...); err != nil {
			return 0, err
		}
	} else if cfg.Actors {
		// 补写演员是可选项：NFO 结构异常时只警告跳过，不能中断整部作品（否则海报/背景图/Logo 也写不成）。
		if err := appendNFOActors(nfo, actors); err != nil {
			actorSkipped = true
			if s.log != nil {
				s.log.Warn("STRM 刮削补写演员信息失败，已跳过", "nfo", nfo, "err", err)
			}
		}
	}
	if (overwrite || !fileExists(poster)) && strings.TrimSpace(info.PosterPath) != "" {
		data, err := client.DownloadImage(ctx, info.PosterPath, "w500")
		if err != nil {
			return 0, err
		}
		if err := writeImageFile(poster, data); err != nil {
			return 0, err
		}
	}
	if cfg.Fanart && (overwrite || !workHasFanart(g)) && strings.TrimSpace(info.BackdropPath) != "" {
		if _, err := s.writeOptionalArtwork(ctx, client, info.BackdropPath, workFanartPath(g), "详情页背景图"); err != nil {
			return 0, err
		}
	}
	if cfg.ClearLogo && (overwrite || !workHasClearLogo(g)) && strings.TrimSpace(info.LogoPath) != "" {
		if _, err := s.writeOptionalArtwork(ctx, client, info.LogoPath, workClearLogoPath(g), "影片 Logo"); err != nil {
			return 0, err
		}
	}
	if withTVExtras && needTVExtras {
		if err := s.writeTVExtras(ctx, client, g, info, overwrite); err != nil {
			return epTMDB, fmt.Errorf("补写季/集元数据失败：%w", err)
		}
	}
	// 异步补季/集时由调用方 finalize；此处同步路径直接收尾
	if !withTVExtras {
		if info.Doubt {
			_ = writePendingState(g, scrapeState{Status: PendingDoubt})
		} else {
			clearPendingMarker(g)
		}
	} else if withTVExtras || !needTVExtras {
		finalizeAfterScrape(g, mediaType, epTMDB, info.Doubt)
	}
	// 记录本次 TMDB 是否根本没有可选资源，避免下一轮再次为同一部作品发请求。
	syncOptionalAssetState(g, cfg, info, actorSkipped)
	clearManualComplete(g)
	return epTMDB, nil
}

func enrichTMDBExtras(ctx context.Context, client *tmdb.Client, info tmdbInfo, cfg Settings) (tmdbInfo, error) {
	appendTo := []string{}
	if cfg.Actors {
		if info.MediaType == MediaTypeTV {
			appendTo = append(appendTo, "aggregate_credits")
		} else {
			appendTo = append(appendTo, "credits")
		}
	}
	raw, err := client.LookupWithAppend(ctx, info.TMDBID, info.MediaType, appendTo...)
	if err != nil {
		return info, fmt.Errorf("获取 TMDB 扩展信息：%w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return info, fmt.Errorf("解析 TMDB 扩展信息：%w", err)
	}
	if cfg.Fanart {
		info.BackdropPath = strings.TrimSpace(anyString(payload["backdrop_path"]))
	}
	if cfg.Actors {
		creditsKey := "credits"
		if info.MediaType == MediaTypeTV {
			creditsKey = "aggregate_credits"
		}
		info.Actors = decodeTMDBActors(payload[creditsKey], 20)
	}
	if cfg.ClearLogo {
		images, imageErr := client.FetchImages(ctx, info.TMDBID, info.MediaType)
		if imageErr != nil {
			return info, fmt.Errorf("获取 TMDB Logo：%w", imageErr)
		}
		info.LogoPath = selectTMDBLogo(images, cfg.TmdbLanguage, anyString(payload["original_language"]))
	}
	return info, nil
}

func selectTMDBLogo(raw json.RawMessage, preferred, original string) string {
	var payload struct {
		Logos []struct {
			FilePath string  `json:"file_path"`
			Language *string `json:"iso_639_1"`
		} `json:"logos"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	language := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if i := strings.IndexAny(value, "-_"); i >= 0 {
			value = value[:i]
		}
		return value
	}
	wantedLanguages := make([]string, 0, 4)
	seenLanguages := map[string]bool{}
	for _, value := range []string{language(preferred), language(original), "en"} {
		if value == "" || seenLanguages[value] {
			continue
		}
		seenLanguages[value] = true
		wantedLanguages = append(wantedLanguages, value)
	}
	for _, wanted := range append(wantedLanguages, "") {
		for _, logo := range payload.Logos {
			actual := ""
			if logo.Language != nil {
				actual = language(*logo.Language)
			}
			path := strings.TrimSpace(logo.FilePath)
			if actual == wanted && strings.EqualFold(filepath.Ext(path), ".png") {
				return path
			}
		}
	}
	for _, logo := range payload.Logos {
		path := strings.TrimSpace(logo.FilePath)
		if strings.EqualFold(filepath.Ext(path), ".png") {
			return path
		}
	}
	return ""
}

func decodeTMDBActors(raw any, limit int) []tmdbActor {
	credits, _ := raw.(map[string]any)
	cast, _ := credits["cast"].([]any)
	if limit <= 0 || limit > len(cast) {
		limit = len(cast)
	}
	out := make([]tmdbActor, 0, limit)
	for _, value := range cast {
		item, _ := value.(map[string]any)
		name := strings.TrimSpace(anyString(item["name"]))
		if name == "" {
			continue
		}
		role := strings.TrimSpace(anyString(item["character"]))
		if role == "" {
			if roles, _ := item["roles"].([]any); len(roles) > 0 {
				if first, _ := roles[0].(map[string]any); first != nil {
					role = strings.TrimSpace(anyString(first["character"]))
				}
			}
		}
		order := len(out)
		if parsed := asInt(item["order"]); parsed != nil {
			order = *parsed
		}
		out = append(out, tmdbActor{Name: name, Role: role, ProfilePath: strings.TrimSpace(anyString(item["profile_path"])), Order: order})
		if len(out) == limit {
			break
		}
	}
	return out
}

func buildNFOActors(client *tmdb.Client, actors []tmdbActor) []nfoActor {
	out := make([]nfoActor, 0, len(actors))
	for _, actor := range actors {
		out = append(out, nfoActor{Name: actor.Name, Role: actor.Role, Order: actor.Order, Thumb: client.ImageURL(actor.ProfilePath, "w185")})
	}
	return out
}

// tmdbEpisodeCountForLocalSeasons 按 finale 截断正片季，避免跨季绝对集号被误当总集数。
func tmdbEpisodeCountForLocalSeasons(ctx context.Context, client *tmdb.Client, g workGroup, tmdbID string) (int, error) {
	seasons := listLocalRegularSeasonNumbers(g)
	if client == nil || strings.TrimSpace(tmdbID) == "" || len(seasons) == 0 {
		return 0, fmt.Errorf("无本地正片季")
	}
	rawSeasons, err := client.FetchTVSeasons(ctx, tmdbID)
	if err != nil {
		return 0, err
	}
	fallback := tmdbSeasonEpisodeCountMap(rawSeasons)
	total := 0
	for _, sn := range seasons {
		n := 0
		if detail, derr := fetchSeasonDetail(ctx, client, tmdbID, sn); derr == nil {
			n = effectiveSeasonEpisodeCount(detail, fallback[sn])
		} else {
			n = fallback[sn]
		}
		if n > 0 {
			total += n
		}
	}
	if total <= 0 {
		return 0, fmt.Errorf("本地季在 TMDB 无集数")
	}
	return total, nil
}

func tmdbSeasonEpisodeCountMap(rawSeasons []json.RawMessage) map[int]int {
	out := map[int]int{}
	for _, raw := range rawSeasons {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		num := asInt(m["season_number"])
		ep := asInt(m["episode_count"])
		if num == nil || ep == nil || *num <= 0 || *ep <= 0 {
			continue
		}
		out[*num] = *ep
	}
	return out
}

// effectiveSeasonEpisodeCount 有 finale 时按集列表计数，否则保留 episode_count。
func effectiveSeasonEpisodeCount(detail *tmdbSeasonDetail, fallback int) int {
	fin := finaleEpisodeNumber(detail)
	if fin <= 0 {
		return fallback
	}
	n := 0
	for _, ep := range detail.Episodes {
		if ep.EpisodeNumber > 0 && ep.EpisodeNumber <= fin {
			n++
		}
	}
	if n > 0 {
		return n
	}
	return fallback
}

func finaleEpisodeNumber(detail *tmdbSeasonDetail) int {
	if detail == nil {
		return 0
	}
	best := 0
	for _, ep := range detail.Episodes {
		if ep.EpisodeType != "finale" || ep.EpisodeNumber <= 0 {
			continue
		}
		if ep.EpisodeNumber > best {
			best = ep.EpisodeNumber
		}
	}
	return best
}

func (s *Service) writeSeasonPosters(ctx context.Context, client *tmdb.Client, g workGroup, tmdbID string, overwrite bool, seasonDirs []seasonDir) error {
	showDir := g.absDir
	seasons := seasonNumbersFromDirs(seasonDirs)
	if len(seasons) == 0 {
		return nil
	}
	rawSeasons, err := client.FetchTVSeasons(ctx, tmdbID)
	if err != nil {
		return err
	}
	byNum := map[int]string{}
	for _, raw := range rawSeasons {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		num := asInt(m["season_number"])
		if num == nil {
			continue
		}
		poster := strings.TrimSpace(anyString(m["poster_path"]))
		if poster == "" {
			continue
		}
		byNum[*num] = poster
	}
	for _, season := range seasons {
		posterPath := byNum[season]
		if posterPath == "" {
			continue
		}
		out := seasonPosterPath(showDir, season)
		if !overwrite && fileExists(out) {
			continue
		}
		if _, err := s.writeOptionalArtwork(ctx, client, posterPath, out, fmt.Sprintf("第 %d 季海报", season)); err != nil {
			return err
		}
	}
	return nil
}

func asInt(v any) *int {
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n
	case int:
		return &t
	case int64:
		n := int(t)
		return &n
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return nil
		}
		n := int(i)
		return &n
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return nil
		}
		return &i
	default:
		return nil
	}
}

func decodeTMDBInfo(raw json.RawMessage, mediaType string) (tmdbInfo, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return tmdbInfo{}, err
	}
	id, title, original, year := rules.ExtractTMDBDisplayFields(m, mediaType)
	plot := strings.TrimSpace(anyString(m["overview"]))
	poster := strings.TrimSpace(anyString(m["poster_path"]))
	backdrop := strings.TrimSpace(anyString(m["backdrop_path"]))
	if id == "" || title == "" {
		return tmdbInfo{}, fmt.Errorf("TMDB 结果缺少标题")
	}
	epCount := 0
	if n := asInt(m["number_of_episodes"]); n != nil && *n > 0 {
		epCount = *n
	}
	return tmdbInfo{
		TMDBID:       id,
		Title:        title,
		Original:     original,
		Year:         year,
		Plot:         plot,
		PosterPath:   poster,
		BackdropPath: backdrop,
		MediaType:    mediaType,
		EpisodeCount: epCount,
	}, nil
}

func mustRaw(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

func (s *Service) newTMDBClient() *tmdb.Client {
	cfg := s.GetSettings()
	apiKey := strings.TrimSpace(cfg.TmdbAPIKey)
	if apiKey == "" {
		return nil
	}
	proxy := tmdb.BuildProxyURL(tmdb.ProxyConfig{
		Enabled:  cfg.ProxyEnabled,
		URL:      cfg.ProxyURL,
		Username: cfg.ProxyUsername,
		Password: cfg.ProxyPassword,
	})
	return tmdb.NewClient(tmdb.Options{
		APIKey:         apiKey,
		Language:       cfg.TmdbLanguage,
		ProxyURL:       proxy,
		Timeout:        20 * time.Second,
		MaxRetries:     2,
		RetryBaseDelay: time.Second,
		APIBaseHost:    cfg.TmdbAPIHost,
		ImageBaseHost:  cfg.TmdbImageHost,
	})
}
