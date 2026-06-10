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

const router = useRouter()

// 当前步骤：1 = 填写目标并发送验证码，2 = 填写验证码与新密码
const currentStep = ref(0)

// 重置方式：手机号 / 邮箱
const targetType = ref<'phone' | 'email'>('phone')

// 第一步：目标输入
const targetValue = ref('')

// 第二步：验证码 + 新密码
const step2Form = reactive({
  code: '',
  new_password: '',
  confirm_password: '',
})

// 加载状态
const sendingCode = ref(false)
const submitting = ref(false)

// 60s 倒计时
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval>

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
    { len: 6, message: '验证码为6位数字', trigger: 'blur' },
  ],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, message: '密码至少8位', trigger: 'blur' },
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

// =================== 步骤一：发送验证码 ===================

async function sendCode() {
  const valid = await step1FormRef.value?.validate().catch(() => false)
  if (!valid) return

  sendingCode.value = true
  try {
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
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 42900) {
      ElMessage.error('发送频率超限，请稍后再试')
    } else if (code === 40404) {
      ElMessage.error('该手机号/邮箱未注册')
    } else {
      ElMessage.error('发送失败，请稍后重试')
    }
  } finally {
    sendingCode.value = false
  }
}

// 重新发送验证码（在第二步）
async function resendCode() {
  if (countdown.value > 0 || sendingCode.value) return
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
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 42900) {
      ElMessage.error('发送频率超限，请稍后再试')
    } else {
      ElMessage.error('发送失败，请稍后重试')
    }
  } finally {
    sendingCode.value = false
  }
}

// =================== 步骤二：提交重置 ===================

async function handleReset() {
  const valid = await step2FormRef.value?.validate().catch(() => false)
  if (!valid) return

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
    if (code === 40000) {
      ElMessage.error('验证码错误或已过期')
    } else if (code === 40404) {
      ElMessage.error('该手机号/邮箱未注册')
    } else if (code === 42900) {
      ElMessage.error('发送频率超限，请稍后再试')
    }
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
    <div class="auth-card glass-card">
      <!-- Logo -->
      <div class="auth-logo">
        <span class="logo-text">墨灵</span>
        <p class="auth-subtitle">重置密码</p>
      </div>

      <!-- el-steps 两步进度 -->
      <el-steps :active="currentStep" finish-status="success" class="reset-steps" align-center>
        <el-step title="验证身份" />
        <el-step title="设置新密码" />
      </el-steps>

      <!-- 第一步：选择方式 + 发送验证码 -->
      <div v-if="currentStep === 0" class="step-content">
        <!-- 重置方式切换 -->
        <div class="method-tabs">
          <button
            class="method-tab"
            :class="{ active: targetType === 'phone' }"
            @click="targetType = 'phone'; targetValue = ''"
          >
            手机号重置
          </button>
          <button
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
            />
          </el-form-item>

          <el-form-item>
            <button
              class="btn-primary"
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
          <span class="hint-target">{{ targetValue }}</span>
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
              />
              <button
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
              placeholder="至少8位"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>

          <el-form-item label="确认新密码" prop="confirm_password">
            <el-input
              v-model="step2Form.confirm_password"
              type="password"
              placeholder="再次输入新密码"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>

          <el-form-item>
            <button
              class="btn-primary"
              :disabled="submitting"
              @click.prevent="handleReset"
            >
              {{ submitting ? '提交中...' : '确认重置密码' }}
            </button>
          </el-form-item>
        </el-form>

        <!-- 返回上一步 -->
        <p class="back-link" @click="goBack">← 重新选择验证方式</p>
      </div>

      <!-- 底部跳转 -->
      <p class="auth-footer">
        想起密码了？
        <router-link to="/login" class="auth-link">返回登录 →</router-link>
      </p>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.auth-card {
  width: 100%;
  max-width: 480px;
  padding: 40px;
}

/* 移动端全宽 */
@media (max-width: 767px) {
  .auth-card {
    padding: 28px 20px;
  }
}

.auth-logo {
  text-align: center;
  margin-bottom: 24px;
}

.auth-subtitle {
  color: var(--color-text-muted);
  font-size: 14px;
  margin-top: 8px;
}

/* 步骤条 */
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
  border-radius: 8px;
  padding: 4px;
}

.method-tab {
  flex: 1;
  height: 36px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 14px;
  cursor: pointer;
  transition: all 0.2s;
}

.method-tab.active {
  background: rgba(99, 102, 241, 0.15);
  color: var(--color-primary);
  font-weight: 500;
}

.method-tab:hover:not(.active) {
  background: rgba(255, 255, 255, 0.05);
  color: var(--color-text);
}

/* 表单 */
.auth-form {
  margin-top: 4px;
}

/* 第二步提示文字 */
.step2-hint {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-bottom: 20px;
  text-align: center;
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
  height: 32px;
  background: rgba(99, 102, 241, 0.12);
  border: 1px solid var(--color-border);
  color: var(--color-primary);
  border-radius: 6px;
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

/* 返回链接 */
.back-link {
  text-align: center;
  color: var(--color-text-muted);
  font-size: 13px;
  margin-top: 8px;
  cursor: pointer;
  transition: color 0.2s;
}

.back-link:hover {
  color: var(--color-primary);
}

/* 底部 */
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
</style>
