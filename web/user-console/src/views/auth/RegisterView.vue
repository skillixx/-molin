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
  if (value.length < 8 || value.length > 32) return callback(new Error('密码长度为 8-32 位'))
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

// 清理定时器
onUnmounted(() => {
  clearInterval(phoneTimer)
  clearInterval(emailTimer)
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

      <p class="auth-hint">注册需同时验证手机号与邮箱，请确保两者均可正常接收验证码</p>

      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-position="top"
        class="auth-form"
      >
        <el-form-item label="用户名（选填）" prop="username">
          <el-input
            v-model="form.username"
            placeholder="2-32位字母/数字/下划线"
            autocomplete="username"
          />
        </el-form-item>

        <!-- 手机号区块 -->
        <div class="form-section">
          <div class="form-section-title">手机号验证</div>
          <el-form-item label="手机号" prop="phone">
            <el-input
              v-model="form.phone"
              placeholder="请输入11位手机号"
              maxlength="11"
              autocomplete="tel"
            />
          </el-form-item>

          <el-form-item label="手机验证码" prop="phoneCode">
            <div class="code-row">
              <el-input
                v-model="form.phoneCode"
                placeholder="请输入短信验证码"
                maxlength="6"
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
            />
          </el-form-item>

          <el-form-item label="邮箱验证码" prop="emailCode">
            <div class="code-row">
              <el-input
                v-model="form.emailCode"
                placeholder="请输入邮箱验证码"
                maxlength="6"
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

        <el-form-item label="设置密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="8-32位，包含字母和数字"
            show-password
            autocomplete="new-password"
          />
        </el-form-item>

        <el-form-item label="确认密码" prop="confirmPassword">
          <el-input
            v-model="form.confirmPassword"
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
            @click.prevent="handleRegister"
          >
            {{ submitting ? '注册中...' : '立即注册' }}
          </button>
        </el-form-item>
      </el-form>

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
  width: 440px;
  padding: 40px;
}

.auth-logo {
  text-align: center;
  margin-bottom: 16px;
}

.auth-subtitle {
  color: var(--color-text-muted);
  font-size: 12px;
  margin-top: 6px;
}

.auth-hint {
  text-align: center;
  color: var(--color-text-muted);
  font-size: 12px;
  margin: 0 0 20px;
  line-height: 1.6;
}

.auth-form {
  margin-top: 4px;
}

/* 区块容器：手机号区块 / 邮箱区块 */
.form-section {
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 16px 16px 4px;
  margin-bottom: 18px;
  background: rgba(99, 102, 241, 0.04);
}

.form-section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-primary);
  margin-bottom: 12px;
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
