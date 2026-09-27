package strm

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	PlayPathRE     = regexp.MustCompile(`(?i)^/api/strm/play/(\d+)/([^/]+)/t/([^/]+)/n/([^/?#\s]+)(?:/s/([^/?#\s]+))?$`)
	PathPlayPathRE = regexp.MustCompile(`(?i)^/api/strm/path/(\d+)/([^/]+)/([^/]+)/t/([^/]+)/n/([^/?#\s]+)(?:/s/([^/?#\s]+))?$`)
)

type PlayReference struct {
	AccountID    int64
	FileID       string
	RootID       string
	RelativePath string
	Token        string
	FileName     string
	Signature    string
	PathBased    bool
}

// ParsePlayReference 同时解析文件 ID 与路径两种 STRM 播放地址。
func ParsePlayReference(value string) (PlayReference, bool) {
	pathValue := playURLPath(value)
	if m := PlayPathRE.FindStringSubmatch(pathValue); len(m) >= 5 {
		accountID, err := strconv.ParseInt(m[1], 10, 64)
		fileID, decodeErr := DecodeFileKey(m[2])
		if err != nil || accountID <= 0 || decodeErr != nil || fileID == "" {
			return PlayReference{}, false
		}
		name, _ := url.PathUnescape(m[4])
		ref := PlayReference{AccountID: accountID, FileID: fileID, Token: m[3], FileName: name}
		if len(m) > 5 {
			ref.Signature = m[5]
		}
		return ref, true
	}
	if m := PathPlayPathRE.FindStringSubmatch(pathValue); len(m) >= 6 {
		accountID, err := strconv.ParseInt(m[1], 10, 64)
		rootID, rootErr := DecodePathKey(m[2])
		relativePath, pathErr := DecodePathKey(m[3])
		if err != nil || accountID <= 0 || rootErr != nil || pathErr != nil || relativePath == "" {
			return PlayReference{}, false
		}
		name, _ := url.PathUnescape(m[5])
		ref := PlayReference{
			AccountID: accountID, RootID: rootID, RelativePath: relativePath,
			Token: m[4], FileName: name, PathBased: true,
		}
		if len(m) > 6 {
			ref.Signature = m[6]
		}
		return ref, true
	}
	return PlayReference{}, false
}

func playURLPath(value string) string {
	text := strings.Trim(strings.TrimSpace(value), "`\"'")
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		u, err := url.Parse(text)
		if err != nil {
			return ""
		}
		return u.EscapedPath()
	}
	pathValue, _, _ := strings.Cut(text, "?")
	return pathValue
}
