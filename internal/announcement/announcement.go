// Package announcement 拉取并缓存后台公告，远端不可用时静默降级。
package announcement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultURL 是公告文件地址。
const DefaultURL = "https://www.litepan.top/announcement.json"

// Section 是公告正文的一个小节。
type Section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Announcement 是解析后的公告内容。
type Announcement struct {
	// Version 用于公告判重，缺失时使用内容哈希。
	Version   string    `json:"notice_version"`
	Badge     string    `json:"badge"`
	Title     string    `json:"dialog_title"`
	Banner    string    `json:"banner"`
	Special   string    `json:"special"`
	Lead      string    `json:"lead"`
	Sections  []Section `json:"issues"`
	Footnote  string    `json:"footnote"`
	FetchedAt time.Time `json:"fetched_at"`
}

const (
	fetchTimeout = 10 * time.Second
	cacheTTL     = 10 * time.Minute
	// failCooldown 避免失败后频繁请求远端。
	failCooldown = 5 * time.Minute
	// maxBodyBytes 远端文件大小上限，防止异常大文件拖垮请求。
	maxBodyBytes = 512 * 1024
)

// Service 拉取并缓存公告。
type Service struct {
	url    string
	client *http.Client

	mu       sync.Mutex
	cached   *Announcement
	cachedAt time.Time
	failedAt time.Time
}

// New 构造公告服务。
func New(url string) *Service {
	return &Service{
		url:    strings.TrimSpace(url),
		client: &http.Client{Timeout: fetchTimeout},
	}
}

// Enabled 返回公告服务是否配置了远端文件。
func (s *Service) Enabled() bool {
	return s.url != ""
}

// Fetch 返回当前公告；拉取失败时静默返回旧缓存。
func (s *Service) Fetch(ctx context.Context) (*Announcement, error) {
	if s.url == "" {
		return nil, nil
	}
	s.mu.Lock()
	now := time.Now()
	if s.cached != nil && now.Sub(s.cachedAt) < cacheTTL {
		item := cloneAnnouncement(s.cached)
		s.mu.Unlock()
		return item, nil
	}
	if !s.failedAt.IsZero() && now.Sub(s.failedAt) < failCooldown {
		item := cloneAnnouncement(s.cached)
		s.mu.Unlock()
		return item, nil
	}
	s.mu.Unlock()

	body, err := s.fetchBody(ctx)
	if err != nil {
		return s.fallbackAfterFailure(), nil
	}

	item := parse(body)
	if item == nil {
		return s.fallbackAfterFailure(), nil
	}
	item.FetchedAt = time.Now()
	s.mu.Lock()
	s.cached = item
	s.cachedAt = item.FetchedAt
	s.failedAt = time.Time{}
	s.mu.Unlock()
	return cloneAnnouncement(item), nil
}

func (s *Service) fallbackAfterFailure() *Announcement {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedAt = time.Now()
	return cloneAnnouncement(s.cached)
}

func (s *Service) fetchBody(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LitePan/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("announcement file exceeds %d bytes", maxBodyBytes)
	}
	return body, nil
}

// parse 只接受文档站约定的有效 JSON。格式异常、非 JSON 或没有可展示正文时返回 nil，
// 调用方静默沿用旧缓存或返回暂无公告，避免把错误页和损坏内容展示给用户。
func parse(raw []byte) *Announcement {
	text := strings.TrimSpace(string(raw))
	if text == "" || text[0] != '{' {
		return nil
	}
	var ja jsonAnnouncement
	if err := json.Unmarshal(raw, &ja); err != nil {
		return nil
	}
	if strings.TrimSpace(ja.Title) == "" && strings.TrimSpace(ja.Lead) == "" && len(ja.Issues) == 0 {
		return nil
	}
	version := strings.TrimSpace(ja.Version)
	if version == "" {
		version = contentHash(text)
	}
	sections := make([]Section, 0, len(ja.Issues))
	for _, s := range ja.Issues {
		s.Title = strings.TrimSpace(s.Title)
		s.Body = strings.TrimSpace(s.Body)
		if s.Title == "" && s.Body == "" {
			continue
		}
		sections = append(sections, s)
	}
	title := strings.TrimSpace(ja.Title)
	if title == "" {
		title = "公告"
	}
	return &Announcement{
		Version:  version,
		Badge:    normalizeVisible(ja.Badge),
		Title:    title,
		Banner:   normalizeVisible(ja.Banner),
		Special:  normalizeVisible(ja.Special),
		Lead:     normalizeVisible(ja.Lead),
		Sections: sections,
		Footnote: strings.TrimSpace(ja.Footnote),
	}
}

type jsonAnnouncement struct {
	Version  string    `json:"notice_version"`
	Badge    string    `json:"badge"`
	Title    string    `json:"dialog_title"`
	Banner   string    `json:"banner"`
	Special  string    `json:"special"`
	Lead     string    `json:"lead"`
	Issues   []Section `json:"issues"`
	Footnote string    `json:"footnote"`
}

// normalizeVisible 归一化可选文本区（badge/banner/special/lead）：
// 空值、none、false（不区分大小写）一律视为不显示。
func normalizeVisible(v string) string {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "", "none", "false":
		return ""
	}
	return v
}

func cloneAnnouncement(item *Announcement) *Announcement {
	if item == nil {
		return nil
	}
	clone := *item
	clone.Sections = append([]Section(nil), item.Sections...)
	return &clone
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
