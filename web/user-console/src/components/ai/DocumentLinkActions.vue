<script setup lang="ts">
import { Document, Link, Promotion } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

type HealthStatus = 'unpublished' | 'unknown' | 'healthy' | 'unhealthy'
const props = defineProps<{
  introUrl?: string
  introStatus?: HealthStatus
  quickStartUrl?: string
  quickStartStatus?: HealthStatus
  docsUrl?: string
  docsStatus?: HealthStatus
}>()

function disabledReason(url: string | undefined, status: HealthStatus | undefined) {
  if (!url || status === 'unpublished') return '尚未发布'
  if (status === 'unhealthy') return '健康检查异常'
  if (status !== 'healthy') return '等待健康检查'
  return ''
}

function openDocument(url: string | undefined, status: HealthStatus | undefined, label: string) {
  const reason = disabledReason(url, status)
  if (reason) {
    ElMessage.info(`${label}${reason}`)
    return
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}
</script>

<template>
  <div class="document-actions">
    <el-tooltip :content="disabledReason(props.introUrl, props.introStatus) || '在安全新窗口打开模型介绍'">
      <span><el-button :icon="Document" :disabled="Boolean(disabledReason(props.introUrl, props.introStatus))" @click="openDocument(props.introUrl, props.introStatus, '模型介绍')">模型介绍</el-button></span>
    </el-tooltip>
    <el-tooltip :content="disabledReason(props.quickStartUrl, props.quickStartStatus) || '在安全新窗口打开快速入门'">
      <span><el-button type="primary" :icon="Promotion" :disabled="Boolean(disabledReason(props.quickStartUrl, props.quickStartStatus))" @click="openDocument(props.quickStartUrl, props.quickStartStatus, '快速入门')">快速入门</el-button></span>
    </el-tooltip>
    <el-tooltip :content="disabledReason(props.docsUrl, props.docsStatus) || '在安全新窗口打开 API 文档'">
      <span><el-button :icon="Link" :disabled="Boolean(disabledReason(props.docsUrl, props.docsStatus))" @click="openDocument(props.docsUrl, props.docsStatus, 'API 文档')">API 文档</el-button></span>
    </el-tooltip>
  </div>
</template>

<style scoped>
.document-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.document-actions > span { display: inline-flex; }
.document-actions :deep(.el-button + .el-button) { margin-left: 0; }
@media (max-width: 520px) { .document-actions > span, .document-actions :deep(.el-button) { flex: 1; min-height: 44px; } }
</style>
