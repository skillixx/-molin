<script setup lang="ts">
/**
 * 登录页
 * 支持邮箱密码登录和手机号验证码登录
 * 登录成功后跳转到商品市场
 */
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { sendPhoneCode } from '@/api/auth'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

// 当前 Tab
const activeTab = ref<'email' | 'phone'>('email')

// 邮箱登录表单
const emailForm = reactive({ email: '', password: '', captcha: '' })

// 手机号验证码登录表单
const phoneForm = reactive({ phone: '', code: '', captcha: '' })

// 前端本地计算校验码，后端接入服务端验证码前用于降低重复误触和低成本频繁请求
const captchaText = ref('')
const captchaAnswer = ref(0)

// 提交状态
const submitting = ref(false)

// 发送验证码状态
const sendingCode = ref(false)

// 60s 倒计时
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval>

// 表单 ref
const emailFormRef = ref<FormInstance>()
const phoneFormRef = ref<FormInstance>()

// =================== 校验规则 ===================

function createCaptcha() {
  const left = Math.floor(Math.random() * 9) + 1
  const right = Math.floor(Math.random() * 9) + 1
  const operators = ['+', '-', '×'] as const
  const operator = operators[Math.floor(Math.random() * operators.length)]

  if (operator === '+') {
    captchaText.value = `${left} + ${right} = ?`
    captchaAnswer.value = left + right
  } else if (operator === '-') {
    const max = Math.max(left, right)
    const min = Math.min(left, right)
    captchaText.value = `${max} - ${min} = ?`
    captchaAnswer.value = max - min
  } else {
    captchaText.value = `${left} × ${right} = ?`
    captchaAnswer.value = left * right
  }
}

function refreshCaptcha() {
  createCaptcha()
  emailForm.captcha = ''
  phoneForm.captcha = ''
  emailFormRef.value?.clearValidate('captcha')
  phoneFormRef.value?.clearValidate('captcha')
}

const captchaValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入计算结果'))
  if (!/^\d+$/.test(value.trim())) return callback(new Error('计算结果只能填写数字'))
  if (Number(value.trim()) !== captchaAnswer.value) {
    return callback(new Error('计算结果不正确'))
  }
  callback()
}

const emailRules: FormRules = {
  email: [
    { required: true, message: '请输入邮箱地址', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
  captcha: [{ validator: captchaValidator, trigger: 'blur' }],
}

const phoneRules: FormRules = {
  phone: [
    { required: true, message: '请输入手机号', trigger: 'blur' },
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的11位手机号', trigger: 'blur' },
  ],
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { len: 6, message: '验证码为6位数字', trigger: 'blur' },
  ],
  captcha: [{ validator: captchaValidator, trigger: 'blur' }],
}

// =================== 登录 ===================

async function handleEmailLogin() {
  const valid = await emailFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    await authStore.loginWithEmail(emailForm.email, emailForm.password)
    ElMessage.success('登录成功')
    router.push(getRedirectPath())
  } catch {
    refreshCaptcha()
  } finally {
    submitting.value = false
  }
}

// 发送手机验证码（登录场景），60s 倒计时
async function sendLoginCode() {
  if (countdown.value > 0 || sendingCode.value) return

  // 发送前单独校验手机号格式
  const valid = await phoneFormRef.value
    ?.validateField(['phone', 'captcha'])
    .then(() => true)
    .catch(() => false)
  if (!valid) return

  sendingCode.value = true
  try {
    await sendPhoneCode(phoneForm.phone, 'login')
    ElMessage.success('验证码已发送，请查收')
    countdown.value = 60
    countdownTimer = setInterval(() => {
      countdown.value--
      if (countdown.value <= 0) clearInterval(countdownTimer)
    }, 1000)
  } catch {
    refreshCaptcha()
  } finally {
    sendingCode.value = false
  }
}

async function handlePhoneLogin() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    await authStore.loginWithPhone(phoneForm.phone, phoneForm.code)
    ElMessage.success('登录成功')
    router.push(getRedirectPath())
  } catch {
    refreshCaptcha()
  } finally {
    submitting.value = false
  }
}

onMounted(createCaptcha)
onUnmounted(() => clearInterval(countdownTimer))

function getRedirectPath() {
  const redirect = route.query.redirect
  return typeof redirect === 'string' && redirect.startsWith('/') ? redirect : '/marketplace'
}
</script>

<template>
  <div class="auth-page page-bg">
    <div class="auth-shell">
      <section class="brand-panel">
        <div class="brand-mark">
          <span class="logo-text">墨灵</span>
          <span class="brand-badge">用户控制台</span>
        </div>
        <h1 class="brand-title">云资源与 AI 应用控制台</h1>
        <p class="brand-desc">统一管理你的应用、资产、钱包和会员权益。</p>

        <div class="signal-board">
          <div class="signal-card signal-card--accent">
            <span class="signal-label">实名认证</span>
            <strong>安全准入</strong>
          </div>
          <div class="signal-card">
            <span class="signal-label">资产权益</span>
            <strong>实时同步</strong>
          </div>
          <div class="signal-card">
            <span class="signal-label">商品市场</span>
            <strong>即开即用</strong>
          </div>
        </div>
      </section>

      <section class="auth-card glass-card">
        <!-- Logo -->
        <div class="auth-logo">
          <span class="auth-kicker">欢迎回来</span>
          <h2 class="auth-title">登录墨灵账号</h2>
          <p class="auth-subtitle">继续访问你的用户控制台</p>
        </div>

        <!-- Tab 切换 -->
        <el-tabs v-model="activeTab" class="auth-tabs">
          <!-- 邮箱密码登录 -->
          <el-tab-pane label="邮箱登录" name="email">
            <el-form
              ref="emailFormRef"
              :model="emailForm"
              :rules="emailRules"
              label-position="top"
              class="auth-form"
              @keyup.enter="handleEmailLogin"
            >
              <el-form-item label="邮箱地址" prop="email">
                <el-input
                  v-model="emailForm.email"
                  placeholder="user@example.com"
                  type="email"
                  autocomplete="email"
                  size="large"
                />
              </el-form-item>

              <el-form-item label="密码" prop="password">
                <el-input
                  v-model="emailForm.password"
                  type="password"
                  placeholder="请输入密码"
                  show-password
                  autocomplete="current-password"
                  size="large"
                />
              </el-form-item>

              <el-form-item label="计算校验码" prop="captcha">
                <div class="captcha-row">
                  <el-input
                    v-model="emailForm.captcha"
                    placeholder="请输入计算结果"
                    maxlength="2"
                    autocomplete="off"
                    size="large"
                  />
                  <button
                    class="captcha-code"
                    type="button"
                    title="点击刷新计算题"
                    @click="refreshCaptcha"
                  >
                    {{ captchaText }}
                  </button>
                </div>
              </el-form-item>

              <el-form-item>
                <button
                  class="btn-primary auth-submit"
                  :disabled="submitting"
                  @click.prevent="handleEmailLogin"
                >
                  {{ submitting ? '登录中...' : '登录' }}
                </button>
              </el-form-item>
            </el-form>
          </el-tab-pane>

          <!-- 手机号验证码登录 -->
          <el-tab-pane label="手机号登录" name="phone">
            <el-form
              ref="phoneFormRef"
              :model="phoneForm"
              :rules="phoneRules"
              label-position="top"
              class="auth-form"
              @keyup.enter="handlePhoneLogin"
            >
              <el-form-item label="手机号" prop="phone">
                <el-input
                  v-model="phoneForm.phone"
                  placeholder="138xxxxxxxx"
                  maxlength="11"
                  autocomplete="tel"
                  size="large"
                />
              </el-form-item>

              <el-form-item label="验证码" prop="code">
                <div class="code-row">
                  <el-input
                    v-model="phoneForm.code"
                    placeholder="请输入6位验证码"
                    maxlength="6"
                    autocomplete="one-time-code"
                    size="large"
                  />
                  <button
                    class="code-btn"
                    :disabled="countdown > 0 || sendingCode"
                    @click.prevent="sendLoginCode"
                  >
                    {{ countdown > 0 ? `${countdown}s 后重发` : (sendingCode ? '发送中...' : '发送验证码') }}
                  </button>
                </div>
              </el-form-item>

              <el-form-item label="计算校验码" prop="captcha">
                <div class="captcha-row">
                  <el-input
                    v-model="phoneForm.captcha"
                    placeholder="发送短信前先输入结果"
                    maxlength="2"
                    autocomplete="off"
                    size="large"
                  />
                  <button
                    class="captcha-code"
                    type="button"
                    title="点击刷新计算题"
                    @click="refreshCaptcha"
                  >
                    {{ captchaText }}
                  </button>
                </div>
              </el-form-item>

              <el-form-item>
                <button
                  class="btn-primary auth-submit"
                  :disabled="submitting"
                  @click.prevent="handlePhoneLogin"
                >
                  {{ submitting ? '登录中...' : '登录' }}
                </button>
              </el-form-item>
            </el-form>
          </el-tab-pane>
        </el-tabs>

        <!-- 底部跳转 -->
        <div class="auth-footer-row">
          <p class="auth-footer">
            没有账号？
            <router-link to="/register" class="auth-link">立即注册</router-link>
          </p>
          <p class="auth-footer">
            忘记密码？
            <router-link to="/reset-password" class="auth-link">立即重置</router-link>
          </p>
        </div>
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
    linear-gradient(115deg, transparent 0 42%, rgba(52, 211, 153, 0.08) 48%, transparent 56%);
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
  mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.88), rgba(0, 0, 0, 0.28));
  pointer-events: none;
}

.auth-shell {
  position: relative;
  z-index: 1;
  width: min(1040px, 100%);
  display: grid;
  grid-template-columns: minmax(0, 1fr) 430px;
  gap: 24px;
  align-items: stretch;
}

.brand-panel {
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

.signal-board {
  position: absolute;
  left: 42px;
  right: 42px;
  bottom: 42px;
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  z-index: 1;
}

.signal-card {
  min-height: 92px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 8px;
  padding: 14px;
  background: rgba(10, 15, 30, 0.46);
}

.signal-card--accent {
  border-color: rgba(52, 211, 153, 0.34);
  background: rgba(52, 211, 153, 0.09);
}

.signal-label {
  display: block;
  color: var(--color-text-muted);
  font-size: 12px;
  margin-bottom: 14px;
}

.signal-card strong {
  color: var(--color-text);
  font-size: 15px;
}

.auth-card {
  width: 100%;
  padding: 36px;
  border-radius: 8px;
  background: rgba(10, 16, 26, 0.78);
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.36);
}

.auth-logo {
  margin-bottom: 28px;
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

.auth-tabs {
  margin-bottom: 8px;
}

.auth-form {
  margin-top: 18px;
}

.auth-submit {
  margin-top: 8px;
  height: 46px;
}

.captcha-row {
  display: flex;
  gap: 10px;
  width: 100%;
}

.captcha-row .el-input {
  flex: 1;
}

.captcha-code {
  flex-shrink: 0;
  min-width: 118px;
  height: 42px;
  border: 1px solid rgba(34, 211, 238, 0.28);
  border-radius: 8px;
  color: var(--color-accent);
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), rgba(52, 211, 153, 0.1)),
    repeating-linear-gradient(45deg, transparent 0 7px, rgba(255, 255, 255, 0.06) 7px 8px);
  font-size: 18px;
  font-weight: 800;
  letter-spacing: 0;
  cursor: pointer;
  user-select: none;
  transition: border-color 0.2s, filter 0.2s, box-shadow 0.2s;
}

.captcha-code:hover {
  border-color: var(--color-accent);
  filter: brightness(1.12);
  box-shadow: 0 0 18px rgba(34, 211, 238, 0.18);
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
  background: rgba(34, 211, 238, 0.1);
  border: 1px solid var(--color-border);
  color: var(--color-primary);
  border-radius: 8px;
  font-size: 13px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.2s, border-color 0.2s;
}

.code-btn:hover:not(:disabled) {
  background: rgba(34, 211, 238, 0.18);
  border-color: var(--color-primary);
}

.code-btn:disabled {
  color: var(--color-text-disabled);
  border-color: rgba(34, 211, 238, 0.1);
  cursor: not-allowed;
}

.auth-footer-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding-top: 8px;
}

.auth-footer {
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

:deep(.el-tabs__nav) {
  width: 100%;
}

:deep(.el-tabs__item) {
  flex: 1;
  justify-content: center;
}

:deep(.el-input__wrapper) {
  min-height: 42px;
  border-radius: 8px;
}

@media (max-width: 900px) {
  .auth-shell {
    grid-template-columns: 1fr;
    max-width: 460px;
  }

  .brand-panel {
    min-height: auto;
    padding: 28px;
  }

  .brand-mark {
    margin-bottom: 36px;
  }

  .brand-title {
    font-size: 30px;
  }

  .signal-board {
    position: relative;
    left: auto;
    right: auto;
    bottom: auto;
    margin-top: 28px;
    grid-template-columns: 1fr;
  }
}

@media (max-width: 520px) {
  .auth-page {
    padding: 20px 14px;
  }

  .brand-panel {
    display: none;
  }

  .auth-card {
    padding: 28px 20px;
  }

  .code-row {
    flex-direction: column;
  }

  .captcha-row {
    flex-direction: column;
  }

  .code-btn,
  .captcha-code {
    width: 100%;
  }

  .auth-footer-row {
    flex-direction: column;
    gap: 0;
  }
}
</style>
