package mediaorganize

func NormalizeTaskConfig(config map[string]any) map[string]any {
	defaults := map[string]any{
		"task_name":              "",
		"account_id":             "",
		"target_directory":       "",
		"target_directory_id":    "",
		"action_type":            "move",
		"target_root":            "",
		"target_root_id":         "",
		"media_type":             "auto",
		"rename_marker":          "",
		"season_folder_template": "Season {season:02d}",
		"use_tmdb":               true,
		"overwrite_existing":     false,
		"recursive":              true,
	}
	if config == nil {
		return defaults
	}
	for key := range defaults {
		if val, ok := config[key]; ok {
			defaults[key] = val
		}
	}
	return defaults
}
