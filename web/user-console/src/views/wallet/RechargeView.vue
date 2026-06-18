<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Link, Refresh, WalletFilled } from '@element-plus/icons-vue'
import { createRechargeOrder } from '@/api/wallet'
import type { RechargeOrderResult } from '@/types/wallet'
import { displayAmount } from '@/utils/display'

const amountOptions = ['50.00', '100.00', '200.00', '500.00', '1000.00', '2000.00']
const form = reactive({
  amount: '',
  payment_method: 'alipay' as const,
})
const submitting = ref(false)
const result = ref<RechargeOrderResult | null>(null)

function selectAmount(amount: string) {
  form.amount = amount
}

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
        <div>
          <span class="page-kicker">支付宝充值</span>
          <h2 class="page-title">钱包充值</h2>
          <p class="page-subtitle">当前仅支持支付宝充值；充值订单通过第三方支付完成。</p>
        </div>
      </div>

      <div class="recharge-layout">
        <div class="form-card glass-card">
          <div class="card-head">
            <div class="alipay-brand" aria-label="支付宝">
              <div class="alipay-logo" aria-hidden="true">支付宝</div>
              <div class="alipay-wordmark">
                <strong>支付宝</strong>
                <span>Alipay</span>
              </div>
            </div>
            <div>
              <h3>选择充值金额</h3>
              <p>金额按字符串提交，避免精度损失。</p>
            </div>
          </div>

          <div class="amount-grid">
            <button
              v-for="amount in amountOptions"
              :key="amount"
              class="amount-card"
              :class="{ active: form.amount === amount }"
              type="button"
              @click="selectAmount(amount)"
            >
              {{ displayAmount(amount) }}
            </button>
          </div>

          <div class="custom-amount-field">
            <div class="amount-field-head">
              <label class="field-label">自定义金额</label>
              <span>支持两位小数</span>
            </div>
            <el-input
              v-model="form.amount"
              class="amount-input"
              clearable
              inputmode="decimal"
              placeholder="例如 100.00"
            >
              <template #prefix>
                <span class="amount-prefix">¥</span>
              </template>
              <template #suffix>
                <span class="amount-suffix">CNY</span>
              </template>
            </el-input>
          </div>

          <div class="pay-method">
            <span>支付方式</span>
            <strong class="pay-method-brand">
              <span class="mini-alipay-logo" aria-hidden="true">支</span>
              支付宝
            </strong>
          </div>

          <el-button class="submit-btn" type="primary" :loading="submitting" @click="handleSubmit">
            <el-icon><WalletFilled /></el-icon>
            创建支付宝充值订单
          </el-button>
        </div>

        <div class="result-card glass-card">
          <template v-if="result">
            <div class="section-title">支付信息</div>
            <div class="result-row"><span>订单号</span><strong>{{ result.order_no }}</strong></div>
            <div class="result-row"><span>金额</span><strong>{{ displayAmount(result.amount) }}</strong></div>
            <div class="result-row"><span>状态</span><strong>待支付</strong></div>
            <el-input :model-value="result.pay_url" readonly />
            <a class="pay-link" :href="result.pay_url" target="_blank" rel="noreferrer">
              <el-icon><Link /></el-icon>
              打开支付宝支付页面
            </a>
          </template>
          <template v-else>
            <div class="empty-result">
              <div class="empty-icon"><el-icon><Refresh /></el-icon></div>
              <strong>等待创建充值订单</strong>
              <span>创建订单后，这里会显示订单号、金额和支付宝支付链接。</span>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.recharge-page {
  padding: 34px 0 0;
}

.page-container {
  max-width: 1060px;
}

.page-header {
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 42%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}

.page-kicker {
  display: inline-flex;
  margin-bottom: 10px;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}

.page-title {
  margin-bottom: 8px;
}

.recharge-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(340px, 0.9fr);
  gap: 16px;
  align-items: start;
}

.form-card,
.result-card {
  padding: 24px;
  border-radius: 8px;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 20px;
}

.alipay-brand {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 150px;
  padding: 8px 12px 8px 8px;
  border: 1px solid rgba(22, 119, 255, 0.42);
  border-radius: 8px;
  background: linear-gradient(135deg, rgba(22, 119, 255, 0.18), rgba(22, 119, 255, 0.08));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.08);
}

.alipay-logo {
  width: 48px;
  height: 38px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 7px;
  background: #1677ff;
  color: #fff;
  font-size: 14px;
  font-weight: 800;
  line-height: 1;
  letter-spacing: 0;
  box-shadow: 0 8px 18px rgba(22, 119, 255, 0.28);
}

.alipay-wordmark {
  display: grid;
  gap: 2px;
}

.alipay-wordmark strong {
  color: var(--color-text);
  font-size: 16px;
  line-height: 1;
}

.alipay-wordmark span {
  color: #8ec5ff;
  font-size: 12px;
  line-height: 1;
}

.card-head h3 {
  color: var(--color-text);
  font-size: 18px;
  margin-bottom: 4px;
}

.card-head p {
  color: var(--color-text-muted);
  font-size: 13px;
}

.amount-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 18px;
}

.amount-card {
  height: 56px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.72);
  color: var(--color-text);
  font-size: 16px;
  font-weight: 700;
  cursor: pointer;
  transition: background 0.2s, border-color 0.2s, color 0.2s, transform 0.2s;
}

.amount-card:hover {
  border-color: rgba(22, 119, 255, 0.48);
  background: rgba(22, 119, 255, 0.12);
  transform: translateY(-1px);
}

.amount-card.active {
  border-color: rgba(22, 119, 255, 0.78);
  background: linear-gradient(135deg, rgba(22, 119, 255, 0.26), rgba(22, 119, 255, 0.12));
  color: #dcecff;
  box-shadow: 0 10px 22px rgba(22, 119, 255, 0.18);
}

.custom-amount-field {
  margin-bottom: 16px;
  padding: 14px;
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(22, 119, 255, 0.08), transparent 42%),
    rgba(15, 23, 42, 0.52);
}

.amount-field-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.amount-field-head span {
  color: #8ec5ff;
  font-size: 12px;
}

.field-label {
  display: inline-flex;
  color: var(--color-text-muted);
  font-size: 13px;
}

.amount-input {
  --el-input-height: 46px;
}

.amount-input :deep(.el-input__wrapper) {
  border-radius: 8px;
  background: rgba(2, 6, 23, 0.66) !important;
  box-shadow:
    0 0 0 1px rgba(22, 119, 255, 0.26) inset,
    0 10px 20px rgba(0, 0, 0, 0.14) !important;
  transition: box-shadow 0.2s, background 0.2s;
}

.amount-input :deep(.el-input__wrapper:hover) {
  background: rgba(2, 6, 23, 0.78) !important;
  box-shadow:
    0 0 0 1px rgba(22, 119, 255, 0.5) inset,
    0 12px 24px rgba(22, 119, 255, 0.12) !important;
}

.amount-input :deep(.el-input__wrapper.is-focus) {
  background: rgba(2, 6, 23, 0.82) !important;
  box-shadow:
    0 0 0 1px rgba(22, 119, 255, 0.82) inset,
    0 0 0 3px rgba(22, 119, 255, 0.14),
    0 14px 28px rgba(22, 119, 255, 0.14) !important;
}

.amount-input :deep(.el-input__inner) {
  color: #f8fbff !important;
  font-size: 18px;
  font-weight: 800;
}

.amount-prefix {
  color: #8ec5ff;
  font-size: 18px;
  font-weight: 800;
}

.amount-suffix {
  color: rgba(142, 197, 255, 0.86);
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0;
}

.pay-method {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 18px;
  padding: 14px;
  border: 1px solid rgba(22, 119, 255, 0.24);
  border-radius: 8px;
  background: rgba(22, 119, 255, 0.08);
}

.pay-method span {
  color: var(--color-text-muted);
  font-size: 13px;
}

.pay-method strong {
  color: #8ec5ff;
}

.pay-method-brand {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}

.mini-alipay-logo {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 6px;
  background: #1677ff;
  color: #fff;
  font-size: 15px;
  font-weight: 800;
  line-height: 1;
}

.submit-btn {
  width: 100%;
  height: 44px;
  border: none;
  border-radius: 8px;
  background: linear-gradient(135deg, #1677ff, #40a9ff) !important;
  color: #fff !important;
  font-weight: 800;
  box-shadow: 0 12px 24px rgba(22, 119, 255, 0.24);
}

.submit-btn:hover {
  background: linear-gradient(135deg, #2482ff, #5ab5ff) !important;
  box-shadow: 0 14px 28px rgba(22, 119, 255, 0.3);
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
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 44px;
  margin-top: 16px;
  border: 1px solid rgba(22, 119, 255, 0.45);
  border-radius: 8px;
  background: rgba(22, 119, 255, 0.14);
  color: #8ec5ff;
  text-decoration: none;
  font-weight: 700;
  transition: background 0.2s, border-color 0.2s, color 0.2s;
}

.pay-link:hover {
  border-color: rgba(22, 119, 255, 0.72);
  background: rgba(22, 119, 255, 0.22);
  color: #dcecff;
}

.empty-result {
  min-height: 258px;
  display: grid;
  place-items: center;
  align-content: center;
  gap: 10px;
  text-align: center;
}

.empty-icon {
  width: 48px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  color: var(--color-primary);
  background: rgba(34, 211, 238, 0.1);
}

.empty-result strong {
  color: var(--color-text);
}

.empty-result span {
  max-width: 280px;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

@media (max-width: 800px) {
  .recharge-layout {
    grid-template-columns: 1fr;
  }

  .amount-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
