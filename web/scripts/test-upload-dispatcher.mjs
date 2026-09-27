import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import ts from "typescript";

// 执行真实派发器，只替换网络和界面依赖，不上传任何文件。
const { outputText } = ts.transpileModule(
  readFileSync(new URL("../src/composables/upload/useLocalUploadDispatcher.ts", import.meta.url), "utf8"),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
);
let failRequest = true;
const requests = [];
const modules = {
  "@/api/upload": { uploadApi: {
    async createTask(form) {
      requests.push(form);
      if (failRequest) throw new Error("模拟网络失败");
      return { task_id: "remote-1" };
    },
  } },
  "@/api/client": { getApiErrorMessage: (error) => error.message },
  "@/composables/upload/uploadTaskFormatters": { isLocalUploadTask: () => true },
  "@/composables/upload/useUploadTaskStream": {
    getNextLocalUploadTaskCandidate: () => null,
    getNextRemoteResumeTaskCandidate: () => null,
  },
};
const exported = {};
new Function("require", "exports", outputText)((name) => {
  assert.ok(name in modules, `未模拟的依赖：${name}`);
  return modules[name];
}, exported);

const deps = {
  selectedAccountId: { value: 99 },
  currentPath: { value: "当前页面的其他目录" },
  getCurrentBreadcrumbNameParts: () => [],
};
const store = {
  localUploadTaskPayloads: new Map(),
  localUploadTaskControllers: new Map(),
  canceledLocalUploadTaskIds: new Set(),
  pausedLocalUploadTaskIds: new Set(),
  localDispatchingTaskIds: new Set(),
  updateLocalUploadTask() {},
};
const stream = {
  async fetchUploadTasks() {},
  async refreshUploadTaskServerConcurrency() {},
  startUploadTaskPolling() {},
};
const dispatcher = exported.useLocalUploadDispatcher(deps, store, stream);
const file = new File(["模拟内容"], "文件.txt");
const task = { task_id: "local-1", account_id: 7, target_path: "", file_name: file.name };
const options = { targetPath: "", batchRootId: "folder-1", batchRootParentId: "root", batchRootOwned: true };

assert.equal((await dispatcher.createSingleUploadTask(file, "skip", task, options)).success, false);
const retry = store.localUploadTaskPayloads.get(task.task_id);
assert.equal(retry.batchRootId, options.batchRootId);
assert.equal(retry.batchRootParentId, options.batchRootParentId);
assert.equal(retry.batchRootOwned, true);
failRequest = false;
assert.equal((await dispatcher.createSingleUploadTask(retry.file, retry.conflictPolicy, task, retry)).success, true);
for (const form of requests) {
  assert.equal(form.get("account_id"), "7", "切换页面账号不得改变任务所属账号");
  assert.equal(form.get("path"), "", "根目录空 ID 不得被当前浏览目录替换");
  assert.equal(form.get("batch_root_id"), "folder-1");
  assert.equal(form.get("batch_root_owned"), "true");
  assert.equal(form.get("conflict_policy"), "skip");
}
assert.equal(store.localUploadTaskPayloads.size, 0, "投递成功后释放 File 引用");
assert.equal(store.localUploadTaskControllers.size, 0);
console.log("upload dispatcher ok: retry metadata, fixed destination, payload cleanup");
