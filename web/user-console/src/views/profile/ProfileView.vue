<script setup lang="ts">
/**
 * 个人信息页（需登录）
 * 包含 4 个模块：
 *   1. 基本信息展示（只读）
 *   2. 修改用户名
 *   3. 修改手机号（两步 OTP）
 *   4. 修改邮箱（两步 OTP）
 *   5. 修改密码
 */
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import {
  updateUsername,
  updatePhone,
  updateEmail,
  changePassword,
  sendPhoneCode,
  sendEmailCode,
} from '@/api/auth'

const authStore = useAuthStore()

// =================== 实名状态映射 ===================

// 实名状态映射，type 值对齐 el-tag 的 type 属性
const realNameMap: Record<string, { label: string; type: 'success' | 'warning' | 'info' | 'danger' }> = {
  unverified: { label: '未认证', type: 'info' },
  pending:    { label: '审核中', type: 'warning' },
  verified:   { label: '已认证', type: 'success' },
  rejected:   { label: '已拒绝', type: 'danger' },
}

const realNameTag = computed(() => {
  const status = authStore.currentUser?.real_name_status ?? 'unverified'
  return realNameMap[status] ?? realNameMap['unverified']
})

// =================== 模块一：修改用户名 ===================

const usernameFormRef = ref<FormInstance>()
const usernameForm = reactive({ username: '' })
const usernameSubmitting = ref(false)

const usernameRules: FormRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    {
      pattern: /^[a-zA-Z0-9_]{2,32}$/,
      message: '用户名为2-32位字母、数字或下划线',
      trigger: 'blur',
    },
  ],
}

async function handleUpdateUsername() {
  const valid = await usernameFormRef.value?.validate().catch(() => false)
  if (!valid) return

  usernameSubmitting.value = true
  try {
    await updateUsername(usernameForm.username)
    ElMessage.success('用户名修改成功')
    usernameForm.username = ''
    await authStore.fetchMe()
  } catch (err: unknown) {
    const status = (err as { response?: { status?: number } })?.response?.status
    if (status === 409) {
      ElMessage.error('用户名已被占用')
    }
    // 其他错误由 http 拦截器统一处理
  } finally {
    usernameSubmitting.value = false
  }
}

// =================== 模块二：修改手机号 ===================

const phoneStep = ref<1 | 2>(1)   // 1=填手机号，2=填验证码
const phoneFormRef = ref<FormInstance>()
const phoneForm = reactive({ phone: '', code: '' })
const phoneCountdown = ref(0)
const phoneSending = ref(false)
const phoneSubmitting = ref(false)
let phoneTimer: ReturnType<typeof setInterval>

const phoneStep1Rules: FormRules = {
  phone: [
    { required: true, message: '请输入新手机号', trigger: 'blur' },
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的11位手机号', trigger: 'blur' },
  ],
}
const phoneStep2Rules: FormRules = {
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { len: 6, message: '验证码为6位', trigger: 'blur' },
  ],
}

async function sendPhoneVerifyCode() {
  const valid = await (phoneFormRef.value?.validateField('phone') as Promise<boolean>).catch(() => false)
  if (!valid) return
  if (phoneCountdown.value > 0 || phoneSending.value) return

  phoneSending.value = true
  try {
    await sendPhoneCode(phoneForm.phone, 'bind_phone')
    ElMessage.success('验证码已发送至新手机号')
    phoneStep.value = 2
    phoneCountdown.value = 60
    phoneTimer = setInterval(() => {
      phoneCountdown.value--
      if (phoneCountdown.value <= 0) clearInterval(phoneTimer)
    }, 1000)
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 42900) {
      ElMessage.error('发送频率超限，请稍后再试')
    }
  } finally {
    phoneSending.value = false
  }
}

async function handleUpdatePhone() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return

  phoneSubmitting.value = true
  try {
    await updatePhone({ phone: phoneForm.phone, code: phoneForm.code })
    ElMessage.success('手机号修改成功')
    phoneForm.phone = ''
    phoneForm.code = ''
    phoneStep.value = 1
    clearInterval(phoneTimer)
    phoneCountdown.value = 0
    await authStore.fetchMe()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 40000) {
      ElMessage.error('验证码错误或已过期')
    }
  } finally {
    phoneSubmitting.value = false
  }
}

function resetPhoneStep() {
  phoneStep.value = 1
  phoneForm.phone = ''
  phoneForm.code = ''
  clearInterval(phoneTimer)
  phoneCountdown.value = 0
}

// =================== 模块三：修改邮箱 ===================

const emailStep = ref<1 | 2>(1)
const emailFormRef = ref<FormInstance>()
const emailForm = reactive({ email: '', code: '' })
const emailCountdown = ref(0)
const emailSending = ref(false)
const emailSubmitting = ref(false)
let emailTimer: ReturnType<typeof setInterval>

const emailStep1Rules: FormRules = {
  email: [
    { required: true, message: '请输入新邮箱地址', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
}
const emailStep2Rules: FormRules = {
  code: [
    { required: true, message: '请输入验证码', trigger: 'blur' },
    { len: 6, message: '验证码为6位', trigger: 'blur' },
  ],
}

async function sendEmailVerifyCode() {
  const valid = await (emailFormRef.value?.validateField('email') as Promise<boolean>).catch(() => false)
  if (!valid) return
  if (emailCountdown.value > 0 || emailSending.value) return

  emailSending.value = true
  try {
    await sendEmailCode(emailForm.email, 'bind_email')
    ElMessage.success('验证码已发送至新邮箱')
    emailStep.value = 2
    emailCountdown.value = 60
    emailTimer = setInterval(() => {
      emailCountdown.value--
      if (emailCountdown.value <= 0) clearInterval(emailTimer)
    }, 1000)
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 42900) {
      ElMessage.error('发送频率超限，请稍后再试')
    }
  } finally {
    emailSending.value = false
  }
}

async function handleUpdateEmail() {
  const valid = await emailFormRef.value?.validate().catch(() => false)
  if (!valid) return

  emailSubmitting.value = true
  try {
    await updateEmail({ email: emailForm.email, code: emailForm.code })
    ElMessage.success('邮箱修改成功')
    emailForm.email = ''
    emailForm.code = ''
    emailStep.value = 1
    clearInterval(emailTimer)
    emailCountdown.value = 0
    await authStore.fetchMe()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 40000) {
      ElMessage.error('验证码错误或已过期')
    }
  } finally {
    emailSubmitting.value = false
  }
}

function resetEmailStep() {
  emailStep.value = 1
  emailForm.email = ''
  emailForm.code = ''
  clearInterval(emailTimer)
  emailCountdown.value = 0
}

// =================== 模块四：修改密码 ===================

const passwordFormRef = ref<FormInstance>()
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})
const passwordSubmitting = ref(false)

const passwordRules: FormRules = {
  old_password: [
    { required: true, message: '请输入旧密码', trigger: 'blur' },
  ],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, message: '密码至少8位', trigger: 'blur' },
  ],
  confirm_password: [
    { required: true, message: '请确认新密码', trigger: 'blur' },
    {
      validator: (_rule: unknown, val: string, cb: (err?: Error) => void) => {
        if (val !== passwordForm.new_password) {
          cb(new Error('两次密码不一致'))
        } else {
          cb()
        }
      },
      trigger: 'blur',
    },
  ],
}

async function handleChangePassword() {
  const valid = await passwordFormRef.value?.validate().catch(() => false)
  if (!valid) return

  passwordSubmitting.value = true
  try {
    await changePassword({
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    ElMessage.success('密码修改成功')
    // 清空表单
    passwordForm.old_password = ''
    passwordForm.new_password = ''
    passwordForm.confirm_password = ''
    passwordFormRef.value?.clearValidate()
  } catch (err: unknown) {
    const code = (err as { response?: { data?: { code?: number } } })?.response?.data?.code
    if (code === 40001) {
      ElMessage.error('旧密码错误')
    }
  } finally {
    passwordSubmitting.value = false
  }
}

// 页面销毁时清除计时器
onUnmounted(() => {
  clearInterval(phoneTimer)
  clearInterval(emailTimer)
})
</script>

<template>
  <div class="profile-page">
    <!-- 页面标题 -->
    <div class="page-header">
      <h2 class="page-title">个人信息</h2>
      <p class="page-desc">管理你的账号信息和安全设置</p>
    </div>

    <div class="profile-content">

      <!-- 模块一：基本信息展示 -->
      <div class="profile-card glass-card">
        <div class="card-header">
          <el-icon class="card-icon"><user /></el-icon>
          <span class="card-title">基本信息</span>
        </div>
        <div class="info-grid">
          <div class="info-item">
            <span class="info-label">用户 ID</span>
            <span class="info-value accent">{{ authStore.currentUser?.id ?? '—' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">用户名</span>
            <span class="info-value" :class="{ muted: !authStore.currentUser?.username }">
              {{ authStore.currentUser?.username ?? '未设置' }}
            </span>
          </div>
          <div class="info-item">
            <span class="info-label">手机号</span>
            <span class="info-value" :class="{ muted: !authStore.currentUser?.phone }">
              {{ authStore.currentUser?.phone ?? '未绑定' }}
            </span>
          </div>
          <div class="info-item">
            <span class="info-label">邮箱</span>
            <span class="info-value" :class="{ muted: !authStore.currentUser?.email }">
              {{ authStore.currentUser?.email ?? '未绑定' }}
            </span>
          </div>
          <div class="info-item">
            <span class="info-label">实名认证</span>
            <el-tag
              :type="realNameTag.type"
              size="small"
              effect="dark"
            >
              {{ realNameTag.label }}
            </el-tag>
          </div>
          <div class="info-item">
            <span class="info-label">注册时间</span>
            <span class="info-value muted">
              {{ authStore.currentUser?.created_at
                  ? new Date(authStore.currentUser.created_at).toLocaleDateString('zh-CN')
                  : '—' }}
            </span>
          </div>
        </div>
      </div>

      <!-- 模块二：修改用户名 -->
      <div class="profile-card glass-card">
        <div class="card-header">
          <el-icon class="card-icon"><edit /></el-icon>
          <span class="card-title">修改用户名</span>
        </div>
        <p class="card-hint">2-32位字母、数字或下划线，提交后全局唯一</p>
        <el-form
          ref="usernameFormRef"
          :model="usernameForm"
          :rules="usernameRules"
          label-position="top"
          class="profile-form"
        >
          <el-form-item label="新用户名" prop="username">
            <div class="inline-row">
              <el-input
                v-model="usernameForm.username"
                placeholder="请输入新用户名"
                maxlength="32"
                :prefix-icon="'User'"
              />
              <el-button
                type="primary"
                :loading="usernameSubmitting"
                class="inline-btn"
                @click="handleUpdateUsername"
              >
                保存
              </el-button>
            </div>
          </el-form-item>
        </el-form>
      </div>

      <!-- 模块三：修改手机号 -->
      <div class="profile-card glass-card">
        <div class="card-header">
          <el-icon class="card-icon"><phone /></el-icon>
          <span class="card-title">修改手机号</span>
        </div>
        <p v-if="authStore.currentUser?.phone" class="card-hint">
          当前绑定：<span class="accent">{{ authStore.currentUser.phone }}</span>
        </p>

        <el-form
          ref="phoneFormRef"
          :model="phoneForm"
          :rules="phoneStep === 1 ? phoneStep1Rules : phoneStep2Rules"
          label-position="top"
          class="profile-form"
        >
          <!-- Step 1：输入新手机号 -->
          <template v-if="phoneStep === 1">
            <el-form-item label="新手机号" prop="phone">
              <div class="inline-row">
                <el-input
                  v-model="phoneForm.phone"
                  placeholder="请输入新手机号"
                  maxlength="11"
                />
                <el-button
                  type="primary"
                  :loading="phoneSending"
                  class="inline-btn"
                  @click="sendPhoneVerifyCode"
                >
                  发送验证码
                </el-button>
              </div>
            </el-form-item>
          </template>

          <!-- Step 2：输入验证码 -->
          <template v-else>
            <p class="step-hint">
              验证码已发送至 <span class="accent">{{ phoneForm.phone }}</span>
              <el-link class="change-link" @click="resetPhoneStep">更换号码</el-link>
            </p>
            <el-form-item label="验证码" prop="code">
              <div class="code-row">
                <el-input
                  v-model="phoneForm.code"
                  placeholder="请输入6位验证码"
                  maxlength="6"
                />
                <button
                  class="code-btn"
                  :disabled="phoneCountdown > 0 || phoneSending"
                  @click.prevent="sendPhoneVerifyCode"
                >
                  {{ phoneCountdown > 0 ? `${phoneCountdown}s 后重发` : '重新发送' }}
                </button>
              </div>
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                :loading="phoneSubmitting"
                class="submit-btn"
                @click="handleUpdatePhone"
              >
                确认修改手机号
              </el-button>
            </el-form-item>
          </template>
        </el-form>
      </div>

      <!-- 模块四：修改邮箱 -->
      <div class="profile-card glass-card">
        <div class="card-header">
          <el-icon class="card-icon"><message /></el-icon>
          <span class="card-title">修改邮箱</span>
        </div>
        <p v-if="authStore.currentUser?.email" class="card-hint">
          当前绑定：<span class="accent">{{ authStore.currentUser.email }}</span>
        </p>

        <el-form
          ref="emailFormRef"
          :model="emailForm"
          :rules="emailStep === 1 ? emailStep1Rules : emailStep2Rules"
          label-position="top"
          class="profile-form"
        >
          <!-- Step 1：输入新邮箱 -->
          <template v-if="emailStep === 1">
            <el-form-item label="新邮箱地址" prop="email">
              <div class="inline-row">
                <el-input
                  v-model="emailForm.email"
                  placeholder="请输入新邮箱地址"
                  type="email"
                />
                <el-button
                  type="primary"
                  :loading="emailSending"
                  class="inline-btn"
                  @click="sendEmailVerifyCode"
                >
                  发送验证码
                </el-button>
              </div>
            </el-form-item>
          </template>

          <!-- Step 2：输入验证码 -->
          <template v-else>
            <p class="step-hint">
              验证码已发送至 <span class="accent">{{ emailForm.email }}</span>
              <el-link class="change-link" @click="resetEmailStep">更换邮箱</el-link>
            </p>
            <el-form-item label="验证码" prop="code">
              <div class="code-row">
                <el-input
                  v-model="emailForm.code"
                  placeholder="请输入6位验证码"
                  maxlength="6"
                />
                <button
                  class="code-btn"
                  :disabled="emailCountdown > 0 || emailSending"
                  @click.prevent="sendEmailVerifyCode"
                >
                  {{ emailCountdown > 0 ? `${emailCountdown}s 后重发` : '重新发送' }}
                </button>
              </div>
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                :loading="emailSubmitting"
                class="submit-btn"
                @click="handleUpdateEmail"
              >
                确认修改邮箱
              </el-button>
            </el-form-item>
          </template>
        </el-form>
      </div>

      <!-- 模块五：修改密码 -->
      <div class="profile-card glass-card">
        <div class="card-header">
          <el-icon class="card-icon"><lock /></el-icon>
          <span class="card-title">修改密码</span>
        </div>
        <el-form
          ref="passwordFormRef"
          :model="passwordForm"
          :rules="passwordRules"
          label-position="top"
          class="profile-form"
        >
          <el-form-item label="当前密码" prop="old_password">
            <el-input
              v-model="passwordForm.old_password"
              type="password"
              placeholder="请输入当前密码"
              show-password
              autocomplete="current-password"
            />
          </el-form-item>
          <el-form-item label="新密码" prop="new_password">
            <el-input
              v-model="passwordForm.new_password"
              type="password"
              placeholder="至少8位"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>
          <el-form-item label="确认新密码" prop="confirm_password">
            <el-input
              v-model="passwordForm.confirm_password"
              type="password"
              placeholder="再次输入新密码"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              :loading="passwordSubmitting"
              class="submit-btn"
              @click="handleChangePassword"
            >
              修改密码
            </el-button>
          </el-form-item>
        </el-form>
      </div>

    </div>
  </div>
</template>

<style scoped>
/* 页面标题区 */
.profile-page {
  padding: 24px;
  max-width: 720px;
  margin: 0 auto;
}

@media (max-width: 767px) {
  .profile-page {
    padding: 16px;
  }
}

.page-header {
  margin-bottom: 24px;
}

.page-title {
  font-size: 22px;
  font-weight: 700;
  color: var(--color-text);
}

.page-desc {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-top: 6px;
}

/* 卡片 */
.profile-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.profile-card {
  padding: 24px;
}

@media (max-width: 767px) {
  .profile-card {
    padding: 18px 16px;
    border-radius: 12px;
  }
}

/* 卡片头部 */
.card-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 16px;
}

.card-icon {
  font-size: 18px;
  color: var(--color-primary);
}

.card-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--color-text);
}

.card-hint {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-bottom: 16px;
}

/* 基本信息网格 */
.info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px 24px;
}

@media (max-width: 480px) {
  .info-grid {
    grid-template-columns: 1fr;
  }
}

.info-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.info-label {
  font-size: 12px;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.info-value {
  font-size: 14px;
  color: var(--color-text);
  font-weight: 500;
}

.info-value.accent {
  color: var(--color-accent);
}

.info-value.muted,
.muted {
  color: var(--color-text-muted);
  font-weight: 400;
}

/* 强调色文本 */
.accent {
  color: var(--color-accent);
}

/* 表单 */
.profile-form {
  margin-top: 4px;
}

/* 行内布局（输入框 + 按钮同行） */
.inline-row {
  display: flex;
  gap: 10px;
  width: 100%;
}

.inline-row .el-input {
  flex: 1;
}

.inline-btn {
  flex-shrink: 0;
  background: var(--gradient-primary);
  border: none;
  color: #fff;
  font-size: 13px;
  transition: filter 0.2s;
}

.inline-btn:hover {
  filter: brightness(1.15);
}

/* 提交按钮（全宽变体） */
.submit-btn {
  width: 100%;
  height: 42px;
  background: var(--gradient-primary) !important;
  border: none !important;
  font-size: 14px;
}

/* 步骤提示 */
.step-hint {
  font-size: 13px;
  color: var(--color-text-muted);
  margin-bottom: 14px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.change-link {
  font-size: 12px;
  color: var(--color-primary) !important;
  cursor: pointer;
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
  padding: 0 12px;
  height: 32px;
  background: rgba(99, 102, 241, 0.12);
  border: 1px solid var(--color-border);
  color: var(--color-primary);
  border-radius: 6px;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.2s;
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
</style>
