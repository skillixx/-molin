<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()
const labels: Record<string, string> = {
  pending: '等待处理', running: '处理中', succeeded: '成功', failed: '失败', cancelled: '已取消', unknown: '结果待确认',
  unquoted: '未报价', held: '已预占', settlement_pending: '待结算', settled: '已结算', released: '已释放', exception: '待对账',
  passed: '安全通过', rejected: '安全拒绝', error: '审核异常',
}
const type = computed(() => {
  if (['succeeded', 'settled', 'passed'].includes(props.status)) return 'success'
  if (['failed', 'rejected', 'exception', 'error'].includes(props.status)) return 'danger'
  if (['pending', 'running', 'held', 'settlement_pending', 'unknown'].includes(props.status)) return 'warning'
  return 'info'
})
</script>

<template>
  <el-tag :type="type" effect="plain" size="small">{{ labels[status] || status }}</el-tag>
</template>
