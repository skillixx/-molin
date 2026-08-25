<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()
const labels: Record<string, string> = {
  created: '已创建', reserved: '已预占', submitted: '已提交', processing: '生成中', storing: '存储中', moderating: '审核中',
  pending: '等待处理', running: '处理中', succeeded: '成功', failed: '失败', cancelled: '已取消', expired: '已过期', pending_reconcile: '待对账', unknown: '结果待确认',
  unquoted: '未报价', held: '已预占', settlement_pending: '待结算', settled: '已结算', released: '已释放', exception: '待对账',
  passed: '安全通过', rejected: '安全拒绝', error: '审核异常', available: '可交付', not_applicable: '不适用',
}
const type = computed(() => {
  if (['succeeded', 'settled', 'passed', 'available'].includes(props.status)) return 'success'
  if (['failed', 'rejected', 'exception', 'error'].includes(props.status)) return 'danger'
  if (['created', 'reserved', 'submitted', 'processing', 'storing', 'moderating', 'pending', 'running', 'held', 'settlement_pending', 'pending_reconcile', 'unknown'].includes(props.status)) return 'warning'
  return 'info'
})
</script>

<template>
  <el-tag :type="type" effect="plain" size="small">{{ labels[status] || status }}</el-tag>
</template>
