<script setup lang="ts">
/**
 * 商品市场页（Week 1 骨架版）
 * Week 1：展示骨架屏占位 + 静态占位卡片
 * TODO: Week 2 接入 GET /api/products，替换为真实数据
 */

import { ref, onMounted } from 'vue'

// 骨架屏数量
const skeletonCount = 6
// 模拟加载状态（Week 2 替换为真实接口调用）
const loading = ref(true)

// Week 1 模拟延迟后关闭骨架屏，展示占位卡片
onMounted(() => {
  setTimeout(() => {
    loading.value = false
  }, 800)
})
</script>

<template>
  <div class="marketplace-page">
    <div class="page-container">
      <!-- 页面标题 -->
      <div class="page-header">
        <h2 class="page-title">商品市场</h2>
        <p class="page-subtitle">探索墨灵提供的云服务与 AI 能力</p>
      </div>

      <!-- 商品网格 -->
      <div class="product-grid">
        <!-- 骨架屏（加载中）-->
        <template v-if="loading">
          <div
            v-for="i in skeletonCount"
            :key="i"
            class="product-card glass-card"
          >
            <el-skeleton animated>
              <template #template>
                <div class="skeleton-content">
                  <el-skeleton-item variant="circle" class="skeleton-icon" />
                  <el-skeleton-item variant="h3" class="skeleton-title" />
                  <el-skeleton-item variant="text" class="skeleton-text" />
                  <el-skeleton-item variant="text" class="skeleton-text short" />
                  <el-skeleton-item variant="button" class="skeleton-btn" />
                </div>
              </template>
            </el-skeleton>
          </div>
        </template>

        <!-- Week 1 静态占位卡片（骨架后展示，等 Week 2 替换）-->
        <!-- TODO: Week 2 接入 GET /api/products，用真实数据替换以下占位卡片 -->
        <template v-else>
          <div
            v-for="item in placeholderProducts"
            :key="item.id"
            class="product-card glass-card"
          >
            <div class="product-icon">{{ item.icon }}</div>
            <h3 class="product-name">{{ item.name }}</h3>
            <p class="product-desc">{{ item.description }}</p>
            <p class="product-price">
              <span class="price-value">¥ {{ item.startPrice }}</span>
              <span class="price-unit"> / 月起</span>
            </p>
            <button class="product-btn btn-primary" disabled>
              查看详情（即将开放）
            </button>
          </div>
        </template>
      </div>

      <!-- 空状态提示 -->
      <div v-if="!loading && placeholderProducts.length === 0" class="empty-state">
        <p>暂无商品，敬请期待</p>
      </div>
    </div>
  </div>
</template>

<script lang="ts">
// 占位商品数据（Week 2 接入真实接口后删除）
const placeholderProducts = [
  {
    id: 1,
    icon: '🚀',
    name: 'GPU 算力包',
    description: '高性能 GPU 计算资源，按量计费，弹性扩展',
    startPrice: '99',
  },
  {
    id: 2,
    icon: '🤖',
    name: 'AI 对话接口',
    description: '接入大语言模型，支持多轮对话和知识库',
    startPrice: '49',
  },
  {
    id: 3,
    icon: '☁️',
    name: '云存储服务',
    description: '安全可靠的对象存储，多地冗余备份',
    startPrice: '9',
  },
  {
    id: 4,
    icon: '⚡',
    name: '推理加速',
    description: '模型推理加速方案，延迟降低 80%',
    startPrice: '199',
  },
  {
    id: 5,
    icon: '🔒',
    name: '安全防护',
    description: 'DDoS 防护 + WAF 应用防火墙',
    startPrice: '29',
  },
  {
    id: 6,
    icon: '📊',
    name: '数据分析',
    description: '实时数据分析平台，可视化报表',
    startPrice: '79',
  },
]
</script>

<style scoped>
.marketplace-page {
  padding: 32px 24px;
}

.page-container {
  max-width: 1280px;
  margin: 0 auto;
}

.page-header {
  margin-bottom: 32px;
}

.page-title {
  font-size: 28px;
  font-weight: 700;
  color: var(--color-text);
  margin-bottom: 8px;
}

.page-subtitle {
  color: var(--color-text-muted);
  font-size: 15px;
}

/* 商品网格 */
.product-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 20px;
}

/* 商品卡片 */
.product-card {
  padding: 28px;
  cursor: pointer;
  transition: box-shadow 0.3s, border-color 0.3s, transform 0.2s;
}

.product-card:hover {
  box-shadow: var(--shadow-glow);
  border-color: rgba(99, 102, 241, 0.4);
  transform: translateY(-2px);
}

.product-icon {
  font-size: 36px;
  margin-bottom: 16px;
  line-height: 1;
}

.product-name {
  font-size: 18px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 8px;
}

.product-desc {
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.6;
  margin-bottom: 16px;
  min-height: 42px;
}

.product-price {
  margin-bottom: 16px;
}

.price-value {
  font-size: 22px;
  font-weight: 700;
  color: var(--color-accent);
}

.price-unit {
  font-size: 13px;
  color: var(--color-text-muted);
}

.product-btn {
  display: block;
  width: 100%;
  height: 40px;
  font-size: 14px;
}

.product-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
  filter: none !important;
}

/* 骨架屏内容 */
.skeleton-content {
  padding: 4px 0;
}

.skeleton-icon {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  margin-bottom: 16px;
}

.skeleton-title {
  width: 60% !important;
  margin-bottom: 12px;
}

.skeleton-text {
  width: 100% !important;
  margin-bottom: 8px;
}

.skeleton-text.short {
  width: 70% !important;
}

.skeleton-btn {
  width: 100% !important;
  height: 40px !important;
  margin-top: 16px;
}

/* 空状态 */
.empty-state {
  text-align: center;
  padding: 60px;
  color: var(--color-text-muted);
}
</style>
