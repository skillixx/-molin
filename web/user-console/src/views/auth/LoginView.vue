<script setup lang="ts">
/**
 * 登录页
 * 支持邮箱密码登录和手机号验证码快捷登录
 * 登录成功后跳转到商品市场
 */
import { ref, reactive, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { sendPhoneCode } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

// 当前 Tab
const activeTab = ref<'email' | 'phone'>('email')

// 邮箱登录表单
const emailForm = reactive({ email: '', password: '' })

// 手机号登录表单
const phoneForm = reactive({ phone: '', code: '' })

// 验证码倒计时
const phoneCountdown = ref(0)
let phoneTimer: ReturnType<typeof setInterval>

// 提交状态
const submitting = ref(false)
const sendingCode = ref(false)

// 表单 ref
const emailFormRef = ref<FormInstance>()
const phoneFormRef = ref<FormInstance>()

// =================== 校验规则 ===================

const emailRules: FormRules = {
  email: [
    { required: true, message: '请输入邮箱地址', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

const phoneRules: FormRules = {
  phone: [
    { required: true, message: '请输入手机号', trigger: 'blur' },
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的11位手机号', trigger: 'blur' },
  ],
  code: [{ required: true, message: '请输入验证码', trigger: 'blur' }],
}

// =================== 发送验证码 ===================

async function sendVerifyCode() {
  if (phoneCountdown.value > 0 || sendingCode.value) return
  if (!phoneForm.phone || !/^1[3-9]\d{9}$/.test(phoneForm.phone)) {
    ElMessage.warning('请输入正确的手机号')
    return
  }
  sendingCode.value = true
  try {
    await sendPhoneCode(phoneForm.phone, 'login')
    ElMessage.success('验证码已发送')
    phoneCountdown.value = 60
    phoneTimer = setInterval(() => {
      phoneCountdown.value--
      if (phoneCountdown.value <= 0) clearInterval(phoneTimer)
    }, 1000)
  } finally {
    sendingCode.value = false
  }
}

// =================== 登录 ===================

async function handleEmailLogin() {
  const valid = await emailFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    await authStore.loginWithEmail(emailForm.email, emailForm.password)
    ElMessage.success('登录成功')
    router.push('/marketplace')
  } finally {
    submitting.value = false
  }
}

async function handlePhoneLogin() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    await authStore.loginWithPhone(phoneForm.phone, phoneForm.code)
    ElMessage.success('登录成功')
    router.push('/marketplace')
  } finally {
    submitting.value = false
  }
}

onUnmounted(() => clearInterval(phoneTimer))
</script>

<template>
  <div class="auth-page page-bg">
    <div class="auth-card glass-card">
      <!-- Logo -->
      <div class="auth-logo">
        <span class="logo-text">墨灵</span>
        <p class="auth-subtitle">欢迎回来</p>
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
              />
            </el-form-item>

            <el-form-item label="密码" prop="password">
              <el-input
                v-model="emailForm.password"
                type="password"
                placeholder="请输入密码"
                show-password
                autocomplete="current-password"
              />
            </el-form-item>

            <el-form-item>
              <button
                class="btn-primary"
                :disabled="submitting"
                @click.prevent="handleEmailLogin"
              >
                {{ submitting ? '登录中...' : '登  录' }}
              </button>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 手机号验证码快捷登录 -->
        <el-tab-pane label="手机号登录" name="phone">
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
                placeholder="138xxxxxxxx"
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
                  @click.prevent="sendVerifyCode"
                >
                  {{ phoneCountdown > 0 ? `${phoneCountdown}s 后重发` : '发送验证码' }}
                </button>
              </div>
            </el-form-item>

            <el-form-item>
              <button
                class="btn-primary"
                :disabled="submitting"
                @click.prevent="handlePhoneLogin"
              >
                {{ submitting ? '登录中...' : '登  录' }}
              </button>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>

      <!-- 底部跳转 -->
      <p class="auth-footer">
        没有账号？
        <router-link to="/register" class="auth-link">立即注册 →</router-link>
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
  font-size: 14px;
  margin-top: 8px;
}

.auth-tabs {
  margin-bottom: 8px;
}

.auth-form {
  margin-top: 16px;
}

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
