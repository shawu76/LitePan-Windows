package strm

import (
	"fmt"
	"net/url"
	"strconv"
)

func BuildPlayPath(accountID int64, fileID, fileName, token string, signEnabled bool, secret []byte) string {
	account := strconv.FormatInt(accountID, 10)
	escapedName := url.PathEscape(fileName)
	path := fmt.Sprintf("/api/strm/play/%s/%s/t/%s/n/%s", account, EncodeFileKey(fileID), token, escapedName)
	if signEnabled {
		path += "/s/" + SignPath(path, secret)
	}
	return path
}

func BuildPlayURL(baseURL string, accountID int64, fileID, fileName, token string, signEnabled bool, secret []byte) string {
	path := BuildPlayPath(accountID, fileID, fileName, token, signEnabled, secret)
	base := NormalizeBaseURL(baseURL)
	if base == "" {
		return path
	}
	return base + path
}

// BuildPathPlayPath 构造按“起始目录 ID + 相对路径”延迟解析的兼容播放路径。
// 当前扫描任务仍使用文件 ID 格式；该格式供后续目录树导入等无法预先取得文件 ID 的场景使用。
func BuildPathPlayPath(accountID int64, rootID, relativePath, fileName, token string, signEnabled bool, secret []byte) string {
	account := strconv.FormatInt(accountID, 10)
	escapedName := url.PathEscape(fileName)
	path := fmt.Sprintf("/api/strm/path/%s/%s/%s/t/%s/n/%s", account, EncodePathKey(rootID), EncodePathKey(relativePath), token, escapedName)
	if signEnabled {
		path += "/s/" + SignPath(path, secret)
	}
	return path
}

func BuildPathPlayURL(baseURL string, accountID int64, rootID, relativePath, fileName, token string, signEnabled bool, secret []byte) string {
	path := BuildPathPlayPath(accountID, rootID, relativePath, fileName, token, signEnabled, secret)
	base := NormalizeBaseURL(baseURL)
	if base == "" {
		return path
	}
	return base + path
}
