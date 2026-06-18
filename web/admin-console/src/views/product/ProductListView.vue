<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Edit, Plus, Refresh, Setting } from '@element-plus/icons-vue'
import {
  createPlan,
  createProduct,
  listAdminProducts,
  listPlans,
  replaceAccess,
  replacePrices,
  updatePlan,
  updateProduct,
  updateProductStatus,
} from '@/api/product-admin'
import { createBillingRule, listBillingRules, updateBillingRule } from '@/api/billing-rule'
import { listRoles } from '@/api/role'
import type { AccessItem, AdminPlan, AdminProduct, PriceItem } from '@/types/product-admin'
import type { BillingRule } from '@/types/billing-rule'
import type { Pagination } from '@/types/api'
import type { Role } from '@/types/user'
import {
  billingTypeLabel,
  displayAmount,
  formatDateTime,
  isPositiveAmount,
  productStatusLabel,
} from '@/utils/display'

const loading = ref(false)
const products = ref<AdminProduct[]>([])
const roles = ref<Role[]>([])
const searchForm = reactive({ keyword: '', status: '', type: '' })
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const productDialogVisible = ref(false)
const productFormRef = ref<FormInstance>()
const editingProduct = ref<AdminProduct | null>(null)
const savingProduct = ref(false)
const productForm = reactive({
  product_type: '',
  product_code: '',
  name: '',
  description: '',
  status: 'draft',
  business_ref_id: undefined as number | undefined,
})
const productRules: FormRules = {
  product_type: [{ required: true, message: '请输入商品类型', trigger: 'blur' }],
  product_code: [{ required: true, message: '请输入商品代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入商品名称', trigger: 'blur' }],
}

const detailVisible = ref(false)
const selectedProduct = ref<AdminProduct | null>(null)
const plans = ref<AdminPlan[]>([])
const billingRules = ref<BillingRule[]>([])
const loadingPlans = ref(false)
const loadingRules = ref(false)

const planDialogVisible = ref(false)
const editingPlan = ref<AdminPlan | null>(null)
const savingPlan = ref(false)
const planForm = reactive({
  plan_code: '',
  name: '',
  billing_type: 'one_time',
  duration_days: undefined as number | undefined,
  quota_json: '',
  status: 'active',
})

const accessDialogVisible = ref(false)
const savingAccess = ref(false)
const accessRows = ref<Array<AccessItem & { role_name?: string }>>([])

const priceDialogVisible = ref(false)
const savingPrices = ref(false)
const priceRows = ref<Array<PriceItem & { price_type: 'default' | 'role' | 'membership' }>>([])

const ruleDialogVisible = ref(false)
const editingRule = ref<BillingRule | null>(null)
const savingRule = ref(false)
const ruleForm = reactive({
  product_id: undefined as number | undefined,
  product_plan_id: undefined as number | undefined,
  usage_type: '',
  usage_unit: '',
  price_amount: '',
  currency: 'CNY',
  billing_mode: 'per_unit',
  free_quota: '',
  status: 'active',
})

onMounted(async () => {
  await Promise.all([fetchProducts(), fetchRoles()])
})

async function fetchProducts() {
  loading.value = true
  try {
    const res = await listAdminProducts({
      keyword: searchForm.keyword || undefined,
      status: searchForm.status || undefined,
      type: searchForm.type || undefined,
      page: pagination.page,
      page_size: pagination.page_size,
    })
    products.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

async function fetchRoles() {
  const res = await listRoles({ page: 1, page_size: 100 })
  roles.value = res.items
}

function handleSearch() {
  pagination.page = 1
  fetchProducts()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchProducts()
}

function openCreateProduct() {
  editingProduct.value = null
  productForm.product_type = ''
  productForm.product_code = ''
  productForm.name = ''
  productForm.description = ''
  productForm.status = 'draft'
  productForm.business_ref_id = undefined
  productDialogVisible.value = true
}

function openEditProduct(product: AdminProduct) {
  editingProduct.value = product
  productForm.product_type = product.product_type
  productForm.product_code = product.product_code
  productForm.name = product.name
  productForm.description = product.description || ''
  productForm.status = product.status
  productForm.business_ref_id = product.business_ref_id ?? undefined
  productDialogVisible.value = true
}

async function saveProduct() {
  const valid = await productFormRef.value?.validate().catch(() => false)
  if (!valid) return
  savingProduct.value = true
  try {
    if (editingProduct.value) {
      await updateProduct(editingProduct.value.id, {
        name: productForm.name,
        description: productForm.description || null,
        business_ref_id: productForm.business_ref_id ?? null,
      })
      ElMessage.success('商品已更新')
    } else {
      await createProduct({
        product_type: productForm.product_type,
        product_code: productForm.product_code,
        name: productForm.name,
        description: productForm.description || undefined,
        status: productForm.status,
        business_ref_id: productForm.business_ref_id ?? undefined,
      })
      ElMessage.success('商品已创建')
    }
    productDialogVisible.value = false
    await fetchProducts()
  } finally {
    savingProduct.value = false
  }
}

async function toggleStatus(product: AdminProduct) {
  const nextStatus = product.status === 'active' ? 'inactive' : 'active'
  await ElMessageBox.confirm(
    `确认${nextStatus === 'active' ? '上架' : '下架'}商品「${product.name}」？`,
    '确认操作',
    { type: 'warning' }
  )
  await updateProductStatus(product.id, nextStatus)
  ElMessage.success(nextStatus === 'active' ? '商品已上架' : '商品已下架')
  await fetchProducts()
}

async function openDetail(product: AdminProduct) {
  selectedProduct.value = product
  detailVisible.value = true
  await Promise.all([fetchPlans(), fetchBillingRules()])
}

async function fetchPlans() {
  if (!selectedProduct.value) return
  loadingPlans.value = true
  try {
    const res = await listPlans(selectedProduct.value.id)
    plans.value = res.items
  } finally {
    loadingPlans.value = false
  }
}

async function fetchBillingRules() {
  if (!selectedProduct.value) return
  loadingRules.value = true
  try {
    const res = await listBillingRules({ product_id: selectedProduct.value.id, page: 1, page_size: 100 })
    billingRules.value = res.items
  } finally {
    loadingRules.value = false
  }
}

function openCreatePlan() {
  editingPlan.value = null
  planForm.plan_code = ''
  planForm.name = ''
  planForm.billing_type = 'one_time'
  planForm.duration_days = undefined
  planForm.quota_json = ''
  planForm.status = 'active'
  planDialogVisible.value = true
}

function openEditPlan(plan: AdminPlan) {
  editingPlan.value = plan
  planForm.plan_code = plan.plan_code
  planForm.name = plan.name
  planForm.billing_type = plan.billing_type
  planForm.duration_days = plan.duration_days ?? undefined
  planForm.quota_json = plan.quota_json || ''
  planForm.status = plan.status
  planDialogVisible.value = true
}

async function savePlan() {
  if (!selectedProduct.value) return
  if (!planForm.name || (!editingPlan.value && !planForm.plan_code)) {
    ElMessage.warning('请填写套餐代码和名称')
    return
  }
  savingPlan.value = true
  try {
    const data = {
      name: planForm.name,
      billing_type: planForm.billing_type,
      duration_days: planForm.duration_days ?? null,
      quota_json: planForm.quota_json || null,
      status: planForm.status,
    }
    if (editingPlan.value) {
      await updatePlan(selectedProduct.value.id, editingPlan.value.id, data)
      ElMessage.success('套餐已更新')
    } else {
      await createPlan(selectedProduct.value.id, { ...data, plan_code: planForm.plan_code })
      ElMessage.success('套餐已创建')
    }
    planDialogVisible.value = false
    await fetchPlans()
  } finally {
    savingPlan.value = false
  }
}

function openAccessDialog() {
  if (roles.value.length === 0) {
    ElMessage.warning('暂无可配置角色，请先维护角色后再配置访问规则')
    return
  }
  accessRows.value = roles.value.map(role => ({
    role_id: role.id,
    role_name: role.name,
    can_view: false,
    can_buy: false,
    can_use: false,
  }))
  accessDialogVisible.value = true
}

function handleAccessBuyChange(row: AccessItem) {
  if (row.can_buy) {
    row.can_view = true
  }
}

function handleAccessUseChange(row: AccessItem) {
  if (row.can_use) {
    row.can_view = true
  }
}

async function saveAccess() {
  if (!selectedProduct.value) return
  savingAccess.value = true
  try {
    // 访问规则接口是覆盖写入，保存时提交全量角色配置，避免未勾选行被前端过滤后造成规则缺失。
    const nextItems = accessRows.value
      .map(({ role_id, can_view, can_buy, can_use }) => ({
        role_id,
        can_view: can_view || can_buy || can_use,
        can_buy,
        can_use,
      }))
    const enabledCount = nextItems.filter(row => row.can_view || row.can_buy || row.can_use).length
    const items = enabledCount === 0 ? [] : nextItems
    await replaceAccess(selectedProduct.value.id, items)
    ElMessage.success(enabledCount === 0 ? '访问规则已清空' : '访问规则已保存')
    accessDialogVisible.value = false
  } finally {
    savingAccess.value = false
  }
}

function openPriceDialog() {
  priceRows.value = plans.value.map(plan => ({
    product_plan_id: plan.id,
    price_type: 'default',
    price_amount: '',
    currency: 'CNY',
  }))
  priceDialogVisible.value = true
}

function addPriceRow() {
  priceRows.value.push({
    product_plan_id: plans.value[0]?.id ?? 0,
    price_type: 'default',
    price_amount: '',
    currency: 'CNY',
  })
}

function removePriceRow(index: number) {
  priceRows.value.splice(index, 1)
}

async function savePrices() {
  if (!selectedProduct.value) return
  const items = priceRows.value
    .filter(row => row.product_plan_id && row.price_amount)
    .map(row => {
      const item: PriceItem = {
        product_plan_id: row.product_plan_id,
        price_amount: row.price_amount,
        currency: row.currency || 'CNY',
      }
      if (row.price_type === 'role') item.role_id = row.role_id
      if (row.price_type === 'membership') item.membership_level_id = row.membership_level_id
      return item
    })
  if (items.length === 0) {
    ElMessage.warning('价格配置至少需要提交一项，不能提交空 items')
    return
  }
  if (items.some(item => !isPositiveAmount(item.price_amount))) {
    ElMessage.warning('价格金额必须大于 0，最多 6 位小数')
    return
  }
  savingPrices.value = true
  try {
    await replacePrices(selectedProduct.value.id, items)
    ElMessage.success('价格配置已保存')
    priceDialogVisible.value = false
  } finally {
    savingPrices.value = false
  }
}

function openCreateRule() {
  if (!selectedProduct.value) return
  editingRule.value = null
  ruleForm.product_id = selectedProduct.value.id
  ruleForm.product_plan_id = undefined
  ruleForm.usage_type = ''
  ruleForm.usage_unit = ''
  ruleForm.price_amount = ''
  ruleForm.currency = 'CNY'
  ruleForm.billing_mode = 'per_unit'
  ruleForm.free_quota = ''
  ruleForm.status = 'active'
  ruleDialogVisible.value = true
}

function openEditRule(rule: BillingRule) {
  editingRule.value = rule
  ruleForm.product_id = rule.product_id
  ruleForm.product_plan_id = rule.product_plan_id ?? undefined
  ruleForm.usage_type = rule.usage_type
  ruleForm.usage_unit = rule.usage_unit
  ruleForm.price_amount = rule.price_amount
  ruleForm.currency = rule.currency
  ruleForm.billing_mode = rule.billing_mode
  ruleForm.free_quota = rule.free_quota || ''
  ruleForm.status = rule.status
  ruleDialogVisible.value = true
}

async function saveRule() {
  if (!ruleForm.product_id || !ruleForm.usage_type || !ruleForm.usage_unit || !ruleForm.price_amount) {
    ElMessage.warning('请填写计费规则必填项')
    return
  }
  if (!isPositiveAmount(ruleForm.price_amount)) {
    ElMessage.warning('单价必须大于 0，最多 6 位小数')
    return
  }
  savingRule.value = true
  try {
    const editableData = {
      usage_type: ruleForm.usage_type,
      usage_unit: ruleForm.usage_unit,
      price_amount: ruleForm.price_amount,
      currency: ruleForm.currency,
      billing_mode: ruleForm.billing_mode,
      free_quota: ruleForm.free_quota || null,
      status: ruleForm.status,
    }
    if (editingRule.value) {
      // 更新接口只提交可编辑字段，避免把创建专用字段带入 PATCH 请求。
      await updateBillingRule(editingRule.value.id, editableData)
      ElMessage.success('计费规则已更新')
    } else {
      await createBillingRule({
        product_id: ruleForm.product_id,
        product_plan_id: ruleForm.product_plan_id ?? null,
        ...editableData,
      })
      ElMessage.success('计费规则已创建')
    }
    ruleDialogVisible.value = false
    await fetchBillingRules()
  } finally {
    savingRule.value = false
  }
}
</script>

<template>
  <div class="product-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">商品管理</h3>
        <p class="page-subtitle">维护商品、套餐、价格、访问规则和按量计费规则</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreateProduct">新建商品</el-button>
    </div>

    <div class="filter-card">
      <el-input v-model="searchForm.keyword" clearable placeholder="商品名称 / 代码" />
      <el-select v-model="searchForm.status" clearable placeholder="商品状态">
        <el-option label="草稿" value="draft" />
        <el-option label="已上架" value="active" />
        <el-option label="已下架" value="inactive" />
      </el-select>
      <el-input v-model="searchForm.type" clearable placeholder="商品类型" />
      <el-button type="primary" :loading="loading" @click="handleSearch">查询</el-button>
      <el-button :icon="Refresh" @click="fetchProducts">刷新</el-button>
    </div>

    <el-table :data="products" v-loading="loading" border>
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="product_code" label="商品代码" min-width="160" />
      <el-table-column prop="name" label="商品名称" min-width="180" />
      <el-table-column prop="product_type" label="类型" width="120" />
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : row.status === 'draft' ? 'info' : 'warning'">
            {{ productStatusLabel(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="更新时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="300" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" text :icon="Setting" @click="openDetail(row)">配置</el-button>
          <el-button text :icon="Edit" @click="openEditProduct(row)">编辑</el-button>
          <el-button type="warning" text @click="toggleStatus(row)">
            {{ row.status === 'active' ? '下架' : '上架' }}
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-row">
      <el-pagination
        background
        layout="prev, pager, next, total"
        :current-page="pagination.page"
        :page-size="pagination.page_size"
        :total="pagination.total"
        @current-change="handlePageChange"
      />
    </div>

    <el-dialog v-model="productDialogVisible" :title="editingProduct ? '编辑商品' : '新建商品'" width="560px">
      <el-form ref="productFormRef" :model="productForm" :rules="productRules" label-width="100px">
        <el-form-item label="商品类型" prop="product_type">
          <el-input v-model="productForm.product_type" :disabled="!!editingProduct" />
        </el-form-item>
        <el-form-item label="商品代码" prop="product_code">
          <el-input v-model="productForm.product_code" :disabled="!!editingProduct" />
        </el-form-item>
        <el-form-item label="商品名称" prop="name">
          <el-input v-model="productForm.name" />
        </el-form-item>
        <el-form-item label="业务引用 ID">
          <el-input-number v-model="productForm.business_ref_id" :min="1" style="width: 100%" />
        </el-form-item>
        <el-form-item v-if="!editingProduct" label="初始状态">
          <el-select v-model="productForm.status" style="width: 100%">
            <el-option label="草稿" value="draft" />
            <el-option label="已上架" value="active" />
            <el-option label="已下架" value="inactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="productForm.description" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="productDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingProduct" @click="saveProduct">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="detailVisible" size="78%" :title="selectedProduct ? `商品配置：${selectedProduct.name}` : '商品配置'">
      <el-tabs>
        <el-tab-pane label="套餐">
          <div class="tab-toolbar">
            <el-button type="primary" :icon="Plus" @click="openCreatePlan">新建套餐</el-button>
            <el-button @click="fetchPlans">刷新</el-button>
          </div>
          <el-table :data="plans" v-loading="loadingPlans" border>
            <el-table-column prop="plan_code" label="套餐代码" min-width="150" />
            <el-table-column prop="name" label="套餐名称" min-width="160" />
            <el-table-column label="计费方式" width="110">
              <template #default="{ row }">{{ billingTypeLabel(row.billing_type) }}</template>
            </el-table-column>
            <el-table-column prop="duration_days" label="有效天数" width="100" />
            <el-table-column prop="status" label="状态" width="100" />
            <el-table-column label="操作" width="100">
              <template #default="{ row }">
                <el-button type="primary" text @click="openEditPlan(row)">编辑</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
        <el-tab-pane label="访问与价格">
          <div class="action-grid">
            <div class="config-card">
              <h4>访问规则</h4>
              <p>覆盖写入角色的 can_view / can_buy / can_use；提交空数组会清空所有规则。</p>
              <el-button type="primary" @click="openAccessDialog">配置访问规则</el-button>
            </div>
            <div class="config-card">
              <h4>价格配置</h4>
              <p>每个价格项必须包含 product_plan_id；价格 items 不可为空。</p>
              <el-button type="primary" @click="openPriceDialog">配置价格</el-button>
            </div>
          </div>
        </el-tab-pane>
        <el-tab-pane label="计费规则">
          <div class="tab-toolbar">
            <el-button type="primary" :icon="Plus" @click="openCreateRule">新增规则</el-button>
            <el-button @click="fetchBillingRules">刷新</el-button>
          </div>
          <el-table :data="billingRules" v-loading="loadingRules" border>
            <el-table-column prop="usage_type" label="用量类型" min-width="140" />
            <el-table-column prop="usage_unit" label="单位" width="100" />
            <el-table-column label="套餐 ID" width="100">
              <template #default="{ row }">{{ row.product_plan_id ?? '商品级' }}</template>
            </el-table-column>
            <el-table-column label="单价" min-width="140">
              <template #default="{ row }">{{ displayAmount(row.price_amount, row.currency) }}</template>
            </el-table-column>
            <el-table-column prop="billing_mode" label="计费模式" width="120" />
            <el-table-column prop="free_quota" label="免费额度" width="120" />
            <el-table-column prop="status" label="状态" width="100" />
            <el-table-column label="操作" width="100">
              <template #default="{ row }">
                <el-button type="primary" text @click="openEditRule(row)">编辑</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
      </el-tabs>
    </el-drawer>

    <el-dialog v-model="planDialogVisible" :title="editingPlan ? '编辑套餐' : '新建套餐'" width="560px">
      <el-form label-width="100px">
        <el-form-item label="套餐代码" required>
          <el-input v-model="planForm.plan_code" :disabled="!!editingPlan" />
        </el-form-item>
        <el-form-item label="套餐名称" required>
          <el-input v-model="planForm.name" />
        </el-form-item>
        <el-form-item label="计费方式">
          <el-select v-model="planForm.billing_type" style="width: 100%">
            <el-option label="一次性" value="one_time" />
            <el-option label="月付" value="monthly" />
            <el-option label="年付" value="yearly" />
            <el-option label="按量" value="usage" />
          </el-select>
        </el-form-item>
        <el-form-item label="有效天数">
          <el-input-number v-model="planForm.duration_days" :min="1" style="width: 100%" />
        </el-form-item>
        <el-form-item label="配额 JSON">
          <el-input v-model="planForm.quota_json" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="planForm.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="planDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingPlan" @click="savePlan">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="accessDialogVisible" title="配置访问规则" width="720px">
      <el-table :data="accessRows" border max-height="420">
        <el-table-column prop="role_name" label="角色" min-width="160" />
        <el-table-column label="可见" width="100"><template #default="{ row }"><el-checkbox v-model="row.can_view" /></template></el-table-column>
        <el-table-column label="可买" width="100">
          <template #default="{ row }">
            <el-checkbox v-model="row.can_buy" @change="handleAccessBuyChange(row)" />
          </template>
        </el-table-column>
        <el-table-column label="可用" width="100">
          <template #default="{ row }">
            <el-checkbox v-model="row.can_use" @change="handleAccessUseChange(row)" />
          </template>
        </el-table-column>
      </el-table>
      <p class="dialog-tip">访问规则按角色全量覆盖保存；勾选可买或可用时会自动包含可见权限。</p>
      <template #footer>
        <el-button @click="accessDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingAccess" @click="saveAccess">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="priceDialogVisible" title="配置价格" width="860px">
      <div class="tab-toolbar"><el-button :icon="Plus" @click="addPriceRow">添加价格项</el-button></div>
      <el-table :data="priceRows" border max-height="420">
        <el-table-column label="套餐" min-width="160">
          <template #default="{ row }">
            <el-select v-model="row.product_plan_id" style="width: 100%">
              <el-option v-for="plan in plans" :key="plan.id" :label="plan.name" :value="plan.id" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="价格类型" width="140">
          <template #default="{ row }">
            <el-select v-model="row.price_type">
              <el-option label="默认价" value="default" />
              <el-option label="角色价" value="role" />
              <el-option label="会员价" value="membership" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="角色" min-width="150">
          <template #default="{ row }">
            <el-select v-model="row.role_id" :disabled="row.price_type !== 'role'" clearable>
              <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="会员等级 ID" width="130">
          <template #default="{ row }">
            <el-input-number v-model="row.membership_level_id" :disabled="row.price_type !== 'membership'" :min="1" />
          </template>
        </el-table-column>
        <el-table-column label="金额" width="160"><template #default="{ row }"><el-input v-model="row.price_amount" /></template></el-table-column>
        <el-table-column label="币种" width="100"><template #default="{ row }"><el-input v-model="row.currency" /></template></el-table-column>
        <el-table-column label="操作" width="80"><template #default="{ $index }"><el-button type="danger" text @click="removePriceRow($index)">删除</el-button></template></el-table-column>
      </el-table>
      <p class="dialog-tip">价格 items 不可为空；金额按字符串提交，前端不做浮点计算。</p>
      <template #footer>
        <el-button @click="priceDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingPrices" @click="savePrices">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="ruleDialogVisible" :title="editingRule ? '编辑计费规则' : '新增计费规则'" width="620px">
      <el-form label-width="110px">
        <el-form-item label="套餐">
          <el-select v-model="ruleForm.product_plan_id" clearable placeholder="不选表示商品级规则" style="width: 100%">
            <el-option v-for="plan in plans" :key="plan.id" :label="plan.name" :value="plan.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="用量类型" required><el-input v-model="ruleForm.usage_type" /></el-form-item>
        <el-form-item label="用量单位" required><el-input v-model="ruleForm.usage_unit" /></el-form-item>
        <el-form-item label="单价" required><el-input v-model="ruleForm.price_amount" /></el-form-item>
        <el-form-item label="币种"><el-input v-model="ruleForm.currency" /></el-form-item>
        <el-form-item label="计费模式"><el-input v-model="ruleForm.billing_mode" /></el-form-item>
        <el-form-item label="免费额度"><el-input v-model="ruleForm.free_quota" /></el-form-item>
        <el-form-item label="状态">
          <el-select v-model="ruleForm.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="ruleDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingRule" @click="saveRule">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.product-page { padding: 0; }
.page-header,
.filter-card,
.tab-toolbar,
.action-grid {
  display: flex;
  align-items: center;
  gap: 12px;
}
.page-header {
  justify-content: space-between;
  margin-bottom: 16px;
}
.page-title-text {
  margin: 0;
  color: var(--mc-text);
  font-size: 18px;
  font-weight: 700;
}
.page-subtitle {
  margin: 4px 0 0;
  color: var(--mc-text-muted);
  font-size: 12px;
}
.filter-card {
  margin-bottom: 14px;
  padding: 14px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: var(--mc-surface);
}
.filter-card .el-input,
.filter-card .el-select {
  width: 180px;
}
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.tab-toolbar {
  justify-content: flex-end;
  margin-bottom: 12px;
}
.action-grid {
  align-items: stretch;
}
.config-card {
  flex: 1;
  min-height: 150px;
  padding: 18px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: rgba(15, 23, 42, 0.42);
}
.config-card h4 {
  margin: 0 0 8px;
  color: var(--mc-text);
}
.config-card p,
.dialog-tip {
  color: var(--mc-text-muted);
  font-size: 12px;
  line-height: 1.6;
}
.dialog-tip {
  margin-top: 10px;
}
@media (max-width: 900px) {
  .page-header,
  .filter-card,
  .action-grid {
    align-items: stretch;
    flex-direction: column;
  }
  .filter-card .el-input,
  .filter-card .el-select {
    width: 100%;
  }
}
</style>
