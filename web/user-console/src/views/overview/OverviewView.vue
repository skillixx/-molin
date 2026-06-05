<script setup lang="ts">
/**
 * 总览页（Week 2 实现）
 * 展示余额、最近资产、公告等
 */
</script>

<template>
  <div class="overview-page">
    <div class="page-container">
      <h2 class="page-title">总览</h2>
      <div class="coming-soon glass-card">
        <p>📊 总览功能正在开发中，Week 2 上线</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.overview-page { padding: 32px 24px; }
.page-container { max-width: 1280px; margin: 0 auto; }
.page-title { font-size: 24px; font-weight: 600; color: var(--color-text); margin-bottom: 24px; }
.coming-soon {
  padding: 60px;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 15px;
}
</style>
