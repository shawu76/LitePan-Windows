<template>
  <template v-for="node in nodes" :key="node.id + '_' + node.name">
    <div
      v-if="node.type === 'dir'"
      class="tnode folder"
      :style="{ paddingLeft: (6 + depth * 16) + 'px' }"
      @click="toggle(node)"
    >
      <span class="caret" :class="{ open: node.open }"><SvgIcon name="chevron-right" size="1em" /></span>
      <span class="t-ic dir"><SvgIcon name="folder" size="1em" /></span>
      <span class="t-name">{{ node.name }}</span>
      <span v-if="mode === 'src' && countOk(node) > 0" class="folder-count">{{ countOk(node) }} 个可秒传</span>
    </div>
    <CrossTransferTree
      v-if="node.type === 'dir' && node.open"
      :nodes="node.children || []"
      :mode="mode"
      :depth="depth + 1"
    />

    <div
      v-else-if="node.type === 'file'"
      class="tnode file"
      :class="{ probing: node.state === 'run' }"
      :data-rel-path="node.rel_path"
      :style="{ paddingLeft: (6 + depth * 16) + 'px' }"
    >
      <span class="caret-spacer"></span>
      <span class="t-ic file"><SvgIcon name="file" size="1em" /></span>
      <span class="t-name">{{ node.name }}</span>
      <span class="t-meta">{{ fmtSize(node.size) }}</span>
      <span v-if="mode === 'src'" class="tag" :class="statusClass(node)" :title="node.transferError || ''">
        <BusySpinner v-if="node.state === 'run'" :size="12" />
        <SvgIcon v-else :name="statusIcon(node)" size="1em" />
        {{ statusText(node) }}
      </span>
    </div>
  </template>
</template>

<script setup>
import BusySpinner from "@/components/base/BusySpinner.vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

defineOptions({ name: 'CrossTransferTree' })

defineProps({
  nodes: { type: Array, default: () => [] },
  mode: { type: String, default: 'src' },
  depth: { type: Number, default: 0 }
})

const toggle = (node) => { node.open = !node.open }

const fmtSize = (b) => {
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = Number(b || 0); let i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return (i === 0 ? v.toFixed(0) : v.toFixed(2)) + ' ' + u[i]
}

const countOk = (node) => {
  if (node.type === 'file') return node.reuse === true ? 1 : 0
  return (node.children || []).reduce((a, c) => a + countOk(c), 0)
}

const statusClass = (n) => {
  if (n.transferred) return 'done'
  if (n.skipped) return 'skipped'
  if (n.relay) return 'run'
  if (n.transferError) return 'error'
  if (n.state === 'run') return 'run'
  if (n.reuse === true) return 'ok'
  if (n.reuse === false) return 'no'
  return 'pending'
}
const statusIcon = (n) => {
  if (n.transferred) return 'check'
  if (n.skipped) return 'forward-step'
  if (n.relay) return 'truck-fast'
  if (n.transferError) return 'circle-exclamation'
  if (n.state === 'run') return ''
  if (n.reuse === true) return 'bolt'
  if (n.reuse === false) return 'ban'
  return 'clock'
}
const statusText = (n) => {
  if (n.transferred) return '已转存'
  if (n.skipped) return '已跳过'
  if (n.relay) return '兜底传输中'
  if (n.transferError) return '传输失败'
  if (n.state === 'run') return '验证中'
  if (n.reuse === true) return '可秒传'
  if (n.reuse === false) return '不可秒传'
  return '待试探'
}
</script>

<style scoped>
.tnode { display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: var(--radius-control); color: var(--text-main, var(--text)); }
.tnode:hover { background: rgba(127,127,127,.1); }
.tnode.file.probing { background: rgba(217,119,6,.12); outline: 1px solid rgba(217,119,6,.25); }
.tnode.folder { cursor: pointer; }
.caret { width: 14px; color: var(--text-secondary, var(--text-muted)); font-size: 12px; transition: transform .14s; flex: 0 0 auto; }
.caret.open { transform: rotate(90deg); }
.caret-spacer { width: 14px; flex: 0 0 auto; }
.t-ic { width: 26px; height: 26px; border-radius: var(--radius-sm); display: flex; align-items: center; justify-content: center; font-size: 13px; flex: 0 0 auto; }
.t-ic.dir { color: #f5b942; background: rgba(245,185,66,.16); }
.t-ic.file { color: #7c93b3; background: rgba(124,147,179,.16); }
.t-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.t-meta { color: var(--text-secondary, var(--text-muted)); font-size: 12px; white-space: nowrap; }
.folder-count { font-size: 12px; padding: 2px 9px; border-radius: var(--radius-pill); white-space: nowrap; border: 1px solid rgba(76,116,223,.22); background: rgba(76,116,223,.08); color: #1d4ed8; }
.tag { font-size: 12px; font-weight: 600; padding: 3px 9px; border-radius: var(--radius-pill); white-space: nowrap; display: inline-flex; align-items: center; gap: 5px; }
.tag.ok { color: #16a34a; background: #dcfce7; }
.tag.no { color: #94a3b8; background: #eef2f7; }
.tag.run { color: #d97706; background: #fef3c7; }
.tag.done { color: #16a34a; background: #dcfce7; }
.tag.skipped { color: #64748b; background: #e2e8f0; }
.tag.error { color: #dc2626; background: #fee2e2; }
.tag.pending { color: #64748b; background: #f1f5f9; }

:global(:root[data-skin="brutal"] .cross-transfer) .tnode,
:global(:root[data-skin="brutal"] .cross-transfer) .t-ic,
:global(:root[data-skin="brutal"] .cross-transfer) .folder-count,
:global(:root[data-skin="brutal"] .cross-transfer) .tag {
  border-radius: 0;
}

:global(:root[data-skin="brutal"] .cross-transfer) .tag {
  border: 1px solid var(--brutal-ink);
  color: var(--brutal-ink);
}
</style>
