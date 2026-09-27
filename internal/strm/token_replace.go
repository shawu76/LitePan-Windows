package strm

import (
	"fmt"
	"net/url"
	"strings"
)

type ReplaceTokenResult struct {
	Total   int `json:"total"`
	Matched int `json:"matched"`
	Updated int `json:"updated"`
}

func ReplaceTokenInFiles(strmDir, oldToken, newToken string, secret []byte) (ReplaceTokenResult, error) {
	var result ReplaceTokenResult
	oldToken = strings.TrimSpace(oldToken)
	newToken = strings.TrimSpace(newToken)
	if newToken == "" {
		return result, fmt.Errorf("new token required")
	}
	total, matched, updated, err := rewriteStrmFiles(strmDir, func(line string) (string, bool) {
		return replaceTokenInLine(line, oldToken, newToken, secret)
	})
	result.Total = total
	result.Matched = matched
	result.Updated = updated
	return result, err
}

func replaceTokenInLine(line, oldToken, newToken string, secret []byte) (string, bool) {
	raw := strings.TrimSpace(line)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" && !strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "/") {
			parsed, err = url.Parse("http://local" + raw)
		}
	}
	if err != nil || parsed == nil {
		return raw, false
	}

	path := parsed.Path
	tokenIdx := strings.Index(path, "/t/")
	if tokenIdx >= 0 {
		rest := path[tokenIdx+3:]
		end := strings.Index(rest, "/n/")
		if end < 0 {
			return raw, false
		}
		current := rest[:end]
		if oldToken != "" && current != oldToken {
			return raw, false
		}
		newPath := path[:tokenIdx+3] + newToken + rest[end:]
		if strings.Contains(newPath, "/s/") {
			newPath = stripSignature(newPath)
			if len(secret) > 0 {
				newPath += "/s/" + SignPath(newPath, secret)
			}
		}
		parsed.Path = newPath
		parsed.RawPath = ""
		return rebuildURL(parsed), true
	}
	return raw, false
}

func stripSignature(path string) string {
	idx := strings.Index(path, "/s/")
	if idx < 0 {
		return path
	}
	return path[:idx]
}

func rebuildURL(u *url.URL) string {
	if u.Host == "local" {
		out := u.Path
		if u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		return out
	}
	return u.String()
}
