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
        layout="total, prev, pager, next, jumper"
        background
        @current-change="$emit('page-change', $event)"
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
}>()

// 统一表头样式（深色主题）
const headerStyle = {
  background: 'rgba(99, 102, 241, 0.1)',
  color: '#94A3B8',
  fontWeight: '500',
  fontSize: '13px',
  borderBottom: '1px solid rgba(99, 102, 241, 0.2)',
}

const rowStyle = {
  background: 'transparent',
}
</script>

<style scoped>
.data-table-wrap {
  width: 100%;
}

.data-table {
  --el-table-border-color: rgba(99, 102, 241, 0.15);
  --el-table-bg-color: rgba(255, 255, 255, 0.02);
  --el-table-tr-bg-color: transparent;
  --el-table-row-hover-bg-color: rgba(99, 102, 241, 0.08);
  --el-table-text-color: #F1F5F9;
  --el-table-header-text-color: #94A3B8;
  border-radius: 8px;
  overflow: hidden;
}

.pagination-wrap {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}
</style>
