<script setup lang="ts">
/**
 * 实名认证页
 * 进页时先查询当前状态，根据 4 种状态分别展示不同 UI：
 * - unverified：显示提交表单
 * - pending：显示审核中状态
 * - verified：显示已通过
 * - rejected：显示拒绝原因 + 重新提交按钮
 *
 * 安全规范：身份证号仅展示后端返回的脱敏值，禁止前端自行脱敏
 */
import { ref, reactive, onMounted } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { getMyVerification, submitVerification } from '@/api/identity'
import { useAuthStore } from '@/stores/auth'
import StatusTag from '@/components/common/StatusTag.vue'
import type { IdentityVerification } from '@/types/auth'

const authStore = useAuthStore()

// 当前实名状态数据
const verification = ref<IdentityVerification | null>(null)
// 页面加载状态
const pageLoading = ref(true)
// 是否展示表单（unverified 或 rejected 后点击重新提交）
const showForm = ref(false)
// 提交状态
const submitting = ref(false)

// 提交表单
const form = reactive({ real_name: '', id_card_no: '' })
const formRef = ref<FormInstance>()

// 身份证号校验（18位，含 X）
const idCardValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入身份证号码'))
  if (!/^\d{17}[\dXx]$/.test(value)) return callback(new Error('请输入正确的18位身份证号码'))
  callback()
}

const formRules: FormRules = {
  real_name: [
    { required: true, message: '请输入真实姓名', trigger: 'blur' },
    { min: 2, max: 20, message: '姓名长度为 2-20 个字符', trigger: 'blur' },
  ],
  id_card_no: [{ validator: idCardValidator, trigger: 'blur' }],
}

// 拉取当前实名状态
async function fetchStatus() {
  pageLoading.value = true
  try {
    verification.value = await getMyVerification()
    // 未提交或被拒绝时直接展示表单
    showForm.value = !verification.value || verification.value.status === 'rejected'
  } catch (err: unknown) {
    // 如果是 404（未提交过），视为 unverified
    const axiosErr = err as { response?: { status?: number } }
    if (axiosErr?.response?.status === 404) {
      verification.value = null
      showForm.value = true
    }
  } finally {
    pageLoading.value = false
  }
}

// 提交实名认证
async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    verification.value = await submitVerification({
      real_name: form.real_name,
      id_card_no: form.id_card_no,
      verification_type: 'id_card',
    })
    await fetchStatus()
    showForm.value = false
    // 刷新 authStore 用户信息（更新 real_name_status）
    await authStore.fetchMe()
    ElMessage.success('实名认证已提交，请等待审核')
    // 清空表单
    form.real_name = ''
    form.id_card_no = ''
  } finally {
    submitting.value = false
  }
}

// 格式化时间
function formatDate(dateStr: string) {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function idCardMasked(value?: string) {
  return value || '--'
}

onMounted(fetchStatus)
</script>

<template>
  <div class="verification-page page-bg">
    <div class="page-container">
      <header class="page-header">
        <div>
          <p class="page-kicker">Identity Verification</p>
          <h2 class="page-title">实名认证</h2>
          <p class="page-desc">完成实名后可购买商品、开通资产并使用需要身份校验的服务。</p>
        </div>
        <StatusTag :status="verification?.status || 'unverified'" />
      </header>

      <!-- 加载中 -->
      <div v-if="pageLoading" class="loading-wrapper">
        <el-skeleton :rows="4" animated />
      </div>

      <template v-else>
        <div class="identity-layout">
          <aside class="status-panel glass-card">
            <div
              class="status-orb"
              :class="{
                'status-orb--verified': verification?.status === 'verified',
                'status-orb--pending': verification?.status === 'pending',
                'status-orb--rejected': verification?.status === 'rejected',
              }"
            >
              <el-icon v-if="verification?.status === 'verified'"><CircleCheck /></el-icon>
              <el-icon v-else-if="verification?.status === 'pending'"><Clock /></el-icon>
              <el-icon v-else-if="verification?.status === 'rejected'"><CircleClose /></el-icon>
              <el-icon v-else><UserFilled /></el-icon>
            </div>
            <p class="status-label">当前状态</p>
            <h3 class="status-title">
              <span v-if="verification?.status === 'verified'">已完成认证</span>
              <span v-else-if="verification?.status === 'pending'">资料审核中</span>
              <span v-else-if="verification?.status === 'rejected'">审核未通过</span>
              <span v-else>等待提交资料</span>
            </h3>
            <p class="status-desc">
              <span v-if="verification?.status === 'verified'">你的账号已获得实名权益，可继续购买和开通资源。</span>
              <span v-else-if="verification?.status === 'pending'">审核通常在 1-3 个工作日内完成，期间部分功能仍会受限。</span>
              <span v-else-if="verification?.status === 'rejected'">请根据拒绝原因修正资料后重新提交。</span>
              <span v-else>提交真实姓名和身份证号码后，将进入人工审核流程。</span>
            </p>

            <div class="benefit-list">
              <div class="benefit-item">
                <el-icon><ShoppingCart /></el-icon>
                <span>购买商品与服务</span>
              </div>
              <div class="benefit-item">
                <el-icon><Wallet /></el-icon>
                <span>开通资产和权益</span>
              </div>
              <div class="benefit-item">
                <el-icon><Lock /></el-icon>
                <span>提升账号安全等级</span>
              </div>
            </div>
          </aside>

          <section class="content-panel glass-card">
            <!-- ====== 审核通过（verified）====== -->
            <div v-if="verification?.status === 'verified'" class="result-block">
              <div class="section-heading">
                <span class="section-badge">Verified</span>
                <h3>认证信息</h3>
              </div>
              <div class="info-list">
                <div class="info-item">
                  <span class="info-label">姓名</span>
                  <span class="info-value">{{ verification.real_name || '--' }}</span>
                </div>
                <div class="info-item">
                  <span class="info-label">身份证号</span>
                  <!-- 只展示后端返回的脱敏值，禁止前端展示原始号码 -->
                  <span class="info-value id-masked">{{ idCardMasked(verification.id_card_no_masked) }}</span>
                </div>
                <div class="info-item">
                  <span class="info-label">认证时间</span>
                  <span class="info-value">{{ formatDate(verification.submitted_at || '') }}</span>
                </div>
              </div>
            </div>

            <!-- ====== 审核中（pending）====== -->
            <div v-else-if="verification?.status === 'pending'" class="result-block">
              <div class="section-heading">
                <span class="section-badge section-badge--warning">Pending</span>
                <h3>已提交资料</h3>
              </div>
              <div class="info-list">
                <div class="info-item">
                  <span class="info-label">姓名</span>
                  <span class="info-value">{{ verification.real_name || '--' }}</span>
                </div>
                <div class="info-item">
                  <span class="info-label">身份证号</span>
                  <span class="info-value id-masked">{{ idCardMasked(verification.id_card_no_masked) }}</span>
                </div>
                <div class="info-item">
                  <span class="info-label">提交时间</span>
                  <span class="info-value">{{ formatDate(verification.submitted_at || '') }}</span>
                </div>
              </div>
              <p class="pending-tip">
                审核完成后，页面顶部实名状态会自动更新。请勿重复提交相同资料。
              </p>
            </div>

            <!-- ====== 审核拒绝（rejected）+ 未提交（null）====== -->
            <template v-else>
              <div v-if="verification?.status === 'rejected'" class="reject-reason">
                <div class="reject-icon">
                  <el-icon><WarningFilled /></el-icon>
                </div>
                <div>
                  <span class="reject-label">拒绝原因</span>
                  <p>{{ verification.reject_reason || '证件信息不符，请重新提交' }}</p>
                </div>
              </div>

              <!-- 提交表单（unverified 或 rejected 时展示） -->
              <div v-if="showForm" class="form-area">
                <div class="section-heading">
                  <span class="section-badge">Submit</span>
                  <h3>{{ verification?.status === 'rejected' ? '重新提交实名资料' : '提交实名资料' }}</h3>
                  <p>请填写与身份证一致的信息，提交后等待平台审核。</p>
                </div>

                <el-form
                  ref="formRef"
                  :model="form"
                  :rules="formRules"
                  label-position="top"
                  class="verify-form"
                >
                  <el-form-item label="真实姓名" prop="real_name">
                    <el-input
                      v-model="form.real_name"
                      placeholder="请输入身份证上的姓名"
                      maxlength="20"
                      autocomplete="name"
                      size="large"
                    />
                  </el-form-item>

                  <el-form-item label="身份证号码" prop="id_card_no">
                    <el-input
                      v-model="form.id_card_no"
                      placeholder="请输入18位身份证号码"
                      maxlength="18"
                      autocomplete="off"
                      size="large"
                    />
                  </el-form-item>

                  <el-form-item>
                    <button
                      class="btn-primary submit-btn"
                      :disabled="submitting"
                      @click.prevent="handleSubmit"
                    >
                      {{ submitting ? '提交中...' : (verification?.status === 'rejected' ? '重新提交实名认证' : '提交实名认证') }}
                    </button>
                  </el-form-item>
                </el-form>

                <div class="privacy-tip">
                  <el-icon><Lock /></el-icon>
                  <span>身份证信息由后端加密处理，仅用于身份核实，页面只展示脱敏数据。</span>
                </div>
              </div>
            </template>
          </section>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.verification-page {
  min-height: 100%;
  padding: 34px 24px 56px;
  position: relative;
  overflow: hidden;
}

.verification-page::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 14% 16%, rgba(6, 182, 212, 0.16), transparent 24%),
    radial-gradient(circle at 82% 12%, rgba(139, 92, 246, 0.14), transparent 26%);
  pointer-events: none;
}

.page-container {
  position: relative;
  z-index: 1;
  max-width: 1120px;
  margin: 0 auto;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 18px;
  margin-bottom: 24px;
}

.page-kicker {
  color: var(--color-accent);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  margin-bottom: 8px;
}

.page-title {
  font-size: 30px;
  line-height: 1.2;
  font-weight: 800;
  color: var(--color-text);
  margin-bottom: 10px;
}

.page-desc {
  color: var(--color-text-muted);
  font-size: 14px;
  line-height: 1.7;
}

.loading-wrapper {
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 16px;
  background: rgba(11, 16, 32, 0.72);
}

.identity-layout {
  display: grid;
  grid-template-columns: minmax(280px, 0.76fr) minmax(0, 1.24fr);
  gap: 22px;
  align-items: start;
}

.status-panel,
.content-panel {
  background: rgba(11, 16, 32, 0.74);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.28);
}

.status-panel {
  min-height: 500px;
  padding: 30px;
  position: sticky;
  top: 24px;
  overflow: hidden;
}

.status-panel::after {
  content: "";
  position: absolute;
  right: -120px;
  bottom: -140px;
  width: 300px;
  height: 300px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(6, 182, 212, 0.16), transparent 66%);
  pointer-events: none;
}

.status-orb {
  width: 72px;
  height: 72px;
  border-radius: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-accent);
  font-size: 34px;
  margin-bottom: 28px;
  background: rgba(6, 182, 212, 0.12);
  border: 1px solid rgba(6, 182, 212, 0.28);
  box-shadow: 0 18px 42px rgba(6, 182, 212, 0.14);
}

.status-orb--verified {
  color: var(--color-success);
  background: rgba(16, 185, 129, 0.13);
  border-color: rgba(16, 185, 129, 0.3);
}

.status-orb--pending {
  color: var(--color-warning);
  background: rgba(245, 158, 11, 0.13);
  border-color: rgba(245, 158, 11, 0.3);
}

.status-orb--rejected {
  color: var(--color-danger);
  background: rgba(239, 68, 68, 0.13);
  border-color: rgba(239, 68, 68, 0.3);
}

.status-label {
  color: var(--color-text-muted);
  font-size: 13px;
  margin-bottom: 8px;
}

.status-title {
  color: var(--color-text);
  font-size: 26px;
  line-height: 1.25;
  font-weight: 800;
  margin-bottom: 12px;
}

.status-desc {
  color: var(--color-text-muted);
  font-size: 14px;
  line-height: 1.8;
  min-height: 76px;
}

.benefit-list {
  display: grid;
  gap: 12px;
  margin-top: 34px;
  position: relative;
  z-index: 1;
}

.benefit-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 46px;
  padding: 12px 14px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--color-text);
  font-size: 13px;
}

.benefit-item .el-icon {
  color: var(--color-accent);
  font-size: 17px;
}

.content-panel {
  padding: 30px;
}

.section-heading {
  margin-bottom: 22px;
}

.section-badge {
  display: inline-flex;
  align-items: center;
  color: var(--color-accent);
  border: 1px solid rgba(6, 182, 212, 0.28);
  background: rgba(6, 182, 212, 0.08);
  border-radius: 999px;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 700;
  margin-bottom: 12px;
}

.section-badge--warning {
  color: var(--color-warning);
  border-color: rgba(245, 158, 11, 0.28);
  background: rgba(245, 158, 11, 0.08);
}

.section-heading h3 {
  color: var(--color-text);
  font-size: 22px;
  line-height: 1.25;
  margin-bottom: 8px;
}

.section-heading p {
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

.info-list {
  display: grid;
  gap: 12px;
}

.info-item {
  display: grid;
  grid-template-columns: 110px minmax(0, 1fr);
  gap: 18px;
  align-items: center;
  min-height: 58px;
  padding: 14px 16px;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: rgba(99, 102, 241, 0.055);
}

.info-label {
  color: var(--color-text-muted);
  font-size: 13px;
}

.info-value {
  min-width: 0;
  color: var(--color-text);
  font-size: 15px;
  text-align: right;
  word-break: break-word;
}

.id-masked {
  color: var(--color-accent);
  font-family: 'Courier New', Courier, monospace;
  letter-spacing: 1px;
}

.pending-tip {
  margin-top: 18px;
  padding: 14px 16px;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.7;
  border: 1px solid rgba(245, 158, 11, 0.2);
  border-radius: 12px;
  background: rgba(245, 158, 11, 0.07);
}

.reject-reason {
  display: grid;
  grid-template-columns: 42px 1fr;
  gap: 14px;
  padding: 16px;
  margin-bottom: 22px;
  background: rgba(239, 68, 68, 0.08);
  border: 1px solid rgba(239, 68, 68, 0.22);
  border-radius: 14px;
}

.reject-icon {
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-danger);
  border-radius: 12px;
  background: rgba(239, 68, 68, 0.12);
}

.reject-label {
  display: block;
  color: var(--color-danger);
  font-size: 13px;
  font-weight: 700;
  margin-bottom: 6px;
}

.reject-reason p {
  color: var(--color-text);
  font-size: 13px;
  line-height: 1.7;
}

.verify-form {
  margin-top: 8px;
}

.submit-btn {
  height: 46px;
  margin-top: 4px;
}

.privacy-tip {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  margin-top: 16px;
  padding: 12px 14px;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.6;
  border: 1px solid rgba(6, 182, 212, 0.18);
  border-radius: 12px;
  background: rgba(6, 182, 212, 0.06);
}

.privacy-tip .el-icon {
  flex-shrink: 0;
  color: var(--color-accent);
  margin-top: 2px;
}

:deep(.el-input__wrapper) {
  min-height: 42px;
  border-radius: 9px;
}

@media (max-width: 900px) {
  .identity-layout {
    grid-template-columns: 1fr;
  }

  .status-panel {
    position: relative;
    top: auto;
    min-height: auto;
  }
}

@media (max-width: 640px) {
  .verification-page {
    padding: 24px 14px 40px;
  }

  .page-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .page-title {
    font-size: 26px;
  }

  .status-panel,
  .content-panel {
    padding: 22px;
  }

  .info-item {
    grid-template-columns: 1fr;
    gap: 6px;
  }

  .info-value {
    text-align: left;
  }
}
</style>
