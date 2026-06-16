<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { createRechargeOrder } from '@/api/wallet'
import type { RechargeOrderResult } from '@/types/wallet'
import { displayAmount } from '@/utils/display'

const form = reactive({
  amount: '',
  payment_method: 'wechat' as 'wechat' | 'alipay',
})
const submitting = ref(false)
const result = ref<RechargeOrderResult | null>(null)

async function handleSubmit() {
  if (!form.amount) {
    ElMessage.warning('请输入充值金额')
    return
  }
  submitting.value = true
  try {
    result.value = await createRechargeOrder({
      amount: form.amount,
      payment_method: form.payment_method,
      return_url: window.location.origin + '/wallet',
    })
    ElMessage.success('充值订单已创建')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="recharge-page">
    <div class="page-container">
      <div class="page-header">
        <h2 class="page-title">钱包充值</h2>
        <p class="page-subtitle">充值订单通过第三方支付完成，钱包支付仅用于商品购买订单</p>
      </div>

      <div class="recharge-layout">
        <div class="form-card glass-card">
          <el-form label-width="90px">
            <el-form-item label="充值金额" required>
              <el-input v-model="form.amount" placeholder="例如 100.00" />
            </el-form-item>
            <el-form-item label="支付方式" required>
              <el-radio-group v-model="form.payment_method">
                <el-radio-button label="wechat">微信支付</el-radio-button>
                <el-radio-button label="alipay">支付宝</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" :loading="submitting" @click="handleSubmit">创建充值订单</el-button>
            </el-form-item>
          </el-form>
        </div>

        <div v-if="result" class="result-card glass-card">
          <div class="section-title">支付信息</div>
          <div class="result-row"><span>订单号</span><strong>{{ result.order_no }}</strong></div>
          <div class="result-row"><span>金额</span><strong>{{ displayAmount(result.amount) }}</strong></div>
          <div class="result-row"><span>状态</span><strong>待支付</strong></div>
          <el-input :model-value="result.pay_url" readonly />
          <a class="pay-link" :href="result.pay_url" target="_blank" rel="noreferrer">
            打开第三方支付页面
          </a>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.recharge-page { padding: 32px 24px; }
.page-container { max-width: 960px; margin: 0 auto; }
.page-header { margin-bottom: 20px; }
.page-title { color: var(--color-text); font-size: 26px; margin-bottom: 8px; }
.page-subtitle { color: var(--color-text-muted); font-size: 14px; }
.recharge-layout {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.form-card,
.result-card {
  padding: 24px;
  border-radius: 8px;
}
.section-title {
  margin-bottom: 16px;
  color: var(--color-text);
  font-size: 18px;
  font-weight: 700;
}
.result-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  color: var(--color-text-muted);
  font-size: 13px;
}
.result-row strong {
  color: var(--color-text);
}
.pay-link {
  display: inline-flex;
  margin-top: 16px;
  color: var(--color-accent);
  text-decoration: none;
}
@media (max-width: 800px) {
  .recharge-layout {
    grid-template-columns: 1fr;
  }
}
</style>
