<script setup lang="ts">
/**
 * 注册页
 * 支持邮箱注册和手机号注册两种方式（el-tabs 切换）
 * 注册成功后自动登录，跳转到商品市场
 */
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { sendEmailCode, sendPhoneCode, registerByEmail, registerByPhone } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

// 当前 Tab：email / phone
const activeTab = ref<'email' | 'phone'>('email')

// 邮箱注册表单
const emailForm = reactive({
  email: '',
  code: '',
  password: '',
  confirmPassword: '',
})

// 手机号注册表单
const phoneForm = reactive({
  phone: '',
  code: '',
  password: '',
  confirmPassword: '',
})

// 验证码倒计时
const emailCountdown = ref(0)
const phoneCountdown = ref(0)
let emailTimer: ReturnType<typeof setInterval>
let phoneTimer: ReturnType<typeof setInterval>

// 提交状态
const submitting = ref(false)
const sendingCode = ref(false)

// 表单 ref
const emailFormRef = ref<FormInstance>()
const phoneFormRef = ref<FormInstance>()

// =================== 表单校验规则 ===================

// 邮箱格式
const emailValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入邮箱地址'))
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) return callback(new Error('邮箱格式不正确'))
  callback()
}

// 手机号格式
const phoneValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入手机号'))
  if (!/^1[3-9]\d{9}$/.test(value)) return callback(new Error('请输入正确的11位手机号'))
  callback()
}

// 密码格式
const passwordValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请输入密码'))
  if (value.length < 8 || value.length > 32) return callback(new Error('密码长度为 8-32 位'))
  if (!/[a-zA-Z]/.test(value) || !/\d/.test(value)) return callback(new Error('密码须包含字母和数字'))
  callback()
}

// 确认密码（邮箱表单）
const emailConfirmValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请再次输入密码'))
  if (value !== emailForm.password) return callback(new Error('两次输入的密码不一致'))
  callback()
}

// 确认密码（手机号表单）
const phoneConfirmValidator = (_: unknown, value: string, callback: (err?: Error) => void) => {
  if (!value) return callback(new Error('请再次输入密码'))
  if (value !== phoneForm.password) return callback(new Error('两次输入的密码不一致'))
  callback()
}

const emailRules: FormRules = {
  email: [{ validator: emailValidator, trigger: 'blur' }],
  code: [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  password: [{ validator: passwordValidator, trigger: 'blur' }],
  confirmPassword: [{ validator: emailConfirmValidator, trigger: 'blur' }],
}

const phoneRules: FormRules = {
  phone: [{ validator: phoneValidator, trigger: 'blur' }],
  code: [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  password: [{ validator: passwordValidator, trigger: 'blur' }],
  confirmPassword: [{ validator: phoneConfirmValidator, trigger: 'blur' }],
}

// =================== 发送验证码 ===================

function startCountdown(countdownRef: typeof emailCountdown, timerRef: { value: ReturnType<typeof setInterval> | undefined }) {
  countdownRef.value = 60
  timerRef.value = setInterval(() => {
    countdownRef.value--
    if (countdownRef.value <= 0) {
      clearInterval(timerRef.value)
    }
  }, 1000)
}

const emailTimerRef = { value: undefined as ReturnType<typeof setInterval> | undefined }
const phoneTimerRef = { value: undefined as ReturnType<typeof setInterval> | undefined }

async function sendEmailVerifyCode() {
  if (emailCountdown.value > 0 || sendingCode.value) return
  if (!emailForm.email) {
    ElMessage.warning('请先输入邮箱地址')
    return
  }
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(emailForm.email)) {
    ElMessage.warning('邮箱格式不正确')
    return
  }
  sendingCode.value = true
  try {
    await sendEmailCode(emailForm.email, 'register')
    ElMessage.success('验证码已发送，请查收邮件')
    startCountdown(emailCountdown, emailTimerRef)
  } finally {
    sendingCode.value = false
  }
}

async function sendPhoneVerifyCode() {
  if (phoneCountdown.value > 0 || sendingCode.value) return
  if (!phoneForm.phone) {
    ElMessage.warning('请先输入手机号')
    return
  }
  if (!/^1[3-9]\d{9}$/.test(phoneForm.phone)) {
    ElMessage.warning('请输入正确的11位手机号')
    return
  }
  sendingCode.value = true
  try {
    await sendPhoneCode(phoneForm.phone, 'register')
    ElMessage.success('验证码已发送，请查收短信')
    startCountdown(phoneCountdown, phoneTimerRef)
  } finally {
    sendingCode.value = false
  }
}

// =================== 提交注册 ===================

async function handleEmailRegister() {
  const valid = await emailFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    // 注册并获取 Token
    const tokens = await registerByEmail({
      email: emailForm.email,
      code: emailForm.code,
      password: emailForm.password,
    })
    // 保存 Token 并拉取用户信息
    localStorage.setItem('access_token', tokens.access_token)
    localStorage.setItem('refresh_token', tokens.refresh_token)
    authStore.accessToken = tokens.access_token
    await authStore.fetchMe()
    ElMessage.success('注册成功，欢迎使用墨灵！')
    router.push('/marketplace')
  } finally {
    submitting.value = false
  }
}

async function handlePhoneRegister() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    const tokens = await registerByPhone({
      phone: phoneForm.phone,
      code: phoneForm.code,
      password: phoneForm.password,
    })
    localStorage.setItem('access_token', tokens.access_token)
    localStorage.setItem('refresh_token', tokens.refresh_token)
    authStore.accessToken = tokens.access_token
    await authStore.fetchMe()
    ElMessage.success('注册成功，欢迎使用墨灵！')
    router.push('/marketplace')
  } finally {
    submitting.value = false
  }
}

// 清理定时器
onUnmounted(() => {
  clearInterval(emailTimer)
  clearInterval(phoneTimer)
})
</script>

<template>
  <div class="auth-page page-bg">
    <div class="auth-card glass-card">
      <!-- Logo -->
      <div class="auth-logo">
        <span class="logo-text">墨灵</span>
        <p class="auth-subtitle">爱斯琴网络科技有限公司</p>
      </div>

      <!-- Tab 切换 -->
      <el-tabs v-model="activeTab" class="auth-tabs">
        <!-- 邮箱注册 -->
        <el-tab-pane label="邮箱注册" name="email">
          <el-form
            ref="emailFormRef"
            :model="emailForm"
            :rules="emailRules"
            label-position="top"
            class="auth-form"
          >
            <el-form-item label="邮箱地址" prop="email">
              <el-input
                v-model="emailForm.email"
                placeholder="user@example.com"
                type="email"
                autocomplete="email"
              />
            </el-form-item>

            <el-form-item label="验证码" prop="code">
              <div class="code-row">
                <el-input
                  v-model="emailForm.code"
                  placeholder="请输入验证码"
                  maxlength="6"
                />
                <button
                  class="code-btn"
                  :disabled="emailCountdown > 0 || sendingCode"
                  @click.prevent="sendEmailVerifyCode"
                >
                  {{ emailCountdown > 0 ? `${emailCountdown}s 后重发` : '发送验证码' }}
                </button>
              </div>
            </el-form-item>

            <el-form-item label="设置密码" prop="password">
              <el-input
                v-model="emailForm.password"
                type="password"
                placeholder="8-32位，包含字母和数字"
                show-password
                autocomplete="new-password"
              />
            </el-form-item>

            <el-form-item label="确认密码" prop="confirmPassword">
              <el-input
                v-model="emailForm.confirmPassword"
                type="password"
                placeholder="再次输入密码"
                show-password
                autocomplete="new-password"
              />
            </el-form-item>

            <el-form-item>
              <button
                class="btn-primary"
                :disabled="submitting"
                @click.prevent="handleEmailRegister"
              >
                {{ submitting ? '注册中...' : '立即注册' }}
              </button>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 手机号注册 -->
        <el-tab-pane label="手机号注册" name="phone">
          <el-form
            ref="phoneFormRef"
            :model="phoneForm"
            :rules="phoneRules"
            label-position="top"
            class="auth-form"
          >
            <el-form-item label="手机号" prop="phone">
              <el-input
                v-model="phoneForm.phone"
                placeholder="请输入11位手机号"
                maxlength="11"
                autocomplete="tel"
              />
            </el-form-item>

            <el-form-item label="验证码" prop="code">
              <div class="code-row">
                <el-input
                  v-model="phoneForm.code"
                  placeholder="请输入短信验证码"
                  maxlength="6"
                />
                <button
                  class="code-btn"
                  :disabled="phoneCountdown > 0 || sendingCode"
                  @click.prevent="sendPhoneVerifyCode"
                >
                  {{ phoneCountdown > 0 ? `${phoneCountdown}s 后重发` : '发送验证码' }}
                </button>
              </div>
            </el-form-item>

            <el-form-item label="设置密码" prop="password">
              <el-input
                v-model="phoneForm.password"
                type="password"
                placeholder="8-32位，包含字母和数字"
                show-password
                autocomplete="new-password"
              />
            </el-form-item>

            <el-form-item label="确认密码" prop="confirmPassword">
              <el-input
                v-model="phoneForm.confirmPassword"
                type="password"
                placeholder="再次输入密码"
                show-password
                autocomplete="new-password"
              />
            </el-form-item>

            <el-form-item>
              <button
                class="btn-primary"
                :disabled="submitting"
                @click.prevent="handlePhoneRegister"
              >
                {{ submitting ? '注册中...' : '立即注册' }}
              </button>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>

      <!-- 底部跳转 -->
      <p class="auth-footer">
        已有账号？
        <router-link to="/login" class="auth-link">去登录 →</router-link>
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
  width: 400px;
  padding: 40px;
}

.auth-logo {
  text-align: center;
  margin-bottom: 28px;
}

.auth-subtitle {
  color: var(--color-text-muted);
  font-size: 12px;
  margin-top: 6px;
}

.auth-tabs {
  margin-bottom: 8px;
}

.auth-form {
  margin-top: 16px;
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
</style>
