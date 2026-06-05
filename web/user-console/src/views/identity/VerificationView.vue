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
    })
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

onMounted(fetchStatus)
</script>

<template>
  <div class="verification-page">
    <div class="page-container">
      <h2 class="page-title">实名认证</h2>

      <!-- 加载中 -->
      <div v-if="pageLoading" class="loading-wrapper">
        <el-skeleton :rows="4" animated />
      </div>

      <template v-else>
        <!-- ====== 审核通过（verified）====== -->
        <div v-if="verification?.status === 'verified'" class="status-card verified-card glass-card">
          <div class="status-icon verified-icon">✓</div>
          <h3 class="status-title">实名认证已完成</h3>
          <StatusTag status="verified" />
          <div class="info-list">
            <div class="info-item">
              <span class="info-label">姓名</span>
              <span class="info-value">{{ verification.real_name }}</span>
            </div>
            <div class="info-item">
              <span class="info-label">身份证号</span>
              <!-- 只展示后端返回的脱敏值，禁止前端展示原始号码 -->
              <span class="info-value id-masked">{{ verification.id_card_no_masked }}</span>
            </div>
            <div class="info-item">
              <span class="info-label">认证时间</span>
              <span class="info-value">{{ formatDate(verification.submitted_at) }}</span>
            </div>
          </div>
        </div>

        <!-- ====== 审核中（pending）====== -->
        <div v-else-if="verification?.status === 'pending'" class="status-card pending-card glass-card">
          <div class="status-icon pending-icon">🕐</div>
          <h3 class="status-title">审核中</h3>
          <StatusTag status="pending" />
          <div class="info-list">
            <div class="info-item">
              <span class="info-label">姓名</span>
              <span class="info-value">{{ verification.real_name }}</span>
            </div>
            <div class="info-item">
              <span class="info-label">身份证号</span>
              <span class="info-value id-masked">{{ verification.id_card_no_masked }}</span>
            </div>
            <div class="info-item">
              <span class="info-label">提交时间</span>
              <span class="info-value">{{ formatDate(verification.submitted_at) }}</span>
            </div>
          </div>
          <p class="pending-tip">
            审核通常在 1-3 个工作日内完成，请耐心等待。
          </p>
        </div>

        <!-- ====== 审核拒绝（rejected）+ 未提交（null）====== -->
        <template v-else>
          <!-- 拒绝原因展示 -->
          <div v-if="verification?.status === 'rejected'" class="status-card rejected-card glass-card">
            <div class="status-icon rejected-icon">✗</div>
            <h3 class="status-title">审核未通过</h3>
            <StatusTag status="rejected" />
            <div class="reject-reason">
              <span class="reject-label">拒绝原因：</span>
              {{ verification.reject_reason || '证件信息不符，请重新提交' }}
            </div>
          </div>

          <!-- 提交表单（unverified 或 rejected 时展示） -->
          <div v-if="showForm" class="form-card glass-card">
            <!-- 为什么需要实名认证 -->
            <div class="info-box">
              <p class="info-box-title">🔒 为什么需要实名认证？</p>
              <ul class="info-box-list">
                <li>购买商品和服务</li>
                <li>申请发票和退款</li>
                <li>账号安全保障</li>
              </ul>
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
                />
              </el-form-item>

              <el-form-item label="身份证号码" prop="id_card_no">
                <el-input
                  v-model="form.id_card_no"
                  placeholder="请输入18位身份证号码"
                  maxlength="18"
                  autocomplete="off"
                />
              </el-form-item>

              <el-form-item>
                <button
                  class="btn-primary"
                  :disabled="submitting"
                  @click.prevent="handleSubmit"
                >
                  {{ submitting ? '提交中...' : (verification?.status === 'rejected' ? '重新提交实名认证' : '提交实名认证') }}
                </button>
              </el-form-item>
            </el-form>

            <p class="privacy-tip">
              🔒 身份证信息严格加密存储，仅用于身份核实，绝不泄露第三方
            </p>
          </div>
        </template>
      </template>
    </div>
  </div>
</template>

<style scoped>
.verification-page {
  padding: 32px 24px;
}

.page-container {
  max-width: 640px;
  margin: 0 auto;
}

.page-title {
  font-size: 24px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 24px;
}

.loading-wrapper {
  padding: 24px;
}

/* 状态卡片 */
.status-card {
  padding: 32px;
  text-align: center;
  margin-bottom: 24px;
}

.status-icon {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  margin: 0 auto 16px;
}

.verified-icon {
  background: rgba(6, 182, 212, 0.15);
  color: #06b6d4;
  border: 2px solid rgba(6, 182, 212, 0.3);
}

.pending-icon {
  background: rgba(245, 158, 11, 0.15);
  color: #f59e0b;
  border: 2px solid rgba(245, 158, 11, 0.3);
}

.rejected-icon {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
  border: 2px solid rgba(239, 68, 68, 0.3);
}

.status-title {
  font-size: 20px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 12px;
}

/* 信息列表 */
.info-list {
  margin-top: 20px;
  text-align: left;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 8px;
  padding: 16px;
  border: 1px solid var(--color-border);
}

.info-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  border-bottom: 1px solid rgba(99, 102, 241, 0.08);
}

.info-item:last-child {
  border-bottom: none;
}

.info-label {
  color: var(--color-text-muted);
  font-size: 13px;
  width: 80px;
  flex-shrink: 0;
}

.info-value {
  color: var(--color-text);
  font-size: 14px;
}

.id-masked {
  font-family: 'Courier New', Courier, monospace;
  letter-spacing: 1px;
  color: var(--color-accent);
}

.pending-tip {
  margin-top: 16px;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1.6;
}

/* 拒绝原因 */
.reject-reason {
  margin-top: 16px;
  padding: 12px;
  background: rgba(239, 68, 68, 0.08);
  border: 1px solid rgba(239, 68, 68, 0.2);
  border-radius: 6px;
  color: #ef4444;
  font-size: 13px;
  text-align: left;
}

.reject-label {
  font-weight: 600;
}

/* 表单卡片 */
.form-card {
  padding: 32px;
}

/* 说明框 */
.info-box {
  background: rgba(99, 102, 241, 0.06);
  border: 1px solid rgba(99, 102, 241, 0.15);
  border-radius: 8px;
  padding: 16px;
  margin-bottom: 24px;
}

.info-box-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 10px;
}

.info-box-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.info-box-list li {
  font-size: 13px;
  color: var(--color-text-muted);
  padding: 3px 0;
  padding-left: 16px;
  position: relative;
}

.info-box-list li::before {
  content: '•';
  position: absolute;
  left: 0;
  color: var(--color-primary);
}

.verify-form {
  margin-top: 8px;
}

/* 隐私提示 */
.privacy-tip {
  margin-top: 16px;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 12px;
  line-height: 1.5;
}
</style>
