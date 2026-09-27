package strm

import (
	"fmt"
	"regexp"
	"strings"
)

type ReplaceBaseURLResult struct {
	Total   int `json:"total"`
	Updated int `json:"updated"`
}

var strmBaseURLPrefix = regexp.MustCompile(`^https?://[^/]+`)

func ReplaceBaseURLInFiles(strmDir, newBaseURL string) (ReplaceBaseURLResult, error) {
	var result ReplaceBaseURLResult
	base := NormalizeBaseURL(newBaseURL)
	if base == "" {
		return result, fmt.Errorf("new base url required")
	}
	total, _, updated, err := rewriteStrmFiles(strmDir, func(line string) (string, bool) {
		return replaceBaseInLine(line, base)
	})
	result.Total = total
	result.Updated = updated
	return result, err
}

func replaceBaseInLine(line, base string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return line, false
	}
	if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
		return line, false
	}
	replaced := strmBaseURLPrefix.ReplaceAllString(line, base)
	return replaced, replaced != line
}

func ValidateBaseURL(raw string) error {
	base := NormalizeBaseURL(raw)
	if base == "" {
		return fmt.Errorf("新基址不能为空")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("新基址格式不正确，示例：https://litepan.top")
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if rest == "" || strings.ContainsAny(rest, " \t\r\n") {
		return fmt.Errorf("新基址格式不正确，示例：https://litepan.top")
	}
	return nil
}
