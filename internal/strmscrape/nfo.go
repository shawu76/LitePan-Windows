package strmscrape

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type workNFO struct {
	XMLName xml.Name
	Title   string     `xml:"title"`
	Year    string     `xml:"year,omitempty"`
	TMDBID  string     `xml:"tmdbid,omitempty"`
	Plot    string     `xml:"plot,omitempty"`
	Actors  []nfoActor `xml:"actor,omitempty"`
}

type nfoActor struct {
	XMLName xml.Name `xml:"actor"`
	Name    string   `xml:"name"`
	Role    string   `xml:"role,omitempty"`
	Order   int      `xml:"order"`
	Thumb   string   `xml:"thumb,omitempty"`
}

type seasonNFO struct {
	XMLName      xml.Name `xml:"season"`
	Title        string   `xml:"title,omitempty"`
	SeasonNumber string   `xml:"seasonnumber"`
	Plot         string   `xml:"plot,omitempty"`
	Premiered    string   `xml:"premiered,omitempty"`
}

type episodeNFO struct {
	XMLName   xml.Name `xml:"episodedetails"`
	Title     string   `xml:"title"`
	Season    string   `xml:"season"`
	Episode   string   `xml:"episode"`
	Plot      string   `xml:"plot,omitempty"`
	Aired     string   `xml:"aired,omitempty"`
	TMDBID    string   `xml:"tmdbid,omitempty"`
	ShowTitle string   `xml:"showtitle,omitempty"`
}

var nfoRootCloseRe = regexp.MustCompile(`(?i)</(?:movie|tvshow)\s*>`)

// nfoLooksStandard 文件含 movie/tvshow 根节点才算可用 NFO，压制组的 MediaInfo 文本不算。
func nfoLooksStandard(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "<movie") || strings.Contains(lower, "<tvshow")
}

// nfoWriteNeeded 目标 NFO 不存在或不是标准 NFO 时都要重写，压制组文本对 Kodi/Emby 无用。
func nfoWriteNeeded(overwrite bool, nfo string) bool {
	return overwrite || !nfoLooksStandard(nfo)
}

// workMetaPaths 返回电影或剧集的兼容元数据路径。
func workMetaPaths(g workGroup, mediaType string) (nfoPath, posterPath string) {
	if mediaType == MediaTypeTV && g.flatFile == "" {
		return filepath.Join(g.absDir, "tvshow.nfo"), filepath.Join(g.absDir, "poster.jpg")
	}
	stemPath := primaryStrmStem(g)
	if stemPath == "" {
		return filepath.Join(g.absDir, "movie.nfo"), filepath.Join(g.absDir, "poster.jpg")
	}
	nfoPath = stemPath + ".nfo"
	if g.flatFile != "" {
		return nfoPath, stemPath + "-poster.jpg"
	}
	return nfoPath, filepath.Join(g.absDir, "poster.jpg")
}

func primaryStrmStem(g workGroup) string {
	if g.flatFile != "" {
		return strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
	}
	if len(g.entries) == 0 {
		return ""
	}
	return strings.TrimSuffix(g.entries[0].absPath, filepath.Ext(g.entries[0].absPath))
}

func workHasNFO(g workGroup, mediaType string) bool {
	for _, p := range workNFOCandidates(g, mediaType) {
		if nfoLooksStandard(p) {
			return true
		}
	}
	return false
}

func workHasActors(g workGroup, mediaType string) bool {
	for _, path := range workNFOCandidates(g, mediaType) {
		if nfoLooksStandard(path) && nfoHasActors(path) {
			return true
		}
	}
	return false
}

func workHasPoster(g workGroup, mediaType string) bool {
	for _, p := range workPosterCandidates(g, mediaType) {
		if fileExists(p) {
			return true
		}
	}
	return false
}

func workNFOCandidates(g workGroup, mediaType string) []string {
	if mediaType == MediaTypeTV && g.flatFile == "" {
		return []string{filepath.Join(g.absDir, "tvshow.nfo")}
	}
	out := make([]string, 0, len(g.entries)+2)
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return []string{stem + ".nfo"}
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(e.absPath, filepath.Ext(e.absPath))
		out = append(out, stem+".nfo")
	}
	// 兼容上一版误写的 movie.nfo
	out = append(out, filepath.Join(g.absDir, "movie.nfo"))
	return out
}

func workPosterCandidates(g workGroup, mediaType string) []string {
	_ = mediaType
	if g.flatFile != "" {
		stem := strings.TrimSuffix(g.flatFile, filepath.Ext(g.flatFile))
		return []string{stem + "-poster.jpg", stem + ".jpg"}
	}
	out := []string{
		filepath.Join(g.absDir, "poster.jpg"),
		filepath.Join(g.absDir, "folder.jpg"),
		filepath.Join(g.absDir, "cover.jpg"),
	}
	for _, e := range g.entries {
		stem := strings.TrimSuffix(e.absPath, filepath.Ext(e.absPath))
		out = append(out, stem+"-poster.jpg", stem+".jpg")
	}
	return out
}

func workPosterFile(g workGroup, mediaType string) string {
	for _, p := range workPosterCandidates(g, mediaType) {
		if fileExists(p) {
			return p
		}
	}
	_, poster := workMetaPaths(g, mediaType)
	return poster
}

func workFanartPath(g workGroup) string {
	if g.flatFile != "" {
		return primaryStrmStem(g) + "-fanart.jpg"
	}
	return filepath.Join(g.absDir, "fanart.jpg")
}

func workHasFanart(g workGroup) bool {
	if g.flatFile != "" {
		stem := primaryStrmStem(g)
		return fileExists(stem+"-fanart.jpg") || fileExists(stem+"-fanart.png")
	}
	for _, name := range []string{"fanart.jpg", "fanart.png", "backdrop.jpg", "backdrop.png", "background.jpg", "background.png"} {
		if fileExists(filepath.Join(g.absDir, name)) {
			return true
		}
	}
	return false
}

func workClearLogoPath(g workGroup) string {
	if g.flatFile != "" {
		return primaryStrmStem(g) + "-clearlogo.png"
	}
	return filepath.Join(g.absDir, "clearlogo.png")
}

func workHasClearLogo(g workGroup) bool {
	if g.flatFile != "" {
		return fileExists(primaryStrmStem(g) + "-clearlogo.png")
	}
	return fileExists(filepath.Join(g.absDir, "clearlogo.png"))
}

func seasonPosterPath(showDir string, season int) string {
	if season <= 0 {
		return filepath.Join(showDir, "season-specials-poster.jpg")
	}
	return filepath.Join(showDir, fmt.Sprintf("season%02d-poster.jpg", season))
}

func listLocalSeasonNumbers(showDir string) []int {
	return seasonNumbersFromDirs(listLocalSeasonDirs(showDir))
}

func seasonNumbersFromDirs(dirs []seasonDir) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, dir := range dirs {
		if _, ok := seen[dir.number]; ok {
			continue
		}
		seen[dir.number] = struct{}{}
		out = append(out, dir.number)
	}
	return out
}

func writeMovieNFO(path, title, tmdbID, plot string, year *int, actors ...nfoActor) error {
	return writeWorkNFO(path, "movie", title, tmdbID, plot, year, actors)
}

func writeTVShowNFO(path, title, tmdbID, plot string, year *int, actors ...nfoActor) error {
	return writeWorkNFO(path, "tvshow", title, tmdbID, plot, year, actors)
}

func writeWorkNFO(path, root, title, tmdbID, plot string, year *int, actors []nfoActor) error {
	nfo := workNFO{
		XMLName: xml.Name{Local: root},
		Title:   strings.TrimSpace(title),
		TMDBID:  strings.TrimSpace(tmdbID),
		Plot:    strings.TrimSpace(plot),
		Actors:  actors,
	}
	if year != nil && *year > 0 {
		nfo.Year = fmt.Sprintf("%d", *year)
	}
	return writeXML(path, nfo)
}

func nfoHasActors(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(strings.ToLower(string(data)), "<actor>")
}

// appendNFOActors 在仅补缺模式下保留已有 NFO 的全部内容，只补入演员节点。
func appendNFOActors(path string, actors []nfoActor) error {
	if len(actors) == 0 || nfoHasActors(path) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	matches := nfoRootCloseRe.FindAllIndex(data, -1)
	if len(matches) == 0 {
		return fmt.Errorf("NFO 缺少 movie/tvshow 根节点")
	}
	idx := matches[len(matches)-1][0]
	var fragments strings.Builder
	for _, actor := range actors {
		raw, err := xml.Marshal(actor)
		if err != nil {
			return err
		}
		fragments.WriteString("  ")
		fragments.Write(raw)
		fragments.WriteByte('\n')
	}
	updated := append([]byte{}, data[:idx]...)
	updated = append(updated, []byte(fragments.String())...)
	updated = append(updated, data[idx:]...)
	return os.WriteFile(path, updated, 0o644)
}

func writeSeasonNFO(path string, season int, title, plot, premiered string) error {
	nfo := seasonNFO{
		Title:        strings.TrimSpace(title),
		SeasonNumber: fmt.Sprintf("%d", season),
		Plot:         strings.TrimSpace(plot),
		Premiered:    strings.TrimSpace(premiered),
	}
	return writeXML(path, nfo)
}

func writeEpisodeNFO(path, title, showTitle, plot, aired, tmdbID string, season, episode int) error {
	nfo := episodeNFO{
		Title:     strings.TrimSpace(title),
		Season:    fmt.Sprintf("%d", season),
		Episode:   fmt.Sprintf("%d", episode),
		Plot:      strings.TrimSpace(plot),
		Aired:     strings.TrimSpace(aired),
		TMDBID:    strings.TrimSpace(tmdbID),
		ShowTitle: strings.TrimSpace(showTitle),
	}
	return writeXML(path, nfo)
}

func writeXML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body := append([]byte(xml.Header), data...)
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o644)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func writeImageFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
