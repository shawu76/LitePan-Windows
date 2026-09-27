package app

import (
	"context"
	"strings"

	"litepan/internal/aiorganize"
	"litepan/internal/classifyorganize"
	"litepan/internal/domain"
	"litepan/internal/file"
	"litepan/internal/logx"
	"litepan/internal/mediaorganize"
	"litepan/internal/mediaorganize/executor"
	"litepan/internal/mediaorganize/planner"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

func wireMediaOrganize(
	st *storeBundle,
	files *file.Service,
	logs *logx.Manager,
	dataDir string,
	ai *aiorganize.Service,
	classifier *classifyorganize.Service,
) *mediaorganize.Service {
	return mediaorganize.NewService(mediaorganize.ServiceOptions{
		Repo:     st.store.MediaOrganizeTasks,
		Files:    files,
		Settings: st.settings,
		DataDir:  dataDir,
		Log:      logs.For(logx.ModuleFileOp),
		Planner: plannerAdapter{
			files:          files,
			settings:       st.settings,
			recognition:    ai,
			classification: classifier,
		},
		Executor: executorAdapter{files: files},
	})
}

type plannerAdapter struct {
	files          *file.Service
	settings       *settings.Service
	recognition    *aiorganize.Service
	classification *classifyorganize.Service
}

func (a plannerAdapter) Build(
	ctx context.Context,
	taskID string,
	task *domain.MediaOrganizeTask,
	cfg map[string]any,
	settings map[string]any,
	hooks mediaorganize.PlannerHooks,
) (*mediaorganize.Plan, error) {
	accountID := mediaorganize.CfgAccountID(cfg)
	if accountID == 0 && task != nil {
		accountID = task.AccountID
	}
	plannerSettings := mediaorganize.EnrichPlannerSettings(a.settings, settings)
	apiKey := mediaorganize.PlannerTMDBAPIKey(plannerSettings)
	tmdbClient := tmdb.NewClient(tmdb.Options{
		APIKey:        apiKey,
		Language:      mediaorganize.PlannerTMDBLanguage(plannerSettings),
		ProxyURL:      tmdb.BuildProxyURL(mediaorganize.TmdbProxyFromSettings(plannerSettings)),
		APIBaseHost:   mediaorganize.PlannerTMDBAPIHost(plannerSettings),
		ImageBaseHost: mediaorganize.PlannerTMDBImageHost(plannerSettings),
	})

	var progressFn planner.ProgressFunc
	if hooks.Progress != nil {
		progressFn = func(p planner.Progress) {
			hooks.Progress(map[string]any{
				"stage":                      p.Stage,
				"scanned_dirs":               p.ScannedDirs,
				"scanned_files":              p.ScannedFiles,
				"groups":                     p.Groups,
				"actions":                    p.Actions,
				"skipped":                    p.Skipped,
				"current_dir":                p.CurrentDir,
				"planned_works":              p.PlannedWorks,
				"max_works":                  p.MaxWorks,
				"quota_reached":              p.QuotaReached,
				"ai_total":                   p.AITotal,
				"ai_completed":               p.AICompleted,
				"ai_cached":                  p.AICached,
				"ai_failed":                  p.AIFailed,
				"ai_chunk":                   p.AIChunk,
				"ai_chunks":                  p.AIChunks,
				"ai_batch_size":              p.AIBatchSize,
				"ai_split_depth":             p.AISplitDepth,
				"ai_attempt_started_at":      p.AIStartedAt,
				"ai_attempt_timeout_seconds": p.AITimeout,
				"ai_retrying":                p.AIRetrying,
			})
		}
	}
	logFn := func(string) {}
	if hooks.Log != nil {
		logFn = hooks.Log
	}
	stopFn := func() error { return nil }
	if hooks.CheckStop != nil {
		stopFn = hooks.CheckStop
	}

	p := planner.New(
		ctx,
		a.files,
		accountID,
		planner.TaskConfigFromMap(cfg),
		plannerSettings,
		taskID,
		tmdbClient,
		logFn,
		progressFn,
		stopFn,
	)
	p.SetRecognitionEnhancer(a.recognition)
	p.SetClassificationEnhancer(a.classification)
	return p.Build()
}

type executorAdapter struct {
	files *file.Service
}

func (a executorAdapter) Apply(
	ctx context.Context,
	plan *mediaorganize.Plan,
	_ string,
	accountID int64,
	_ map[string]any,
	settings map[string]any,
	hooks mediaorganize.ExecutorHooks,
) error {
	if plan == nil {
		return nil
	}
	overwrite := boolAny(settings["overwrite_existing"], false)
	logFn := func(string) {}
	if hooks.Log != nil {
		logFn = hooks.Log
	}
	stopFn := func() error { return nil }
	if hooks.CheckStop != nil {
		stopFn = func() error {
			if err := hooks.CheckStop(); err != nil {
				return executor.ErrStopped
			}
			return nil
		}
	}
	ex := executor.New(ctx, a.files, plan, accountID, overwrite, logFn, stopFn)
	_, err := ex.Apply()
	if err != nil && strings.Contains(err.Error(), "stopped") {
		return mediaorganize.ErrTaskAborted
	}
	return err
}

func boolAny(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		if s == "" {
			return def
		}
		return s == "1" || s == "true" || s == "yes" || s == "on"
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return def
	}
}
