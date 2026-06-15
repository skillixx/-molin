<script setup lang="ts">
/**
 * 注册页
 * 统一注册：手机号 + 邮箱必须同时提交，并需双重 OTP 验证码（手机验证码 + 邮箱验证码）
 * 注册成功后自动登录，跳转到商品市场
 */
import { ref, reactive, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { sendEmailCode, sendPhoneCode, register } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

// 统一注册表单
const form = reactive({
  username: '',
  phone: '',
  phoneCode: '',
  email: '',
  emailCode: '',
  password: '',
  confirmPassword: '',
})

// 验证码倒计时
const phoneCountdown = ref(0)
const emailCountdown = ref(0)
let phoneTimer: ReturnType<typeof setInterval> | undefined
let emailTimer: ReturnType<typeof setInterval> | undefined

// 提交状态
const submitting = ref(false)
const sendingPhoneCode = ref(false)
const sendingEmailCode = ref(false)

// 表单 ref
const formRef = ref<FormInstance>()

// =================== 表单校验规则 ===================

// 用户名（可选，但填写则需符合规则）
const usernameValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback()
  if (!/^[a-zA-Z0-9_]{2,32}$/.test(value)) {
    return callback(new Error('用户名为 2-32 位字母/数字/下划线'))
  }
  callback()
}

// 手机号格式
const phoneValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入手机号'))
  if (!/^1[3-9]\d{9}$/.test(value)) return callback(new Error('请输入正确的11位手机号'))
  callback()
}

// 邮箱格式
const emailValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入邮箱地址'))
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) return callback(new Error('邮箱格式不正确'))
  callback()
}

// 验证码格式（6位数字）
const codeValidator = (label: string) => (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error(`请输入${label}`))
  if (!/^\d{6}$/.test(value)) return callback(new Error(`${label}为6位数字`))
  callback()
}

// 密码格式
const passwordValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入密码'))
  if (value.length < 6 || value.length > 72) return callback(new Error('密码长度为 6-72 位'))
  if (!/[a-zA-Z]/.test(value) || !/\d/.test(value)) return callback(new Error('密码须包含字母和数字'))
  callback()
}

// 确认密码
const confirmPasswordValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请再次输入密码'))
  if (value !== form.password) return callback(new Error('两次输入的密码不一致'))
  callback()
}

const rules: FormRules = {
  username: [{ validator: usernameValidator, trigger: 'blur' }],
  phone: [{ validator: phoneValidator, trigger: 'blur' }],
  phoneCode: [{ validator: codeValidator('手机验证码'), trigger: 'blur' }],
  email: [{ validator: emailValidator, trigger: 'blur' }],
  emailCode: [{ validator: codeValidator('邮箱验证码'), trigger: 'blur' }],
  password: [{ validator: passwordValidator, trigger: 'blur' }],
  confirmPassword: [{ validator: confirmPasswordValidator, trigger: 'blur' }],
}

// =================== 发送验证码 ===================

function startCountdown(countdownRef: typeof phoneCountdown, setTimer: (t: ReturnType<typeof setInterval>) => void) {
  countdownRef.value = 60
  const timer = setInterval(() => {
    countdownRef.value--
    if (countdownRef.value <= 0) {
      clearInterval(timer)
    }
  }, 1000)
  setTimer(timer)
}

async function sendPhoneVerifyCode() {
  if (phoneCountdown.value > 0 || sendingPhoneCode.value) return
  if (!form.phone) {
    ElMessage.warning('请先输入手机号')
    return
  }
  if (!/^1[3-9]\d{9}$/.test(form.phone)) {
    ElMessage.warning('请输入正确的11位手机号')
    return
  }
  sendingPhoneCode.value = true
  try {
    await sendPhoneCode(form.phone, 'register')
    ElMessage.success('验证码已发送，请查收短信')
    startCountdown(phoneCountdown, (t) => { phoneTimer = t })
  } finally {
    sendingPhoneCode.value = false
  }
}

async function sendEmailVerifyCode() {
  if (emailCountdown.value > 0 || sendingEmailCode.value) return
  if (!form.email) {
    ElMessage.warning('请先输入邮箱地址')
    return
  }
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email)) {
    ElMessage.warning('邮箱格式不正确')
    return
  }
  sendingEmailCode.value = true
  try {
    await sendEmailCode(form.email, 'register')
    ElMessage.success('验证码已发送，请查收邮件')
    startCountdown(emailCountdown, (t) => { emailTimer = t })
  } finally {
    sendingEmailCode.value = false
  }
}

// =================== 提交注册 ===================

async function handleRegister() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    const tokens = await register({
      username: form.username || undefined,
      phone: form.phone,
      email: form.email,
      password: form.password,
      phone_code: form.phoneCode,
      email_code: form.emailCode,
    })
    await authStore.applyLoginResponse(tokens)
    ElMessage.success('注册成功，欢迎使用墨灵！')
    router.push('/marketplace')
  } finally {
    submitting.value = false
  }
}

// 清理定时器
onUnmounted(() => {
  clearInterval(phoneTimer)
  clearInterval(emailTimer)
})
</script>

<template>
  <div class="auth-page page-bg">
    <div class="register-shell">
      <section class="register-panel">
        <div class="brand-mark">
          <span class="logo-text">墨灵</span>
          <span class="brand-badge">Create Account</span>
        </div>
        <h1 class="brand-title">开通你的云资源账号</h1>
        <p class="brand-desc">完成手机号与邮箱双重验证后，即可进入墨灵用户控制台。</p>

        <div class="step-list">
          <div class="step-item">
            <span class="step-index">01</span>
            <div>
              <strong>填写账号信息</strong>
              <p>设置用户名和登录密码。</p>
            </div>
          </div>
          <div class="step-item">
            <span class="step-index">02</span>
            <div>
              <strong>完成双 OTP 验证</strong>
              <p>手机号和邮箱都需要通过验证码。</p>
            </div>
          </div>
          <div class="step-item">
            <span class="step-index">03</span>
            <div>
              <strong>进入用户控制台</strong>
              <p>注册成功后自动登录并进入商品市场。</p>
            </div>
          </div>
        </div>
      </section>

      <section class="auth-card glass-card">
        <!-- Logo -->
        <div class="auth-logo">
          <span class="auth-kicker">新用户注册</span>
          <h2 class="auth-title">创建墨灵账号</h2>
          <p class="auth-subtitle">爱斯琴网络科技有限公司</p>
        </div>

        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          label-position="top"
          class="auth-form"
        >
          <div class="form-section">
            <div class="form-section-title">账号信息</div>
            <el-form-item label="用户名（选填）" prop="username">
              <el-input
                v-model="form.username"
                placeholder="2-32位字母/数字/下划线"
                autocomplete="username"
                size="large"
              />
            </el-form-item>
            <div class="password-grid">
              <el-form-item label="设置密码" prop="password">
                <el-input
                  v-model="form.password"
                  type="password"
                  placeholder="6-72位，包含字母和数字"
                  show-password
                  autocomplete="new-password"
                  size="large"
                />
              </el-form-item>
              <el-form-item label="确认密码" prop="confirmPassword">
                <el-input
                  v-model="form.confirmPassword"
                  type="password"
                  placeholder="再次输入密码"
                  show-password
                  autocomplete="new-password"
                  size="large"
                />
              </el-form-item>
            </div>
          </div>

          <!-- 手机号区块 -->
          <div class="form-section">
            <div class="form-section-title">手机号验证</div>
            <el-form-item label="手机号" prop="phone">
              <el-input
                v-model="form.phone"
                placeholder="请输入11位手机号"
                maxlength="11"
                autocomplete="tel"
                size="large"
              />
            </el-form-item>

            <el-form-item label="手机验证码" prop="phoneCode">
              <div class="code-row">
                <el-input
                  v-model="form.phoneCode"
                  placeholder="请输入短信验证码"
                  maxlength="6"
                  size="large"
                />
                <button
                  class="code-btn"
                  :disabled="phoneCountdown > 0 || sendingPhoneCode"
                  @click.prevent="sendPhoneVerifyCode"
                >
                  {{ phoneCountdown > 0 ? `${phoneCountdown}s 后重发` : '发送验证码' }}
                </button>
              </div>
            </el-form-item>
          </div>

          <!-- 邮箱区块 -->
          <div class="form-section">
            <div class="form-section-title">邮箱验证</div>
            <el-form-item label="邮箱地址" prop="email">
              <el-input
                v-model="form.email"
                placeholder="user@example.com"
                type="email"
                autocomplete="email"
                size="large"
              />
            </el-form-item>

            <el-form-item label="邮箱验证码" prop="emailCode">
              <div class="code-row">
                <el-input
                  v-model="form.emailCode"
                  placeholder="请输入邮箱验证码"
                  maxlength="6"
                  size="large"
                />
                <button
                  class="code-btn"
                  :disabled="emailCountdown > 0 || sendingEmailCode"
                  @click.prevent="sendEmailVerifyCode"
                >
                  {{ emailCountdown > 0 ? `${emailCountdown}s 后重发` : '发送验证码' }}
                </button>
              </div>
            </el-form-item>
          </div>

          <el-form-item>
            <button
              class="btn-primary auth-submit"
              :disabled="submitting"
              @click.prevent="handleRegister"
            >
              {{ submitting ? '注册中...' : '立即注册' }}
            </button>
          </el-form-item>
        </el-form>

        <!-- 底部跳转 -->
        <p class="auth-footer">
          已有账号？
          <router-link to="/login" class="auth-link">去登录</router-link>
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
    radial-gradient(circle at 12% 18%, rgba(6, 182, 212, 0.2), transparent 25%),
    radial-gradient(circle at 88% 16%, rgba(139, 92, 246, 0.2), transparent 28%),
    linear-gradient(115deg, transparent 0 40%, rgba(99, 102, 241, 0.08) 48%, transparent 58%);
  pointer-events: none;
}

.auth-page::after {
  content: "";
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(99, 102, 241, 0.08) 1px, transparent 1px),
    linear-gradient(90deg, rgba(6, 182, 212, 0.08) 1px, transparent 1px);
  background-size: 42px 42px;
  mask-image: radial-gradient(circle at center, rgba(0, 0, 0, 0.9), rgba(0, 0, 0, 0.25));
  pointer-events: none;
}

.register-shell {
  position: relative;
  z-index: 1;
  width: min(1120px, 100%);
  display: grid;
  grid-template-columns: minmax(0, 0.86fr) minmax(520px, 1fr);
  gap: 24px;
  align-items: stretch;
}

.register-panel {
  min-height: 660px;
  border: 1px solid rgba(99, 102, 241, 0.18);
  border-radius: 20px;
  padding: 42px;
  background:
    linear-gradient(145deg, rgba(99, 102, 241, 0.14), rgba(6, 182, 212, 0.06)),
    rgba(255, 255, 255, 0.035);
  backdrop-filter: blur(18px);
  position: relative;
  overflow: hidden;
}

.register-panel::after {
  content: "";
  position: absolute;
  right: -110px;
  bottom: -140px;
  width: 400px;
  height: 400px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(6, 182, 212, 0.2), transparent 62%);
}

.brand-mark {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 92px;
}

.brand-badge {
  color: var(--color-accent);
  border: 1px solid rgba(6, 182, 212, 0.28);
  background: rgba(6, 182, 212, 0.08);
  border-radius: 999px;
  padding: 5px 10px;
  font-size: 12px;
}

.brand-title {
  max-width: 480px;
  color: var(--color-text);
  font-size: 40px;
  line-height: 1.14;
  font-weight: 800;
  margin-bottom: 18px;
}

.brand-desc {
  max-width: 430px;
  color: var(--color-text-muted);
  font-size: 16px;
  line-height: 1.8;
}

.step-list {
  position: absolute;
  left: 42px;
  right: 42px;
  bottom: 42px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  z-index: 1;
}

.step-item {
  display: grid;
  grid-template-columns: 48px 1fr;
  gap: 14px;
  padding: 16px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 14px;
  background: rgba(10, 15, 30, 0.46);
}

.step-index {
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 800;
}

.step-item strong {
  color: var(--color-text);
  font-size: 15px;
}

.step-item p {
  color: var(--color-text-muted);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 4px;
}

.auth-card {
  width: 100%;
  padding: 34px;
  border-radius: 20px;
  background: rgba(11, 16, 32, 0.76);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.36);
}

.auth-logo {
  margin-bottom: 22px;
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
  font-size: 13px;
}

.auth-form {
  margin-top: 4px;
}

/* 区块容器：手机号区块 / 邮箱区块 */
.form-section {
  border: 1px solid var(--color-border);
  border-radius: 14px;
  padding: 16px 16px 2px;
  margin-bottom: 14px;
  background: rgba(99, 102, 241, 0.055);
}

.form-section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-primary);
  margin-bottom: 12px;
}

.password-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
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
  min-width: 112px;
  padding: 0 14px;
  height: 40px;
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

.auth-footer {
  text-align: center;
  color: var(--color-text-muted);
  font-size: 13px;
  margin-top: 16px;
}

.auth-link {
  color: var(--color-primary);
  text-decoration: none;
  transition: color 0.2s;
}

.auth-link:hover {
  color: var(--color-primary-end);
}

.auth-submit {
  height: 46px;
  margin-top: 4px;
}

:deep(.el-input__wrapper) {
  min-height: 42px;
  border-radius: 9px;
}

@media (max-width: 980px) {
  .register-shell {
    grid-template-columns: 1fr;
    max-width: 560px;
  }

  .register-panel {
    min-height: auto;
    padding: 28px;
  }

  .brand-mark {
    margin-bottom: 34px;
  }

  .brand-title {
    font-size: 30px;
  }

  .step-list {
    position: relative;
    left: auto;
    right: auto;
    bottom: auto;
    margin-top: 28px;
  }
}

@media (max-width: 560px) {
  .auth-page {
    padding: 20px 14px;
  }

  .register-panel {
    display: none;
  }

  .auth-card {
    padding: 28px 20px;
  }

  .password-grid {
    grid-template-columns: 1fr;
    gap: 0;
  }

  .code-row {
    flex-direction: column;
  }

  .code-btn {
    width: 100%;
  }
}
</style>
