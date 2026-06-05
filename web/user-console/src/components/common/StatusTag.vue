<script setup lang="ts">
/**
 * 通用状态标签组件
 * 根据 status 自动渲染对应颜色的标签
 * 适用于：实名状态、资产状态等
 */
interface Props {
  status: 'unverified' | 'pending' | 'verified' | 'rejected' | 'active' | 'expired' | 'suspended' | string
}

const props = defineProps<Props>()

// 状态配置映射
const statusMap: Record<string, { label: string; cls: string }> = {
  unverified: { label: '未认证', cls: 'unverified' },
  pending:    { label: '审核中', cls: 'pending' },
  verified:   { label: '已认证', cls: 'verified' },
  rejected:   { label: '已拒绝', cls: 'rejected' },
  active:     { label: '使用中', cls: 'verified' },
  expired:    { label: '已过期', cls: 'rejected' },
  suspended:  { label: '已暂停', cls: 'pending' },
}

const config = computed(() => statusMap[props.status] ?? { label: props.status, cls: 'unverified' })
</script>

<template>
  <span :class="['status-tag', `status-tag--${config.cls}`]">
    {{ config.label }}
  </span>
</template>

<style scoped>
.status-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
}

.status-tag--unverified {
  color: var(--color-text-muted);
  background: rgba(148, 163, 184, 0.1);
  border: 1px solid rgba(148, 163, 184, 0.3);
}

.status-tag--pending {
  color: #f59e0b;
  background: rgba(245, 158, 11, 0.15);
  border: 1px solid rgba(245, 158, 11, 0.3);
}

.status-tag--verified {
  color: #06b6d4;
  background: rgba(6, 182, 212, 0.15);
  border: 1px solid rgba(6, 182, 212, 0.3);
}

.status-tag--rejected {
  color: #ef4444;
  background: rgba(239, 68, 68, 0.15);
  border: 1px solid rgba(239, 68, 68, 0.3);
}
</style>
