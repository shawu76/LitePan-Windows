import { computed, type ComputedRef, type Ref } from "vue";
import { SCAN_MODES, type StrmTask } from "@/api/strm";
import { formatRunTimeText } from "@/utils/format";

/**
 * STRM 任务的派生展示数据：执行中数量、最近一轮扫描成功率、接下来的运行计划。
 * 运行计划直接用后端下发的 next_run_at，不在前端复刻调度规则。
 */

export function isStrmTaskEnabled(task: StrmTask): boolean {
  return task.status === "active" || task.status === "running";
}

export function isStrmTaskScanning(task: StrmTask): boolean {
  return Boolean(task.is_scanning);
}

export function useStrmScanPlan(tasks: Ref<StrmTask[]>): {
  scanningCount: ComputedRef<number>;
  scanSuccessRate: ComputedRef<number | null>;
  planItems: ComputedRef<Array<{ time: string; name: string; mode: string }>>;
  /** 接下来最近一次运行的时间（ISO 字符串）与任务名；无计划时为空。 */
  nextRunAt: ComputedRef<string>;
  nextRunTask: ComputedRef<string>;
} {
  const scanningCount = computed(() => tasks.value.filter((task) => isStrmTaskScanning(task)).length);

  // 成功率分母只算已出结果的任务（ok / failed）；protected 与 stopped 不计入。
  const scanSuccessRate = computed<number | null>(() => {
    let ok = 0;
    let failed = 0;
    for (const task of tasks.value) {
      const status = (task.last_scan_status || "").trim().toLowerCase();
      if (status === "ok" || status === "success") ok += 1;
      else if (status === "failed" || status === "error") failed += 1;
    }
    if (ok + failed === 0) return null;
    return (ok / (ok + failed)) * 100;
  });

  const upcoming = computed(() => {
    const entries: Array<{ name: string; at: Date; mode: string }> = [];
    for (const task of tasks.value) {
      if (!isStrmTaskEnabled(task)) continue;
      const raw = task.next_run_at;
      if (!raw) continue;
      const at = new Date(raw);
      if (Number.isNaN(at.getTime())) continue;
      entries.push({
        name: task.name,
        at,
        mode: SCAN_MODES.find((item) => item.value === task.scan_mode)?.label ?? task.scan_mode ?? "",
      });
    }
    entries.sort((a, b) => a.at.getTime() - b.at.getTime());
    return entries;
  });

  const planItems = computed(() =>
    upcoming.value.slice(0, 3).map((entry) => ({
      time: formatRunTimeText(entry.at),
      name: entry.name,
      mode: entry.mode,
    })),
  );

  const nextRunAt = computed(() => (upcoming.value[0] ? upcoming.value[0].at.toISOString() : ""));
  const nextRunTask = computed(() => upcoming.value[0]?.name ?? "");

  return { scanningCount, scanSuccessRate, planItems, nextRunAt, nextRunTask };
}
