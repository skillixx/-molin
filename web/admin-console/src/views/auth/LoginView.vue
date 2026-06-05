<template>
  <!-- 登录页 -->
  <div class="login-page">
    <!-- 背景点阵 -->
    <div class="bg-grid" />

    <div class="login-card">
      <!-- Logo / 品牌 -->
      <div class="brand">
        <h1 class="brand-title">墨灵</h1>
        <p class="brand-sub">爱斯琴网络科技有限公司 · 管理后台</p>
      </div>

      <!-- 登录表单 -->
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        class="login-form"
        @submit.prevent="handleLogin"
      >
        <el-form-item prop="email">
          <el-input
            v-model="form.email"
            placeholder="管理员邮箱"
            size="large"
            :prefix-icon="Message"
            autocomplete="username"
          />
        </el-form-item>

        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="登录密码"
            size="large"
            :prefix-icon="Lock"
            show-password
            autocomplete="current-password"
            @keyup.enter="handleLogin"
          />
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            size="large"
            :loading="loading"
            class="login-btn"
            native-type="submit"
            @click="handleLogin"
          >
            登录
          </el-button>
        </el-form-item>
      </el-form>

      <!-- 错误提示 -->
      <el-alert
        v-if="errorMsg"
        :title="errorMsg"
        type="error"
        show-icon
        :closable="false"
        class="error-alert"
      />

      <p class="footer-text">© 爱斯琴网络科技有限公司</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { Message, Lock } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const formRef = ref<FormInstance>()
const loading = ref(false)
const errorMsg = ref('')

const form = reactive({
  email: '',
  password: '',
})

const rules: FormRules = {
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, message: '密码长度不少于 6 位', trigger: 'blur' },
  ],
}

async function handleLogin() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  errorMsg.value = ''

  try {
    await authStore.login(form.email, form.password)
    // 登录成功，跳转到来源页面或首页
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } catch (err: unknown) {
    // 错误已由 http 拦截器的 ElMessage 处理，这里只保留本地提示
    if (err instanceof Error) {
      errorMsg.value = err.message || '登录失败，请检查邮箱和密码'
    } else {
      errorMsg.value = '登录失败，请检查邮箱和密码'
    }
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #0A0F1E;
  position: relative;
  overflow: hidden;
}

/* 点阵背景 */
.bg-grid {
  position: absolute;
  inset: 0;
  background-image: radial-gradient(circle, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
  background-size: 28px 28px;
  pointer-events: none;
  z-index: 0;
}

.login-card {
  position: relative;
  z-index: 1;
  width: 420px;
  padding: 40px 36px 32px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(99, 102, 241, 0.25);
  border-radius: 16px;
  backdrop-filter: blur(12px);
  box-shadow: 0 8px 40px rgba(0, 0, 0, 0.4), 0 0 60px rgba(99, 102, 241, 0.08);
}

.brand {
  text-align: center;
  margin-bottom: 32px;
}

.brand-title {
  font-size: 36px;
  font-weight: 800;
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  margin: 0 0 8px;
  letter-spacing: 4px;
}

.brand-sub {
  color: #94A3B8;
  font-size: 13px;
  margin: 0;
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.login-btn {
  width: 100%;
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  border: none;
  font-size: 15px;
  font-weight: 500;
  letter-spacing: 1px;
  transition: all 0.25s;
}

.login-btn:hover {
  background: linear-gradient(315deg, #6366F1, #8B5CF6);
  box-shadow: 0 0 20px rgba(99, 102, 241, 0.5);
  transform: translateY(-1px);
}

.error-alert {
  margin-top: 12px;
  border-radius: 8px;
}

.footer-text {
  text-align: center;
  color: #475569;
  font-size: 12px;
  margin: 20px 0 0;
}
</style>
