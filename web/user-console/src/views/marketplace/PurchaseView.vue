<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

onMounted(() => {
  router.replace(`/marketplace/${route.params.id}`)
})
</script>

<template>
  <div class="purchase-redirect">
    <div class="glass-card redirect-card">正在打开商品详情，请在套餐中确认购买。</div>
  </div>
</template>

<style scoped>
.purchase-redirect {
  padding: 32px 24px;
}
.redirect-card {
  max-width: 640px;
  margin: 0 auto;
  padding: 40px;
  border-radius: 8px;
  color: var(--color-text-muted);
  text-align: center;
}
</style>
