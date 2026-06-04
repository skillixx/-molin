# Identity 模块 — 后端 A 负责

## 职责边界

只负责：实名认证提交、实名认证审核、状态写回 users 表、审核日志。

不负责：用户登录（auth 模块）、权限控制（iam 模块）。

---

## Week 1 任务清单

```text
□ model/identity.go           — identity_verifications, identity_verification_logs
□ repository/identity_repo.go — 提交、查询、更新状态
□ service/identity_service.go — 提交认证、审核审批/拒绝
□ handler/identity_handler.go
□ dto/identity_dto.go
□ route.go

Migration：
□ server/migrations/000003_create_identity_tables.up.sql
```

---

## 核心规则

- 身份证号**严禁明文存储**，只存 HMAC-SHA256 hash 和 masked 值。
- masked 格式：前 6 位 + `********` + 后 4 位（如 `330102********1234`）。
- 查重：新提交时先用 HMAC hash 检查是否已绑定其他用户。
- 审核通过后：更新 `identity_verifications.status = verified`，同步写 `users.real_name_status = verified`。

## HMAC 身份证号处理

```go
// service/identity_service.go

import "molin/server/pkg/crypto"

func hashIDCard(idCardNo, secret string) string {
    return crypto.HMAC256(idCardNo, secret) // 使用环境变量 ID_CARD_HMAC_SECRET
}

func maskIDCard(idCardNo string) string {
    if len(idCardNo) != 18 {
        return "**"
    }
    return idCardNo[:6] + "********" + idCardNo[14:]
}
```

## 提交认证流程

```go
func (s *IdentityService) Submit(ctx context.Context, userID uint64, req dto.SubmitReq) error {
    // 1. 不允许重复提交（pending/verified 状态时）
    existing, _ := s.repo.FindActiveByUser(ctx, userID)
    if existing != nil && (existing.Status == "pending" || existing.Status == "verified") {
        return ErrAlreadySubmitted
    }
    // 2. 计算 HMAC，检查身份证号是否已被其他用户绑定
    hmacHash := hashIDCard(req.IDCardNo, s.cfg.IDCardHMACSecret)
    if conflict, _ := s.repo.ExistsByHMAC(ctx, hmacHash, userID); conflict {
        return ErrIDCardAlreadyBound
    }
    // 3. 写入（不存明文）
    return s.repo.Create(ctx, &model.IdentityVerification{
        UserID:          userID,
        RealName:        req.RealName,
        IDCardNoHash:    hmacHash,
        IDCardNoMasked:  maskIDCard(req.IDCardNo),
        AttachmentsJSON: req.Attachments,
        Status:          "pending",
    })
}
```

## 审核流程

```go
func (s *IdentityService) Review(ctx context.Context, verificationID, operatorID uint64, approve bool, reason string) error {
    verification, _ := s.repo.FindByID(ctx, verificationID)
    newStatus := "rejected"
    if approve {
        newStatus = "verified"
    }
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 更新认证状态
        s.repo.UpdateStatus(tx, verificationID, newStatus, reason)
        // 写审核日志
        s.repo.CreateLog(tx, &model.IdentityVerificationLog{
            VerificationID: verificationID,
            UserID:         verification.UserID,
            Action:         newStatus,
            OperatorID:     &operatorID,
            Remark:         reason,
        })
        // 审核通过：同步更新用户实名状态
        if approve {
            s.userRepo.UpdateRealNameStatus(tx, verification.UserID, "verified", verification.RealName)
        }
        return nil
    })
}
```

---

## Migration

### server/migrations/000003_create_identity_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS identity_verifications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  real_name VARCHAR(128) NOT NULL,
  id_card_no_hash VARCHAR(128) NOT NULL,
  id_card_no_masked VARCHAR(64) NOT NULL,
  verification_type VARCHAR(32) NOT NULL DEFAULT 'id_card',
  attachments_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(512) NULL,
  submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  verified_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_user_id (user_id),
  KEY idx_identity_hash (id_card_no_hash),
  KEY idx_identity_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS identity_verification_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  verification_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_logs_verification_id (verification_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 接口清单

```text
POST   /api/identity/verifications                              -- 用户提交实名认证
GET    /api/identity/verifications/me                          -- 查当前用户认证状态
GET    /api/admin/identity-verifications                       -- 管理员查列表（需 identity:review 权限）
GET    /api/admin/identity-verifications/:id                   -- 管理员查详情
PATCH  /api/admin/identity-verifications/:id/review            -- 管理员审核
```
