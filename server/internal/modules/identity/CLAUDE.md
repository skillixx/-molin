# Identity 模块 — 后端 A 负责

## 职责边界

只负责：实名认证提交、审核、状态同步回写到 users 表。

不负责：用户基础信息管理（auth 模块）、权限（iam 模块）。

## 需要创建的文件

```text
model/
  identity.go       -- identity_verifications, identity_verification_logs

repository/
  identity_repo.go  -- 实名记录 CRUD，含按用户查最新一条

service/
  identity_service.go   -- 提交认证、审核通过/拒绝、HMAC 处理

handler/
  identity_handler.go   -- 用户端 + 管理端

dto/
  identity_dto.go

route.go
```

## 安全要求（必须严格执行）

```go
func (s *IdentityService) Submit(ctx context.Context, userID uint64, realName string, idCardNo string) error {
    // 1. idCardNo 严禁入库（函数执行完后不得保留明文）
    hmac := crypto.HMAC256(idCardNo, os.Getenv("ID_CARD_HMAC_SECRET"))
    masked := maskIDCard(idCardNo)  // 前6位 + ****** + 后4位

    // 2. 检查该身份证是否已被其他账号实名（用 hmac 查重）
    if s.repo.ExistsByHMAC(hmac) {
        return ErrIDCardAlreadyUsed
    }

    // 3. 写入实名记录
    return s.repo.Create(&IdentityVerification{
        UserID:         userID,
        RealName:       realName,
        IDCardNoHMAC:   hmac,
        IDCardNoMasked: masked,
        Status:         "pending",
    })
}

func maskIDCard(idCard string) string {
    if len(idCard) != 18 { return "******" }
    return idCard[:6] + "********" + idCard[14:]
}
```

## 审核流程

```go
func (s *IdentityService) Review(ctx context.Context, verificationID uint64, action string, reason string, operatorID uint64) error {
    // 1. 更新 identity_verifications.status
    // 2. 写入 identity_verification_logs
    // 3. 如果 action = approve，同步更新 users.real_name_status = verified
    // 4. 写入 audit_logs（调用 audit_service）
}
```

## 接口清单

```text
POST /api/identity/verifications
GET  /api/identity/verifications/latest
GET  /api/admin/identity-verifications
GET  /api/admin/identity-verifications/:id
PATCH /api/admin/identity-verifications/:id/review
GET  /api/admin/users/:id/identity
```

## 依赖关系

- 依赖 `server/pkg/crypto/hmac.go` — HMAC-SHA256
- 依赖 `modules/auth/repository` — 审核通过后更新 users.real_name_status
- 依赖 `modules/audit/service` — 审核操作写审计日志
