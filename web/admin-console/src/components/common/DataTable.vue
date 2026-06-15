<template>
  <!-- 通用数据表格，含分页 -->
  <div class="data-table-wrap">
    <el-table
      :data="data"
      v-loading="loading"
      border
      stripe
      class="data-table"
      :header-cell-style="headerStyle"
      :row-style="rowStyle"
    >
      <slot />
    </el-table>

    <!-- 分页 -->
    <div v-if="total > 0" class="pagination-wrap">
      <el-pagination
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        :page-sizes="[10, 20, 50, 100]"
        layout="total, sizes, prev, pager, next, jumper"
        background
        @current-change="$emit('page-change', $event)"
        @size-change="$emit('page-size-change', $event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  data: unknown[]
  loading: boolean
  total: number
  page: number
  pageSize: number
}>()

defineEmits<{
  'page-change': [page: number]
  'page-size-change': [pageSize: number]
}>()

// 统一表头样式（深色主题）
const headerStyle = {
  background: '#1a263b',
  color: '#8fa3bd',
  fontWeight: '600',
  fontSize: '13px',
  borderBottom: '1px solid rgba(148, 163, 184, 0.18)',
}

const rowStyle = {
  background: '#162033',
  color: '#e5edf7',
}
</script>

<style scoped>
.data-table-wrap {
  width: 100%;
}

.data-table {
  --el-table-border-color: var(--mc-border-soft);
  --el-table-bg-color: var(--mc-surface);
  --el-table-tr-bg-color: var(--mc-surface);
  --el-table-row-hover-bg-color: var(--mc-surface-muted);
  --el-table-text-color: var(--mc-text);
  --el-table-header-text-color: var(--mc-text-muted);
  border-radius: 8px;
  overflow: hidden;
}

.pagination-wrap {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}
</style>
