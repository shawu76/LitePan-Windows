import { http } from "./client";
import type { Account } from "./types";
import type { CacheStats } from "./cache";
import type { CacheRetentionStats, CacheRetentionTask } from "./cacheRetention";
import type { FuseMount } from "./fuse";
import type { LogStats } from "./logs";
import type { MediaOrganizeTask } from "./mediaOrganize";
import type { NotificationItem } from "./notifications";
import type { StrmTask } from "./strm";

export interface DashboardOverview {
  accounts: Account[];
  cache_stats: CacheStats;
  cache_retention_tasks: CacheRetentionTask[];
  cache_retention_stats: CacheRetentionStats;
  fuse_mounts: FuseMount[];
  strm_tasks: StrmTask[];
  organize_tasks: MediaOrganizeTask[];
  notifications: NotificationItem[];
  unread_count: number;
  log_stats: LogStats;
}

export function fetchDashboardOverview() {
  return http.get<DashboardOverview>("/admin/overview");
}
