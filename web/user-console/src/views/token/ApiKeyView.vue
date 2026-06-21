<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Key, Plus } from '@element-plus/icons-vue'
import { createApiKey, listApiKeys, listModels, revokeApiKey } from '@/api/token'
import type { ApiKeyItem, CreatedApiKey, TokenModel } from '@/types/token'
import { formatDateTime } from '@/utils/display'

const loading = ref(false)
const saving = ref(false)
const revokingId = ref<number | null>(null)
const rows = ref<ApiKeyItem[]>([])
const models = ref<TokenModel[]>([])
const dialogVisible = ref(false)
const secretDialogVisible = ref(false)
const createdKey = ref<CreatedApiKey | null>(null)
const form = reactive({ name: '', model_scope: [] as string[] })
const query = reactive({ page: 1, page_size: 20, total: 0 })

onMounted(async () => {
  await Promise.all([fetchRows(), fetchModels()])
})

async function fetchRows() {
  loading.value = true
  try {
    const res = await listApiKeys({ page: query.page, page_size: query.page_size })
    rows.value = res.items
    query.page = res.page
    query.page_size = res.page_size
    query.total = res.total
  } finally {
    loading.value = false
  }
}

async function fetchModels() {
  const res = await listModels()
  models.value = res.items
}

function openCreate() {
  form.name = ''
  form.model_scope = []
  dialogVisible.value = true
}

async function handleCreate() {
  const name = form.name.trim()
  if (!name) {
    ElMessage.warning('请填写密钥名称')
    return
  }
  saving.value = true
  try {
    const payload = { name, ...(form.model_scope.length ? { model_scope: form.model_scope } : {}) }
    createdKey.value = await createApiKey(payload)
    dialogVisible.value = false
    secretDialogVisible.value = true
    await fetchRows()
  } finally {
    saving.value = false
  }
}

async function copySecret() {
  if (!createdKey.value?.secret_key) return
  await navigator.clipboard.writeText(createdKey.value.secret_key)
  ElMessage.success('密钥已复制')
}

async function handleRevoke(row: unknown) {
  const item = row as ApiKeyItem
  try {
    await ElMessageBox.confirm('吊销后该密钥立即失效，不可恢复。确认吊销？', '吊销 API 密钥', {
      confirmButtonText: '确认吊销',
      cancelButtonText: '取消',
      type: 'warning',
    })
    revokingId.value = item.id
    await revokeApiKey(item.id)
    ElMessage.success('密钥已吊销')
    await fetchRows()
  } catch {
    // 用户取消吊销时不提示错误。
  } finally {
    revokingId.value = null
  }
}

function handlePageChange(page: number) {
  query.page = page
  fetchRows()
}

function handlePageSizeChange(pageSize: number) {
  query.page = 1
  query.page_size = pageSize
  fetchRows()
}

function billingModeLabel(value: string) {
  const map: Record<string, string> = { postpaid: '后付费', prepaid: '预付费' }
  return map[value] ?? value
}

function statusTagType(status: string) {
  return status === 'active' ? 'success' : 'info'
}
</script>

<template>
  <div class="api-key-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <span class="page-kicker">开发者设置</span>
          <h2 class="page-title">API 密钥</h2>
          <p class="page-subtitle">为脚本、外部程序或 Agent 创建平台 sk，密钥明文只会显示一次。</p>
        </div>
        <el-button type="primary" :icon="Plus" @click="openCreate">创建密钥</el-button>
      </div>

      <el-alert
        class="security-alert"
        type="warning"
        show-icon
        :closable="false"
        title="请妥善保管 API 密钥。完整密钥只在创建成功时展示一次，关闭后无法再次查看。"
      />

      <el-table v-loading="loading" :data="rows" class="data-table" border>
        <el-table-column prop="name" label="名称" min-width="150" />
        <el-table-column prop="key_prefix" label="密钥前缀" min-width="170" />
        <el-table-column label="计费模式" width="110">
          <template #default="{ row }">{{ billingModeLabel(row.billing_mode) }}</template>
        </el-table-column>
        <el-table-column label="可用模型" min-width="220">
          <template #default="{ row }">
            <span v-if="!row.model_scope?.length">不限</span>
            <el-tag v-for="model in row.model_scope" v-else :key="model" class="model-tag" size="small">{{ model }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)">{{ row.status === 'active' ? '有效' : '已吊销' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后使用" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.last_used_at) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button
              text
              type="danger"
              :disabled="row.status !== 'active'"
              :loading="revokingId === row.id"
              @click="handleRevoke(row)"
            >
              吊销
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          background
          layout="sizes, prev, pager, next, total"
          :page-sizes="[10, 20, 50, 100]"
          :current-page="query.page"
          :page-size="query.page_size"
          :total="query.total"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>

      <el-dialog v-model="dialogVisible" title="创建 API 密钥" width="560px">
        <el-form label-position="top">
          <el-form-item label="密钥名称" required>
            <el-input v-model="form.name" maxlength="64" placeholder="例如：本地脚本" />
          </el-form-item>
          <el-form-item label="可用模型">
            <el-select v-model="form.model_scope" multiple clearable filterable placeholder="不选择表示不限模型" style="width: 100%">
              <el-option
                v-for="model in models"
                :key="model.logical_model_code"
                :label="model.display_name || model.logical_model_code"
                :value="model.logical_model_code"
              />
            </el-select>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" :icon="Key" :loading="saving" @click="handleCreate">创建</el-button>
        </template>
      </el-dialog>

      <el-dialog v-model="secretDialogVisible" title="请立即保存 API 密钥" width="640px" :close-on-click-modal="false">
        <el-alert
          type="error"
          show-icon
          :closable="false"
          title="密钥只显示一次，关闭后无法再次查看。请立即复制并保存到安全位置。"
        />
        <div class="secret-box">
          <code>{{ createdKey?.secret_key }}</code>
          <el-button type="primary" :icon="CopyDocument" @click="copySecret">复制</el-button>
        </div>
        <template #footer>
          <el-button type="primary" @click="secretDialogVisible = false">我已保存</el-button>
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<style scoped>
.api-key-page { padding: 34px 0 0; }
.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-end;
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(7, 11, 18, 0.62);
  box-shadow: var(--shadow-card);
}
.page-kicker {
  display: inline-flex;
  margin-bottom: 10px;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}
.security-alert { margin-bottom: 16px; }
.model-tag { margin: 2px 4px 2px 0; }
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.secret-box {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  margin-top: 16px;
  padding: 14px;
  border: 1px solid rgba(248, 113, 113, 0.36);
  border-radius: 8px;
  background: rgba(127, 29, 29, 0.18);
}
.secret-box code {
  overflow-wrap: anywhere;
  color: #fecaca;
}
@media (max-width: 720px) {
  .page-header,
  .secret-box {
    grid-template-columns: 1fr;
  }
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
