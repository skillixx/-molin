<template>
  <!-- 403 无权限页面 -->
  <div class="forbidden-page">
    <div class="content">
      <div class="code">403</div>
      <h2>无访问权限</h2>
      <p>您没有访问此页面的权限，请联系系统管理员。</p>
      <el-button type="primary" @click="router.back()">返回上一页</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
const router = useRouter()
</script>

<style scoped>
.forbidden-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #0A0F1E;
  color: #F1F5F9;
}

.content {
  text-align: center;
}

.code {
  font-size: 96px;
  font-weight: 800;
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  line-height: 1;
  margin-bottom: 16px;
}

h2 {
  font-size: 24px;
  margin: 0 0 8px;
  color: #F1F5F9;
}

p {
  color: #94A3B8;
  margin-bottom: 24px;
}
</style>
