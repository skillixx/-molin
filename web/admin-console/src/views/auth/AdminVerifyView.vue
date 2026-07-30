<template>
  <!-- 管理员双重认证页面 -->
  <div class="verify-page">
    <div class="verify-card">
      <!-- 顶部标题 -->
      <div class="verify-header">
        <div class="brand-logo">
          <el-icon size="32" class="brand-icon"><Lock /></el-icon>
        </div>
        <h2 class="verify-title">管理员安全认证</h2>
        <p class="verify-subtitle">
          访问管理功能前，请完成手机 + 邮箱双重验证（有效期 24 小时）
        </p>
      </div>

      <!-- 步骤条 -->
      <el-steps :active="currentStep" finish-status="success" class="verify-steps" align-center>
        <el-step title="发送手机验证码" />
        <el-step title="验证手机" />
        <el-step title="发送邮箱验证码" />
        <el-step title="验证邮箱" />
      </el-steps>

      <!-- 步骤内容区 -->
      <div class="step-content">
        <!-- Step 0：发送手机验证码 -->
        <el-card v-if="currentStep === 0" class="step-card">
          <div class="step-inner">
            <el-icon size="40" class="step-icon phone-icon"><Iphone /></el-icon>
            <h3 class="step-title">验证手机号</h3>
            <p class="step-desc">
              验证码将发送至您的手机：
              <span class="highlight-text">{{ maskedPhone }}</span>
            </p>
            <el-button
              class="action-btn"
              :loading="sendingPhone"
              :disabled="sendingVerificationCode || phoneCountdown > 0"
              @click="handleSendPhoneCode"
            >
              {{ phoneCountdown > 0 ? `重新发送（${phoneCountdown}s）` : '发送验证码' }}
            </el-button>
          </div>
        </el-card>

        <!-- Step 1：输入手机验证码 -->
        <el-card v-if="currentStep === 1" class="step-card">
          <div class="step-inner">
            <el-icon size="40" class="step-icon phone-icon"><ChatLineRound /></el-icon>
            <h3 class="step-title">输入手机验证码</h3>
            <p class="step-desc">请输入手机收到的 6 位验证码</p>
            <el-input
              v-model="phoneCode"
              placeholder="请输入手机收到的验证码"
              class="code-input"
              maxlength="6"
              inputmode="numeric"
              clearable
              @keydown.enter="handleVerifyPhone"
            />
            <div class="btn-row">
              <el-button class="back-btn" text @click="currentStep = 0">重新发送</el-button>
              <el-button
                class="action-btn"
                :loading="verifyingPhone"
                :disabled="!isSixDigitVerificationCode(phoneCode)"
                @click="handleVerifyPhone"
              >
                验证
              </el-button>
            </div>
          </div>
        </el-card>

        <!-- Step 2：发送邮箱验证码 -->
        <el-card v-if="currentStep === 2" class="step-card">
          <div class="step-inner">
            <el-icon size="40" class="step-icon email-icon"><Message /></el-icon>
            <h3 class="step-title">验证邮箱</h3>
            <p class="step-desc">
              验证码将发送至您的邮箱：
              <span class="highlight-text">{{ maskedEmail }}</span>
            </p>
            <el-button
              class="action-btn"
              :loading="sendingEmail"
              :disabled="sendingVerificationCode || emailCountdown > 0"
              @click="handleSendEmailCode"
            >
              {{ emailCountdown > 0 ? `重新发送（${emailCountdown}s）` : '发送验证码' }}
            </el-button>
          </div>
        </el-card>

        <!-- Step 3：输入邮箱验证码 -->
        <el-card v-if="currentStep === 3" class="step-card">
          <div class="step-inner">
            <el-icon size="40" class="step-icon email-icon"><EditPen /></el-icon>
            <h3 class="step-title">输入邮箱验证码</h3>
            <p class="step-desc">请输入邮箱收到的 6 位验证码</p>
            <el-input
              v-model="emailCode"
              placeholder="请输入邮箱收到的验证码"
              class="code-input"
              maxlength="6"
              inputmode="numeric"
              clearable
              @keydown.enter="handleVerifyEmail"
            />
            <div class="btn-row">
              <el-button class="back-btn" text @click="currentStep = 2">重新发送</el-button>
              <el-button
                class="action-btn"
                :loading="verifyingEmail"
                :disabled="!isSixDigitVerificationCode(emailCode)"
                @click="handleVerifyEmail"
              >
                验证
              </el-button>
            </div>
          </div>
        </el-card>

        <!-- Step 4：认证成功 -->
        <el-card v-if="currentStep === 4" class="step-card success-card">
          <div class="step-inner">
            <el-icon size="48" class="step-icon success-icon"><CircleCheckFilled /></el-icon>
            <h3 class="step-title success-title">认证成功</h3>
            <p class="step-desc">有效期 24 小时，正在跳转...</p>
          </div>
        </el-card>
      </div>

      <!-- 底部返回链接 -->
      <div class="verify-footer">
        <el-button text class="back-login-btn" @click="router.push('/login')">
          <el-icon><ArrowLeft /></el-icon>
          返回登录
        </el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Lock,
  Iphone,
  ChatLineRound,
  Message,
  EditPen,
  CircleCheckFilled,
  ArrowLeft,
} from '@element-plus/icons-vue'
import { sendAdminVerificationCode, adminVerifyPhone, adminVerifyEmail } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { isSixDigitVerificationCode } from './admin-verification-policy'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

// 当前步骤（0=发手机码, 1=验手机, 2=发邮箱码, 3=验邮箱, 4=成功）
const currentStep = ref(0)

// 手机验证码
const phoneCode = ref('')
const sendingPhone = ref(false)
const verifyingPhone = ref(false)
const phoneCountdown = ref(0)

// 邮箱验证码
const emailCode = ref('')
const sendingEmail = ref(false)
const verifyingEmail = ref(false)
const emailCountdown = ref(0)
// 两个发码端点共享互斥状态，避免快速点击或异步状态切换造成并发发送。
const sendingVerificationCode = computed(() => sendingPhone.value || sendingEmail.value)

// 倒计时定时器
let phoneTimer: ReturnType<typeof setInterval> | null = null
let emailTimer: ReturnType<typeof setInterval> | null = null

// 脱敏手机和邮箱（直接从 currentUser 取，后端返回已脱敏）
const maskedPhone = computed(() => authStore.currentUser?.phone ?? '未绑定')
const maskedEmail = computed(() => authStore.currentUser?.email ?? '未绑定')

onMounted(() => {
  // 已完成双重认证时不重复停留在认证页，直接进入原目标页面。
  if (authStore.adminVerified) {
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.replace(redirect)
    return
  }

  // 如果手机认证仍有效，只要求继续完成邮箱认证。
  if (authStore.currentUser?.admin_phone_verified) {
    currentStep.value = 2
  }
})

/** 启动倒计时（60秒） */
function startPhoneCountdown() {
  phoneCountdown.value = 60
  phoneTimer = setInterval(() => {
    phoneCountdown.value--
    if (phoneCountdown.value <= 0) {
      clearInterval(phoneTimer!)
      phoneTimer = null
    }
  }, 1000)
}

function startEmailCountdown() {
  emailCountdown.value = 60
  emailTimer = setInterval(() => {
    emailCountdown.value--
    if (emailCountdown.value <= 0) {
      clearInterval(emailTimer!)
      emailTimer = null
    }
  }, 1000)
}

/** Step 0：发送手机验证码（D-96：改用 admin 专属发码端点，不传 target/scene）*/
async function handleSendPhoneCode() {
  if (sendingVerificationCode.value) return
  sendingPhone.value = true
  try {
    await sendAdminVerificationCode('phone')
    ElMessage.success('验证码已发送')
    startPhoneCountdown()
    currentStep.value = 1
  } catch (err: unknown) {
    handleApiError(err, 'phone')
  } finally {
    sendingPhone.value = false
  }
}

/** Step 1：验证手机验证码 */
async function handleVerifyPhone() {
  if (!isSixDigitVerificationCode(phoneCode.value)) {
    ElMessage.warning('请输入 6 位数字验证码')
    return
  }
  verifyingPhone.value = true
  try {
    await adminVerifyPhone({ code: phoneCode.value })
    ElMessage.success('手机验证成功')
    await authStore.fetchMe()
    currentStep.value = 2
  } catch (err: unknown) {
    handleApiError(err, 'phone')
  } finally {
    verifyingPhone.value = false
  }
}

/** Step 2：发送邮箱验证码（D-96：改用 admin 专属发码端点，不传 target/scene）*/
async function handleSendEmailCode() {
  if (sendingVerificationCode.value) return
  sendingEmail.value = true
  try {
    await sendAdminVerificationCode('email')
    ElMessage.success('验证码已发送')
    startEmailCountdown()
    currentStep.value = 3
  } catch (err: unknown) {
    handleApiError(err, 'email')
  } finally {
    sendingEmail.value = false
  }
}

/** Step 3：验证邮箱验证码 */
async function handleVerifyEmail() {
  if (!isSixDigitVerificationCode(emailCode.value)) {
    ElMessage.warning('请输入 6 位数字验证码')
    return
  }
  verifyingEmail.value = true
  try {
    await adminVerifyEmail({ code: emailCode.value })
    currentStep.value = 4
    // 刷新用户信息，使 admin_phone_verified / admin_email_verified 更新
    await authStore.fetchMe()
    await authStore.fetchPermissions()
    sessionStorage.removeItem('admin_verify_pending')
    ElMessage.success('认证成功，有效期 24 小时')
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.push(redirect)
  } catch (err: unknown) {
    handleApiError(err, 'email')
  } finally {
    verifyingEmail.value = false
  }
}

/** 统一错误处理 */
function handleApiError(err: unknown, type: 'phone' | 'email') {
  // HTTP 拦截器已经统一显示服务端安全文案，此处只处理页面状态副作用，避免同一错误重复弹窗。
  const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
  if (code === 40003) {
    router.push('/login')
  } else if (code === 42900) {
    // 触发服务端限流后同步启动本地倒计时，避免用户在冷却期内重复点击。
    if (type === 'phone') {
      startPhoneCountdown()
    } else {
      startEmailCountdown()
    }
  }
}

// 组件卸载时清理定时器
onUnmounted(() => {
  if (phoneTimer) clearInterval(phoneTimer)
  if (emailTimer) clearInterval(emailTimer)
})
</script>

<style scoped>
.verify-page {
  min-height: 100vh;
  background: var(--mc-bg-grid);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 16px;
  position: relative;
  overflow: hidden;
}

.verify-page::before {
  content: "";
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(var(--mc-grid-line) 1px, transparent 1px),
    linear-gradient(90deg, var(--mc-grid-line) 1px, transparent 1px),
    linear-gradient(var(--mc-grid-line-strong) 1px, transparent 1px),
    linear-gradient(90deg, var(--mc-grid-line-strong) 1px, transparent 1px);
  background-size: 34px 34px, 34px 34px, 136px 136px, 136px 136px;
  mask-image: radial-gradient(circle at center, rgba(0, 0, 0, 0.92), rgba(0, 0, 0, 0.35));
  pointer-events: none;
}

.verify-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 640px;
  background: rgba(22, 32, 51, 0.94);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 40px 48px;
  backdrop-filter: blur(12px);
}

/* 头部 */
.verify-header {
  text-align: center;
  margin-bottom: 32px;
}

.brand-logo {
  display: flex;
  justify-content: center;
  margin-bottom: 16px;
}

.brand-icon {
  color: var(--mc-primary);
  filter: drop-shadow(0 0 12px rgba(56, 189, 248, 0.28));
}

.verify-title {
  font-size: 22px;
  font-weight: 700;
  color: var(--mc-text);
  margin: 0 0 8px;
}

.verify-subtitle {
  font-size: 14px;
  color: var(--mc-text-muted);
  margin: 0;
  line-height: 1.6;
}

/* 步骤条 */
.verify-steps {
  margin-bottom: 32px;
}

:deep(.el-step__title) {
  font-size: 12px;
  color: var(--mc-text-muted);
}

:deep(.el-step__title.is-finish) {
  color: var(--mc-primary);
}

:deep(.el-step__title.is-process) {
  color: var(--mc-text);
}

:deep(.el-step__head.is-process .el-step__icon) {
  border-color: var(--mc-primary);
  background: var(--mc-primary);
  color: #06111f;
}

:deep(.el-step__head.is-finish .el-step__icon) {
  border-color: var(--mc-primary);
  color: var(--mc-primary);
}

:deep(.el-step__line) {
  background-color: rgba(56, 189, 248, 0.18);
}

/* 步骤卡片 */
.step-card {
  background: var(--mc-surface) !important;
  border: 1px solid var(--mc-border-soft) !important;
  border-radius: var(--mc-radius) !important;
}

:deep(.el-card__body) {
  padding: 32px 24px;
}

.step-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}

.step-icon {
  color: var(--mc-primary);
}

.phone-icon {
  color: var(--mc-accent);
}

.email-icon {
  color: var(--mc-primary);
}

.success-icon {
  color: var(--mc-success);
  filter: drop-shadow(0 0 12px rgba(34, 197, 94, 0.28));
}

.step-title {
  font-size: 17px;
  font-weight: 600;
  color: var(--mc-text);
  margin: 0;
}

.success-title {
  color: var(--mc-success);
}

.step-desc {
  font-size: 14px;
  color: var(--mc-text-muted);
  margin: 0;
  text-align: center;
  line-height: 1.6;
}

.highlight-text {
  color: var(--mc-primary);
  font-weight: 500;
}

/* 输入框 */
.code-input {
  width: 100%;
  max-width: 320px;
}

:deep(.code-input .el-input__wrapper) {
  background: #0f1a2d;
  border: 1px solid var(--mc-border);
  box-shadow: none;
}

:deep(.code-input .el-input__wrapper.is-focus) {
  border-color: var(--mc-primary);
  box-shadow: 0 0 0 1px rgba(56, 189, 248, 0.26);
}

:deep(.code-input .el-input__inner) {
  color: var(--mc-text);
  text-align: center;
  font-size: 18px;
  letter-spacing: 4px;
}

/* 按钮 */
.action-btn {
  background: linear-gradient(135deg, var(--mc-primary-strong), var(--mc-primary));
  border: none;
  color: #06111f;
  padding: 10px 32px;
  font-size: 14px;
  font-weight: 500;
  border-radius: 8px;
  min-width: 160px;
  transition: opacity 0.2s;
}

.action-btn:hover:not(:disabled) {
  opacity: 0.85;
}

.action-btn:disabled {
  opacity: 0.5;
}

.btn-row {
  display: flex;
  align-items: center;
  gap: 16px;
}

.back-btn {
  color: var(--mc-text-muted);
  font-size: 13px;
}

.back-btn:hover {
  color: var(--mc-primary);
}

/* 成功卡片 */
.success-card {
  border-color: rgba(34, 197, 94, 0.24) !important;
}

/* 底部 */
.verify-footer {
  text-align: center;
  margin-top: 24px;
}

.back-login-btn {
  color: var(--mc-text-muted);
  font-size: 13px;
}

.back-login-btn:hover {
  color: var(--mc-primary);
}

/* 移动端适配 */
@media (max-width: 768px) {
  .verify-card {
    padding: 28px 20px;
  }

  :deep(.el-step__title) {
    font-size: 11px;
  }
}
</style>
