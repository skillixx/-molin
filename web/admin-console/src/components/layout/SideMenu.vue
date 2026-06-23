<template>
  <!-- 侧边导航菜单 -->
  <el-menu
    :default-active="activeMenu"
    :collapse="collapsed"
    class="side-menu"
    background-color="transparent"
    text-color="#8fa3bd"
    active-text-color="#38bdf8"
    router
  >
    <!-- 品牌 Logo 区域 -->
    <div class="brand-area">
      <span v-if="!collapsed" class="brand-name">墨灵</span>
      <span v-else class="brand-icon">M</span>
    </div>

    <el-menu-item index="/dashboard">
      <el-icon><Odometer /></el-icon>
      <template #title>仪表盘</template>
    </el-menu-item>

    <el-sub-menu index="iam">
      <template #title>
        <el-icon><Setting /></el-icon>
        <span>权限管理</span>
      </template>
      <el-menu-item index="/users">
        <el-icon><User /></el-icon>
        <template #title>用户管理</template>
      </el-menu-item>
      <el-menu-item v-if="can('role:manage')" index="/roles">
        <el-icon><Medal /></el-icon>
        <template #title>角色管理</template>
      </el-menu-item>
      <el-menu-item v-if="can('role:manage')" index="/permissions">
        <el-icon><Key /></el-icon>
        <template #title>权限列表</template>
      </el-menu-item>
    </el-sub-menu>

    <el-sub-menu v-if="can('group:manage')" index="groups">
      <template #title>
        <el-icon><Connection /></el-icon>
        <span>用户分组</span>
      </template>
      <el-menu-item index="/groups">
        <el-icon><Collection /></el-icon>
        <template #title>分组列表</template>
      </el-menu-item>
    </el-sub-menu>

    <el-menu-item v-if="can('identity:review')" index="/identity">
      <el-icon><Stamp /></el-icon>
      <template #title>实名审核</template>
    </el-menu-item>

    <el-menu-item v-if="can('audit:read')" index="/audit-logs">
      <el-icon><DocumentChecked /></el-icon>
      <template #title>审计日志</template>
    </el-menu-item>

    <el-sub-menu v-if="can('product:view')" index="product">
      <template #title>
        <el-icon><Goods /></el-icon>
        <span>商品与计费</span>
      </template>
      <el-menu-item index="/products">
        <el-icon><Goods /></el-icon>
        <template #title>商品管理</template>
      </el-menu-item>
    </el-sub-menu>

    <el-menu-item v-if="can('order:list')" index="/orders">
      <el-icon><List /></el-icon>
      <template #title>订单管理</template>
    </el-menu-item>

    <el-sub-menu v-if="can('wallet:view')" index="wallet">
      <template #title>
        <el-icon><Wallet /></el-icon>
        <span>钱包财务</span>
      </template>
      <el-menu-item index="/transactions">
        <el-icon><Wallet /></el-icon>
        <template #title>钱包中心</template>
      </el-menu-item>
      <el-menu-item index="/consumption-records">
        <el-icon><Tickets /></el-icon>
        <template #title>消费记录</template>
      </el-menu-item>
    </el-sub-menu>

    <el-menu-item v-if="can('asset:view')" index="/assets">
      <el-icon><Box /></el-icon>
      <template #title>用户资产</template>
    </el-menu-item>

    <el-sub-menu v-if="can('membership:view')" index="membership">
      <template #title>
        <el-icon><Medal /></el-icon>
        <span>会员管理</span>
      </template>
      <el-menu-item index="/memberships">
        <el-icon><Collection /></el-icon>
        <template #title>会员配置</template>
      </el-menu-item>
    </el-sub-menu>

    <el-sub-menu v-if="can('app:manage')" index="apps">
      <template #title>
        <el-icon><Box /></el-icon>
        <span>应用市场</span>
      </template>
      <el-menu-item index="/apps">
        <el-icon><Goods /></el-icon>
        <template #title>应用管理</template>
      </el-menu-item>
    </el-sub-menu>

    <el-sub-menu v-if="can('token:manage')" index="token">
      <template #title>
        <el-icon><Cpu /></el-icon>
        <span>Token 网关</span>
      </template>
      <el-menu-item index="/token/channels">
        <el-icon><Connection /></el-icon>
        <template #title>渠道管理</template>
      </el-menu-item>
      <el-menu-item index="/token/models">
        <el-icon><MagicStick /></el-icon>
        <template #title>模型目录</template>
      </el-menu-item>
      <el-menu-item index="/token/usage">
        <el-icon><Tickets /></el-icon>
        <template #title>用量统计</template>
      </el-menu-item>
    </el-sub-menu>

    <el-sub-menu v-if="can('agent:manage') || can('skill:manage') || can('plugin:manage')" index="workbench">
      <template #title>
        <el-icon><MagicStick /></el-icon>
        <span>Agent 工作台</span>
      </template>
      <el-menu-item index="/workbench/config">
        <el-icon><Collection /></el-icon>
        <template #title>配置管理</template>
      </el-menu-item>
    </el-sub-menu>

    <el-sub-menu v-if="can('content:manage')" index="content">
      <template #title>
        <el-icon><Bell /></el-icon>
        <span>内容中心</span>
      </template>
      <el-menu-item index="/announcements">
        <el-icon><DocumentChecked /></el-icon>
        <template #title>公告与帮助</template>
      </el-menu-item>
    </el-sub-menu>
  </el-menu>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

defineProps<{
  collapsed: boolean
}>()

const route = useRoute()
const auth = useAuthStore()
// 动态详情页归属到对应的固定菜单入口，保证侧边栏高亮稳定。
const activeMenu = computed(() => {
  if (route.path.startsWith('/groups/')) return '/groups'
  return route.path
})
const can = (code: string) => auth.hasPermission(code)
</script>

<style scoped>
.side-menu {
  height: 100%;
  border-right: none;
  overflow-x: hidden;
  overflow-y: auto;
  padding-bottom: 14px;
  scrollbar-width: thin;
  scrollbar-color: rgba(56, 189, 248, 0.36) transparent;
}

.side-menu::-webkit-scrollbar {
  width: 6px;
}

.side-menu::-webkit-scrollbar-thumb {
  background: rgba(56, 189, 248, 0.36);
  border-radius: 999px;
}

.side-menu::-webkit-scrollbar-track {
  background: transparent;
}

.brand-area {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-bottom: 1px solid var(--mc-border);
  margin-bottom: 8px;
}

.brand-name {
  font-size: 20px;
  font-weight: 700;
  background: linear-gradient(135deg, var(--mc-primary), var(--mc-accent));
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  letter-spacing: 2px;
}

.brand-icon {
  font-size: 22px;
  font-weight: 700;
  background: linear-gradient(135deg, var(--mc-primary), var(--mc-accent));
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

:deep(.el-menu-item),
:deep(.el-sub-menu__title) {
  height: 42px;
  margin: 2px 10px;
  border-radius: 7px;
}

:deep(.el-menu-item:hover),
:deep(.el-sub-menu__title:hover) {
  background: rgba(56, 189, 248, 0.08) !important;
  color: var(--mc-text) !important;
}

:deep(.el-menu-item.is-active) {
  background: rgba(56, 189, 248, 0.13) !important;
  color: var(--mc-primary) !important;
  box-shadow: inset 3px 0 0 var(--mc-primary);
}
</style>
