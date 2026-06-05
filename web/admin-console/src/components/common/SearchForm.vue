<template>
  <!-- 通用搜索栏，使用具名插槽放置搜索字段 -->
  <div class="search-form-wrap">
    <el-form
      :model="model"
      inline
      class="search-form"
      @submit.prevent="$emit('search')"
    >
      <slot />

      <el-form-item>
        <el-button
          type="primary"
          native-type="submit"
          :loading="loading"
          :icon="Search"
        >
          搜索
        </el-button>
        <el-button :icon="RefreshRight" @click="$emit('reset')">
          重置
        </el-button>
      </el-form-item>
    </el-form>
  </div>
</template>

<script setup lang="ts">
import { Search, RefreshRight } from '@element-plus/icons-vue'

defineProps<{
  model: Record<string, unknown>
  loading?: boolean
}>()

defineEmits<{
  search: []
  reset: []
}>()
</script>

<style scoped>
.search-form-wrap {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 10px;
  padding: 16px 20px 4px;
  margin-bottom: 16px;
  backdrop-filter: blur(12px);
  transition: box-shadow 0.2s;
}

.search-form-wrap:hover {
  box-shadow: 0 0 24px rgba(99, 102, 241, 0.15);
}
</style>
