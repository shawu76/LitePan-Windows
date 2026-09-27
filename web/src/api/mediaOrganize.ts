import { http } from "./client";

export interface MediaOrganizeTaskConfig {
  target_directory?: string;
  target_directory_id?: string;
  action_type?: string;
  target_root?: string;
  target_root_id?: string;
  media_type?: string;
  rename_marker?: string;
  use_tmdb?: boolean;
  overwrite_existing?: boolean;
  recursive?: boolean;
  account_id?: string | number;
}

export interface MediaOrganizeTask {
  id: string;
  task_name: string;
  account_id: number;
  config: MediaOrganizeTaskConfig;
  status: string;
  last_run_at?: string;
  last_run_result?: MediaOrganizeRunResult;
  created_at?: string;
  updated_at?: string;
  is_running?: boolean;
}

export interface MediaOrganizeRunResult {
  total?: number;
  renamed?: number;
  moved?: number;
  skipped?: number;
  normal_skipped?: number;
  abnormal_skipped?: number;
  failed?: number;
  pending?: number;
  stopped?: boolean;
}

export interface MediaOrganizePlanAction {
  id: string;
  kind: string;
  source_id?: string;
  source_name?: string;
  source_parent_id?: string;
  target_parent_id?: string;
  target_name?: string;
  reason?: string;
  confidence?: number;
  metadata?: Record<string, unknown>;
  status?: string;
  error?: string;
}

export interface MediaOrganizePlan {
  task_id?: string;
  created_at?: string;
  target_root_id?: string;
  target_parent_id?: string;
  actions?: MediaOrganizePlanAction[];
  skipped?: Array<Record<string, unknown>>;
  diagnostics?: Record<string, unknown>;
}

export interface MediaOrganizeProgress {
  stage?: string;
  scanned_dirs?: number;
  scanned_files?: number;
  groups?: number;
  actions?: number;
  skipped?: number;
  current_dir?: string;
  planned_works?: number;
  max_works?: number;
  quota_reached?: boolean;
  ai_total?: number;
  ai_completed?: number;
  ai_cached?: number;
  ai_failed?: number;
  ai_chunk?: number;
  ai_chunks?: number;
  ai_batch_size?: number;
  ai_split_depth?: number;
  ai_attempt_started_at?: number;
  ai_attempt_timeout_seconds?: number;
  ai_retrying?: boolean;
}

export interface MediaOrganizeLogEntry {
  time: string;
  message: string;
}

export interface MediaOrganizeSettings {
  proxy_enabled: boolean;
  proxy_url: string;
  proxy_username: string;
  proxy_password: string;
  tmdb_api_key: string;
  tmdb_language: string;
  tmdb_api_host: string;
  tmdb_image_host: string;
  api_request_interval_ms: number;
  tmdb_request_interval_ms: number;
  file_extensions: string;
  metadata_extensions: string;
  media_tag_order: string[] | string;
  align_media_tags: boolean;
  max_works_per_run: number;
  overwrite_existing: boolean;
}

export type MediaOrganizeTaskInput = {
  task_name: string;
  account_id: number;
  target_directory: string;
  target_directory_id: string;
  action_type: string;
  target_root?: string;
  target_root_id?: string;
  media_type: string;
  rename_marker?: string;
  use_tmdb: boolean;
  overwrite_existing?: boolean;
  recursive?: boolean;
};

export function fetchMediaOrganizeTasks() {
  return http.get<MediaOrganizeTask[]>("/admin/media-organize/tasks");
}

export function createMediaOrganizeTask(input: MediaOrganizeTaskInput) {
  return http.post<MediaOrganizeTask>("/admin/media-organize/tasks", input);
}

export function updateMediaOrganizeTask(id: string, input: Partial<MediaOrganizeTaskInput>) {
  return http.put<MediaOrganizeTask>(`/admin/media-organize/tasks/${id}`, input);
}

export function deleteMediaOrganizeTask(id: string) {
  return http.del<{ id: string; stopping?: boolean }>(`/admin/media-organize/tasks/${id}`);
}

export interface MediaOrganizePlanResult {
  plan: MediaOrganizePlan;
  summary?: { actions?: number; skipped?: number };
}

export function planMediaOrganizeTask(id: string) {
  return http.postWithTimeout<MediaOrganizePlanResult>(
    `/admin/media-organize/tasks/${id}/plan`,
    undefined,
    2 * 60 * 60 * 1000,
  );
}

export function fetchMediaOrganizePlan(id: string) {
  return http.get<MediaOrganizePlan>(`/admin/media-organize/tasks/${id}/plan`);
}

export function applyMediaOrganizeTask(id: string) {
  return http.post<Record<string, unknown>>(`/admin/media-organize/tasks/${id}/apply`);
}

export function stopMediaOrganizeTask(id: string) {
  return http.post<{ stopping: boolean }>(`/admin/media-organize/tasks/${id}/stop`);
}

export function fetchMediaOrganizeLogs(id: string) {
  return http.get<{
    logs: MediaOrganizeLogEntry[];
    status: string;
    last_run_result?: MediaOrganizeRunResult;
  }>(`/admin/media-organize/tasks/${id}/logs`);
}

export function fetchMediaOrganizeProgress(id: string) {
  return http.get<MediaOrganizeProgress>(`/admin/media-organize/tasks/${id}/progress`);
}

export function updateMediaOrganizePlanAction(taskId: string, actionId: string, targetName: string) {
  return http.put<{ action?: MediaOrganizePlanAction; changed?: boolean }>(
    `/admin/media-organize/tasks/${taskId}/plan/actions/${actionId}`,
    { target_name: targetName },
  );
}

export function deleteMediaOrganizePlanAction(taskId: string, actionId: string) {
  return http.del<{ removed?: string }>(`/admin/media-organize/tasks/${taskId}/plan/actions/${actionId}`);
}

export function batchDeleteMediaOrganizePlanActions(taskId: string, actionIds: string[]) {
  return http.post<{ removed?: string[] }>(`/admin/media-organize/tasks/${taskId}/plan/actions/batch-delete`, {
    action_ids: actionIds,
  });
}

export function testMediaOrganizeTmdb(payload?: Partial<MediaOrganizeSettings>) {
  return http.post<{
    ok: boolean;
    api_ok?: boolean;
    image_ok?: boolean;
    image_status?: number;
    language?: string;
    proxy_used?: boolean;
  }>("/admin/media-organize/test-tmdb", payload ?? {});
}

export interface MediaOrganizeTmdbSearchHit {
  id?: number | string;
  title?: string;
  name?: string;
  original_title?: string;
  original_name?: string;
  release_date?: string;
  first_air_date?: string;
  poster_path?: string;
  media_type?: string;
  overview?: string;
}

export function searchMediaOrganizeTmdb(params: {
  query: string;
  year?: number;
  language?: string;
  media_type?: string;
}) {
  return http.get<MediaOrganizeTmdbSearchHit[]>("/admin/media-organize/search-tmdb", {
    query: params.query,
    year: params.year,
    language: params.language,
    media_type: params.media_type ?? "auto",
  });
}

export function setMediaOrganizeBinding(taskId: string, groupUid: string, tmdbId: string, mediaType: "movie" | "tv") {
  return http.post<{ group_uid: string; tmdb_id: string; media_type: string; plan?: MediaOrganizePlan }>(
    `/admin/media-organize/tasks/${taskId}/bindings`,
    { group_uid: groupUid, tmdb_id: tmdbId, media_type: mediaType },
  );
}

export function fetchMediaOrganizeSettings() {
  return http.get<MediaOrganizeSettings>("/admin/media-organize/settings");
}

export function saveMediaOrganizeSettings(settings: Partial<MediaOrganizeSettings>) {
  return http.put<MediaOrganizeSettings>("/admin/media-organize/settings", settings);
}
