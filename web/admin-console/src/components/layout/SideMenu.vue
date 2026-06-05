<template>
  <!-- 侧边导航菜单 -->
  <el-menu
    :default-active="activeMenu"
    :collapse="collapsed"
    class="side-menu"
    background-color="transparent"
    text-color="#94A3B8"
    active-text-color="#6366F1"
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
      <el-menu-item index="/roles">
        <el-icon><Medal /></el-icon>
        <template #title>角色管理</template>
      </el-menu-item>
      <el-menu-item index="/permissions">
        <el-icon><Key /></el-icon>
        <template #title>权限列表</template>
      </el-menu-item>
    </el-sub-menu>

    <el-menu-item index="/identity">
      <el-icon><Stamp /></el-icon>
      <template #title>实名审核</template>
    </el-menu-item>

    <el-menu-item index="/products">
      <el-icon><Goods /></el-icon>
      <template #title>商品管理</template>
    </el-menu-item>

    <el-menu-item index="/orders">
      <el-icon><List /></el-icon>
      <template #title>订单管理</template>
    </el-menu-item>

    <el-menu-item index="/transactions">
      <el-icon><Wallet /></el-icon>
      <template #title>钱包流水</template>
    </el-menu-item>

    <el-menu-item index="/assets">
      <el-icon><Box /></el-icon>
      <template #title>用户资产</template>
    </el-menu-item>

    <el-menu-item index="/announcements">
      <el-icon><Bell /></el-icon>
      <template #title>公告管理</template>
    </el-menu-item>
  </el-menu>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

defineProps<{
  collapsed: boolean
}>()

const route = useRoute()
// 当前激活菜单项
const activeMenu = computed(() => route.path)
</script>

<style scoped>
.side-menu {
  height: 100%;
  border-right: none;
}

.brand-area {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-bottom: 1px solid rgba(99, 102, 241, 0.2);
  margin-bottom: 8px;
}

.brand-name {
  font-size: 20px;
  font-weight: 700;
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  letter-spacing: 2px;
}

.brand-icon {
  font-size: 22px;
  font-weight: 700;
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}
</style>
