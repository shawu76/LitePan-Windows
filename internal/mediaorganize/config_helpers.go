package mediaorganize

import (
	"encoding/json"
	"strconv"
	"strings"

	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

func CfgAccountID(cfg map[string]any) int64 {
	raw := strings.TrimSpace(stringFromAny(cfg["account_id"]))
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func PlannerSettingsFromAPI(settings map[string]any) map[string]any {
	out := make(map[string]any, len(moSettingFieldToKey)+4)
	for apiField, moKey := range moSettingFieldToKey {
		if v, ok := settings[apiField]; ok {
			if apiField == "media_tag_order" {
				out[moKey] = normalizeMediaTagOrder(v)
				continue
			}
			out[moKey] = v
		}
	}
	for k, v := range settings {
		if strings.HasPrefix(k, "mo_") {
			out[k] = v
		}
	}
	return out
}

func EnrichPlannerSettings(svc *settings.Service, api map[string]any) map[string]any {
	out := PlannerSettingsFromAPI(api)
	if svc == nil {
		return out
	}
	out["mo_proxy_enabled"] = svc.Bool(settings.KeyMOProxyEnabled)
	out["mo_proxy_url"] = svc.String(settings.KeyMOProxyURL)
	out["mo_proxy_username"] = svc.String(settings.KeyMOProxyUsername)
	out["mo_proxy_password"] = svc.String(settings.KeyMOProxyPassword)
	if key := strings.TrimSpace(svc.String(settings.KeyMOTmdbAPIKey)); key != "" {
		out["mo_tmdb_api_key"] = key
	}
	if lang := strings.TrimSpace(svc.String(settings.KeyMOTmdbLanguage)); lang != "" {
		out["mo_tmdb_language"] = lang
	}
	out["mo_tmdb_api_host"] = svc.String(settings.KeyMOTmdbAPIHost)
	out["mo_tmdb_image_host"] = svc.String(settings.KeyMOTmdbImageHost)
	out["mo_api_request_interval_ms"] = svc.Int(settings.KeyMOAPIRequestIntervalMS)
	out["mo_tmdb_request_interval_ms"] = svc.Int(settings.KeyMOTmdbRequestIntervalMS)
	out["mo_file_extensions"] = svc.String(settings.KeyMOFileExtensions)
	out["mo_metadata_extensions"] = svc.String(settings.KeyMOMetadataExtensions)
	out["mo_media_tag_order"] = svc.String(settings.KeyMOMediaTagOrder)
	out["mo_align_media_tags"] = svc.Bool(settings.KeyMOAlignMediaTags)
	out["mo_max_works_per_run"] = svc.Int(settings.KeyMOMaxWorksPerRun)
	out["mo_overwrite_existing"] = svc.Bool(settings.KeyMOOverwriteExisting)
	return out
}

func PlannerTMDBAPIKey(plannerSettings map[string]any) string {
	if key := strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_api_key"])); key != "" {
		return key
	}
	return strings.TrimSpace(stringFromAny(plannerSettings["tmdb_api_key"]))
}

func PlannerTMDBLanguage(plannerSettings map[string]any) string {
	if lang := strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_language"])); lang != "" {
		return lang
	}
	if lang := strings.TrimSpace(stringFromAny(plannerSettings["tmdb_language"])); lang != "" {
		return lang
	}
	return "zh-CN"
}

// PlannerTMDBAPIHost 返回 TMDB API 反代主域名，未配置返回空。
func PlannerTMDBAPIHost(plannerSettings map[string]any) string {
	return strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_api_host"]))
}

// PlannerTMDBImageHost 返回 TMDB 图片反代主域名（未配置返回空，由 tmdb.Client 回落官方默认）。
func PlannerTMDBImageHost(plannerSettings map[string]any) string {
	return strings.TrimSpace(stringFromAny(plannerSettings["mo_tmdb_image_host"]))
}

func TmdbProxyFromSettings(settings map[string]any) tmdb.ProxyConfig {
	return proxyConfigFrom(settings, "mo_")
}

func proxyConfigFrom(settings map[string]any, prefix string) tmdb.ProxyConfig {
	return tmdb.ProxyConfig{
		Enabled:  rules.SettingBool(settings[prefix+"proxy_enabled"], false),
		URL:      stringFromAny(settings[prefix+"proxy_url"]),
		Username: stringFromAny(settings[prefix+"proxy_username"]),
		Password: stringFromAny(settings[prefix+"proxy_password"]),
	}
}

func normalizeMediaTagOrder(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any, []string:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return stringFromAny(raw)
	}
}
