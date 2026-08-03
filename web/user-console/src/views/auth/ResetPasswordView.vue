<script setup lang="ts">
/**
 * OTP 密码重置页（无需登录）
 * 两步流程：
 *   Step 1 — 选择重置方式（手机/邮箱），填写目标，发送验证码（scene=reset_password），60s 倒计时
 *   Step 2 — 填写验证码 + 新密码 + 确认密码，提交 resetPassword()
 * 成功后跳转 /login
 */
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { sendPhoneCode, sendEmailCode, resetPassword } from '@/api/auth'
import { maskPhone } from '@/utils/privacy'
import { getSmsSendErrorMessage } from '@/utils/sms'

const router = useRouter()

// 当前步骤：1 = 填写目标并发送验证码，2 = 填写验证码与新密码
const currentStep = ref(0)

// 重置方式：手机号 / 邮箱
const targetType = ref<'phone' | 'email'>('phone')

// 第一步：目标输入
const targetValue = ref('')

// 手机号在发送成功后的确认区域只显示脱敏值，邮箱保持原有展示。
const maskedTargetValue = computed(() => (
  targetType.value === 'phone' ? maskPhone(targetValue.value) : targetValue.value
))

// 第二步：验证码 + 新密码
const step2Form = reactive({
  code: '',
  new_password: '',
  confirm_password: '',
})

// 加载状态
const sendingCode = ref(false)
const submitting = ref(false)
const formError = ref('')

// 60s 倒计时
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval>

// 将后端可公开的错误消息保留在表单内，避免 Toast 消失后用户失去上下文。
function getErrorMessage(error: unknown, fallback: string) {
  return (error as { response?: { data?: { message?: string } } })?.response?.data?.message || fallback
}

// 表单 ref
const step1FormRef = ref<FormInstance>()
const step2FormRef = ref<FormInstance>()

// =================== 校验规则 ===================

const phoneRule = /^1[3-9]\d{9}$/
const emailRule = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

const step1Rules = computed<FormRules>(() => ({
  target: [
    { required: true, message: '请输入' + (targetType.value === 'phone' ? '手机号' : '邮箱地址'), trigger: 'blur' },
    {
      validator: (_rule: unknown, val: string, cb: (err?: Error) => void) => {
        if (targetType.value === 'phone' && !phoneRule.test(val)) {
          cb(new Error('请输入正确的11位手机号'))
        } else if (targetType.value === 'email' && !emailRule.test(val)) {
          cb(new Error('邮箱格式不正确'))
        } else {
          cb()
        }
      },
      trigger: 'blur',
    },
  ],
}))

const step2Rules: FormRules = {
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { pattern: /^\d{6}$/, message: '验证码为6位数字', trigger: 'blur' },
  ],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, max: 72, message: '密码长度须为6-72位', trigger: 'blur' },
  ],
  confirm_password: [
    { required: true, message: '请确认新密码', trigger: 'blur' },
    {
      validator: (_rule: unknown, val: string, cb: (err?: Error) => void) => {
        if (val !== step2Form.new_password) {
          cb(new Error('两次密码不一致'))
        } else {
          cb()
        }
      },
      trigger: 'blur',
    },
  ],
}

// 首次发送和重新发送共用错误提示，避免两个流程后续出现行为差异。
function showSendCodeError(error: unknown) {
  if (targetType.value === 'phone') {
    formError.value = getSmsSendErrorMessage(error)
  } else {
    const code = (error as { response?: { data?: { code?: number } } })?.response?.data?.code
    const fallback = code === 42900
      ? '发送频率超限，请稍后再试'
      : code === 40404 || code === 40400 ? '该邮箱未注册' : '发送失败，请稍后重试'
    formError.value = getErrorMessage(error, fallback)
  }
  ElMessage.error(formError.value)
}

// =================== 步骤一：发送验证码 ===================

async function sendCode() {
  // 表单校验期间也要阻止重复点击，确保一次用户操作只产生一次发码请求。
  if (sendingCode.value) return
  sendingCode.value = true
  try {
    const valid = await step1FormRef.value?.validate().catch(() => false)
    if (!valid) return

    formError.value = ''
    if (targetType.value === 'phone') {
      await sendPhoneCode(targetValue.value, 'reset_password')
    } else {
      await sendEmailCode(targetValue.value, 'reset_password')
    }
    ElMessage.success('验证码已发送，请查收')
    // 进入第二步
    currentStep.value = 1
    // 启动60s倒计时
    countdown.value = 60
    countdownTimer = setInterval(() => {
      countdown.value--
      if (countdown.value <= 0) clearInterval(countdownTimer)
    }, 1000)
  } catch (err: unknown) {
    showSendCodeError(err)
  } finally {
    sendingCode.value = false
  }
}

// 重新发送验证码（在第二步）
async function resendCode() {
  if (countdown.value > 0 || sendingCode.value) return
  formError.value = ''
  sendingCode.value = true
  try {
    if (targetType.value === 'phone') {
      await sendPhoneCode(targetValue.value, 'reset_password')
    } else {
      await sendEmailCode(targetValue.value, 'reset_password')
    }
    ElMessage.success('验证码已重新发送')
    countdown.value = 60
    countdownTimer = setInterval(() => {
      countdown.value--
      if (countdown.value <= 0) clearInterval(countdownTimer)
    }, 1000)
  } catch (err: unknown) {
    showSendCodeError(err)
  } finally {
    sendingCode.value = false
  }
}

// =================== 步骤二：提交重置 ===================

async function handleReset() {
  const valid = await step2FormRef.value?.validate().catch(() => false)
  if (!valid) return

  formError.value = ''
  submitting.value = true
  try {
    await resetPassword({
      target: targetValue.value,
      target_type: targetType.value,
      code: step2Form.code,
      new_password: step2Form.new_password,
    })
    ElMessage.success('密码重置成功，请重新登录')
    router.push('/login')
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    const fallback = code === 40000
      ? '验证码错误或已过期'
      : code === 40404
        ? '该手机号/邮箱未注册'
        : code === 42900 ? '发送频率超限，请稍后再试' : '密码重置失败，请稍后重试'
    formError.value = getErrorMessage(err, fallback)
    ElMessage.error(formError.value)
  } finally {
    submitting.value = false
  }
}

// 返回第一步
function goBack() {
  currentStep.value = 0
  step2Form.code = ''
  step2Form.new_password = ''
  step2Form.confirm_password = ''
  clearInterval(countdownTimer)
  countdown.value = 0
}

onUnmounted(() => clearInterval(countdownTimer))
</script>

<template>
  <div class="auth-page page-bg">
    <div class="reset-shell">
      <section class="reset-panel">
        <div class="brand-mark">
          <span class="logo-text">墨灵</span>
          <span class="brand-badge">Password Recovery</span>
        </div>
        <h1 class="brand-title">找回你的账号访问权限</h1>
        <p class="brand-desc">通过注册手机号或邮箱完成一次性验证码校验，然后设置新的登录密码。</p>

        <div class="recovery-flow">
          <div class="flow-item" :class="{ active: currentStep === 0 }">
            <span class="flow-index">01</span>
            <div>
              <strong>验证账号归属</strong>
              <p>选择手机号或邮箱并获取验证码。</p>
            </div>
          </div>
          <div class="flow-item" :class="{ active: currentStep === 1 }">
            <span class="flow-index">02</span>
            <div>
              <strong>设置新密码</strong>
              <p>提交验证码并更新登录密码。</p>
            </div>
          </div>
        </div>
      </section>

      <section class="auth-card glass-card">
        <div class="auth-logo">
          <span class="auth-kicker">账号恢复</span>
          <h2 class="auth-title">重置登录密码</h2>
          <p class="auth-subtitle">验证码有效期较短，请在收到后及时完成操作。</p>
        </div>

        <!-- el-steps 两步进度 -->
        <el-steps :active="currentStep" finish-status="success" class="reset-steps" align-center>
          <el-step title="验证身份" />
          <el-step title="设置新密码" />
        </el-steps>

        <p v-if="formError" class="form-error" role="alert">
          {{ formError }}
        </p>

        <!-- 第一步：选择方式 + 发送验证码 -->
        <div v-if="currentStep === 0" class="step-content">
          <div class="method-tabs">
            <button
              type="button"
              class="method-tab"
              :class="{ active: targetType === 'phone' }"
              @click="targetType = 'phone'; targetValue = ''"
            >
              手机号重置
            </button>
            <button
              type="button"
              class="method-tab"
              :class="{ active: targetType === 'email' }"
              @click="targetType = 'email'; targetValue = ''"
            >
              邮箱重置
            </button>
          </div>

          <el-form
            ref="step1FormRef"
            :model="{ target: targetValue }"
            :rules="step1Rules"
            label-position="top"
            class="auth-form"
          >
            <el-form-item
              :label="targetType === 'phone' ? '注册手机号' : '注册邮箱'"
              prop="target"
            >
              <el-input
                v-model="targetValue"
                :placeholder="targetType === 'phone' ? '请输入注册时使用的手机号' : '请输入注册时使用的邮箱'"
                :type="targetType === 'email' ? 'email' : 'text'"
                :maxlength="targetType === 'phone' ? 11 : undefined"
                autocomplete="off"
                size="large"
              />
            </el-form-item>

            <el-form-item>
              <button
                type="button"
                class="btn-primary auth-submit"
                :disabled="sendingCode"
                @click.prevent="sendCode"
              >
                {{ sendingCode ? '发送中...' : '发送验证码' }}
              </button>
            </el-form-item>
          </el-form>
        </div>

        <!-- 第二步：填写验证码 + 新密码 -->
        <div v-else class="step-content">
          <p class="step2-hint">
            验证码已发送至
            <span class="hint-target">{{ maskedTargetValue }}</span>
          </p>

          <el-form
            ref="step2FormRef"
            :model="step2Form"
            :rules="step2Rules"
            label-position="top"
            class="auth-form"
          >
            <el-form-item label="验证码" prop="code">
              <div class="code-row">
                <el-input
                  v-model="step2Form.code"
                  placeholder="请输入6位验证码"
                  maxlength="6"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  size="large"
                />
                <button
                  type="button"
                  class="code-btn"
                  :disabled="countdown > 0 || sendingCode"
                  @click.prevent="resendCode"
                >
                  {{ countdown > 0 ? `${countdown}s 后重发` : '重新发送' }}
                </button>
              </div>
            </el-form-item>

            <el-form-item label="新密码" prop="new_password">
              <el-input
                v-model="step2Form.new_password"
                type="password"
                placeholder="6-72位"
                maxlength="72"
                show-password
                autocomplete="new-password"
                size="large"
              />
            </el-form-item>

            <el-form-item label="确认新密码" prop="confirm_password">
              <el-input
                v-model="step2Form.confirm_password"
                type="password"
                placeholder="再次输入新密码"
                maxlength="72"
                show-password
                autocomplete="new-password"
                size="large"
              />
            </el-form-item>

            <el-form-item>
              <button
                type="button"
                class="btn-primary auth-submit"
                :disabled="submitting"
                @click.prevent="handleReset"
              >
                {{ submitting ? '提交中...' : '确认重置密码' }}
              </button>
            </el-form-item>
          </el-form>

          <button class="back-link" type="button" @click="goBack">
            重新选择验证方式
          </button>
        </div>

        <p class="auth-footer">
          想起密码了？
          <router-link to="/login" class="auth-link">返回登录</router-link>
        </p>
      </section>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 24px;
  position: relative;
  overflow: hidden;
}

.auth-page::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.13), transparent 34%),
    linear-gradient(225deg, rgba(251, 191, 36, 0.1), transparent 32%),
    linear-gradient(115deg, transparent 0 40%, rgba(52, 211, 153, 0.08) 48%, transparent 58%);
  pointer-events: none;
}

.auth-page::after {
  content: "";
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(148, 163, 184, 0.08) 1px, transparent 1px),
    linear-gradient(90deg, rgba(34, 211, 238, 0.07) 1px, transparent 1px);
  background-size: 42px 42px;
  mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.88), rgba(0, 0, 0, 0.28));
  pointer-events: none;
}

.reset-shell {
  position: relative;
  z-index: 1;
  width: min(1040px, 100%);
  display: grid;
  grid-template-columns: minmax(0, 1fr) 430px;
  gap: 24px;
  align-items: stretch;
}

.reset-panel {
  min-height: 560px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 42px;
  background:
    linear-gradient(145deg, rgba(34, 211, 238, 0.12), rgba(52, 211, 153, 0.06)),
    rgba(255, 255, 255, 0.035);
  backdrop-filter: blur(18px);
  position: relative;
  overflow: hidden;
}

.brand-mark {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 88px;
}

.brand-badge {
  color: var(--color-accent);
  border: 1px solid rgba(52, 211, 153, 0.28);
  background: rgba(52, 211, 153, 0.08);
  border-radius: 999px;
  padding: 5px 10px;
  font-size: 12px;
}

.brand-title {
  max-width: 520px;
  color: var(--color-text);
  font-size: 42px;
  line-height: 1.12;
  font-weight: 800;
  margin-bottom: 18px;
}

.brand-desc {
  max-width: 460px;
  color: var(--color-text-muted);
  font-size: 16px;
  line-height: 1.8;
}

.recovery-flow {
  position: absolute;
  left: 42px;
  right: 42px;
  bottom: 42px;
  display: grid;
  gap: 12px;
  z-index: 1;
}

.flow-item {
  display: grid;
  grid-template-columns: 48px 1fr;
  gap: 14px;
  padding: 16px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 8px;
  background: rgba(10, 15, 30, 0.46);
}

.flow-item.active {
  border-color: rgba(52, 211, 153, 0.34);
  background: rgba(52, 211, 153, 0.09);
}

.flow-index {
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 800;
}

.flow-item strong {
  color: var(--color-text);
  font-size: 15px;
}

.flow-item p {
  color: var(--color-text-muted);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 4px;
}

.auth-card {
  width: 100%;
  padding: 36px;
  border-radius: 8px;
  background: rgba(10, 16, 26, 0.78);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.36);
}

.auth-logo {
  margin-bottom: 24px;
}

.auth-kicker {
  display: inline-flex;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 10px;
}

.auth-title {
  color: var(--color-text);
  font-size: 28px;
  line-height: 1.2;
  margin-bottom: 8px;
}

.auth-subtitle {
  color: var(--color-text-muted);
  font-size: 14px;
}

.reset-steps {
  margin-bottom: 28px;
}

:deep(.el-step__title) {
  color: var(--color-text-muted) !important;
  font-size: 13px !important;
}

:deep(.el-step__title.is-process),
:deep(.el-step__title.is-finish) {
  color: var(--color-primary) !important;
}

:deep(.el-step__head.is-finish .el-step__line) {
  border-color: var(--color-primary) !important;
}

:deep(.el-step__icon) {
  background: rgba(11, 16, 32, 0.92) !important;
}

.step-content {
  animation: fadeIn 0.25s ease;
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(8px); }
  to   { opacity: 1; transform: translateY(0); }
}

/* 方式切换 tabs */
.method-tabs {
  display: flex;
  gap: 8px;
  margin-bottom: 20px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  padding: 4px;
}

.method-tab {
  flex: 1;
  min-height: 44px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 14px;
  cursor: pointer;
  transition: all 0.2s;
}

.method-tab.active {
  background: var(--gradient-primary);
  color: #fff;
  font-weight: 600;
}

.method-tab:hover:not(.active) {
  background: rgba(255, 255, 255, 0.05);
  color: var(--color-text);
}

/* 表单 */
.auth-form {
  margin-top: 4px;
}

.form-error {
  margin: 0 0 14px;
  padding: 10px 12px;
  border: 1px solid rgba(248, 113, 113, 0.36);
  border-radius: 8px;
  color: #fecaca;
  background: rgba(127, 29, 29, 0.18);
  font-size: 13px;
  line-height: 1.5;
}

.auth-submit {
  height: 46px;
  margin-top: 4px;
}

.step2-hint {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-bottom: 20px;
  padding: 12px 14px;
  border: 1px solid rgba(6, 182, 212, 0.18);
  border-radius: 12px;
  background: rgba(6, 182, 212, 0.06);
}

.hint-target {
  color: var(--color-accent);
  font-weight: 500;
}

/* 验证码行 */
.code-row {
  display: flex;
  gap: 10px;
  width: 100%;
}

.code-row .el-input {
  flex: 1;
}

.code-btn {
  flex-shrink: 0;
  padding: 0 14px;
  min-height: 44px;
  background: rgba(99, 102, 241, 0.12);
  border: 1px solid var(--color-border);
  color: var(--color-primary);
  border-radius: 8px;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.2s, border-color 0.2s;
}

.code-btn:hover:not(:disabled) {
  background: rgba(99, 102, 241, 0.2);
  border-color: var(--color-primary);
}

.code-btn:disabled {
  color: var(--color-text-disabled);
  border-color: rgba(99, 102, 241, 0.1);
  cursor: not-allowed;
}

.back-link {
  display: block;
  width: 100%;
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 13px;
  margin-top: 8px;
  cursor: pointer;
  transition: color 0.2s;
}

.back-link:hover {
  color: var(--color-primary);
}

.auth-footer {
  text-align: center;
  color: var(--color-text-muted);
  font-size: 13px;
  margin-top: 20px;
}

.auth-link {
  color: var(--color-primary);
  text-decoration: none;
  transition: color 0.2s;
}

.auth-link:hover {
  color: var(--color-primary-end);
}

:deep(.el-input__wrapper) {
  min-height: 44px;
  border-radius: 9px;
}

@media (max-width: 900px) {
  .reset-shell {
    grid-template-columns: 1fr;
    max-width: 460px;
  }

  .reset-panel {
    min-height: auto;
    padding: 28px;
  }

  .brand-mark {
    margin-bottom: 36px;
  }

  .brand-title {
    font-size: 30px;
  }

  .recovery-flow {
    position: relative;
    left: auto;
    right: auto;
    bottom: auto;
    margin-top: 28px;
  }
}

@media (max-width: 520px) {
  .auth-page {
    padding: 20px 14px;
  }

  .reset-panel {
    display: none;
  }

  .auth-card {
    padding: 28px 20px;
  }

  .code-row {
    flex-direction: column;
  }

  .code-btn {
    width: 100%;
  }
}
</style>
