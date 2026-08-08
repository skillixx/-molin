<script setup lang="ts">
import { computed } from 'vue'
import type { AIPriceSKU } from '@/types/aiGateway'

const props = defineProps<{ prices: AIPriceSKU[]; compact?: boolean }>()

const visiblePrices = computed(() => props.compact ? props.prices.slice(0, 2) : props.prices)

const meterLabels: Record<string, string> = {
  input_tokens: '输入',
  output_tokens: '输出',
  cached_tokens: '缓存读取',
  reasoning_tokens: '推理',
}

function priceText(item: AIPriceSKU) {
  const scale = Number(item.scale)
  const unit = scale === 1_000_000 ? '/ 百万 Token' : `/ ${item.scale} Token`
  return `¥${item.sale_unit_price} ${unit}`
}
</script>

<template>
  <div class="price-summary" :class="{ compact }">
    <div v-for="item in visiblePrices" :key="item.meter_type" class="price-item">
      <span class="price-label">{{ meterLabels[item.meter_type] || item.meter_type }}</span>
      <strong>{{ priceText(item) }}</strong>
    </div>
  </div>
</template>

<style scoped>
.price-summary { display: flex; flex-wrap: wrap; gap: 12px 20px; }
.price-item { min-width: 150px; display: grid; gap: 3px; }
.price-label { color: var(--color-text-muted); font-size: 12px; }
.price-item strong { color: var(--color-accent-warm); font-size: 14px; font-variant-numeric: tabular-nums; letter-spacing: 0; }
.compact { gap: 8px 16px; }
.compact .price-item { min-width: 135px; }
@media (max-width: 520px) { .price-summary, .price-item { width: 100%; } }
</style>
