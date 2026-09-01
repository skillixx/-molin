package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	authrepo "molin/server/internal/modules/auth/repository"
	authservice "molin/server/internal/modules/auth/service"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

// 仅在测试构建中导出夹具；生产二进制不会包含合成账户、凭据或暂停钩子。
type VideoImportHTTPFixture struct {
	TokenForUser                                  func(uint64) string
	RevokeToken                                   func()
	FailJWTRevocations                            func()
	ShortJWT                                      func(int64) (string, time.Time)
	DB                                            *gorm.DB
	App                                           *VideoHTTPService
	Keys                                          *authservice.APIKeyService
	JWT                                           *VideoJWTAuthenticator
	Key, OtherKey, Token, SourceID, Model, Policy string
	ProjectID                                     uint64
	PauseRead                                     func() (<-chan struct{}, func())
	ProviderCalls                                 func() int32
	SourcePresent                                 func() bool
	InputDeleted                                  func(string) bool
	InputPresent                                  func(string) bool
	WithUploads                                   func(VideoUploadStore) *VideoHTTPService
	WithUploadsOnDB                               func(*gorm.DB, VideoUploadStore) *VideoHTTPService
	WithDB                                        func(*gorm.DB) *VideoHTTPService
	Reference                                     []byte
	SyntheticSDKKey                               func() string
	OtherKeyID                                    uint64
}

// 仅测试构建替换SQL连接池边界，以观察真实COMMIT成功但确认丢失；业务服务和仓储不被替换。
func (f VideoImportHTTPFixture) UseApplicationDB(db *gorm.DB) func() {
	previous := f.App.db
	f.App.db = db
	return func() { f.App.db = previous }
}

// UseVideoHTTPBillingFault 仅供测试在inline完成后注入生成事务故障；生产构建不包含该入口。
func UseVideoHTTPBillingFault(app *VideoHTTPService, fault func(string) error) func() {
	previous := app.billing.fault
	app.billing.fault = fault
	return func() { app.billing.fault = previous }
}

// AddVideoModelPublicationMappingForTest 仅给测试增加第二个合成模型映射；生产配置仍在构造时深拷贝冻结。
func AddVideoModelPublicationMappingForTest(admin *VideoAdminService, code, providerModel string) bool {
	if admin == nil || admin.modelPublishing == nil || admin.modelPublishing.Models == nil || code == "" || providerModel == "" {
		return false
	}
	admin.modelPublishing.Models[code] = providerModel
	return true
}

// 管理员也使用原G5共享夹具编号分配器，不能让AUTO_INCREMENT占用下一套夹具的显式用户编号。
func NextVideoFixtureUserID() uint64 { return uint64(990000) + videoG5FixtureSequence.Add(1) }

// 真实HTTP模型使用自增ID，推进合成编号以免后续显式模型ID冲突。
func ReserveVideoFixtureIDsThrough(id uint64) {
	if id < 990000 {
		return
	}
	target := id - 990000
	for {
		old := videoG5FixtureSequence.Load()
		if old >= target || videoG5FixtureSequence.CompareAndSwap(old, target) {
			return
		}
	}
}

// 高位辅助Key必须同步推进共享编号，完整套件中的下一套用户、Project与Key才能安全复用同一临时库。
func TestVideoG6FixtureSequenceTracksAuxiliaryKeyMySQL(t *testing.T) {
	fixture := NewVideoImportHTTPFixture(t)
	occupiedThrough := fixture.ProjectID + 9000000
	nextID := NextVideoFixtureUserID()
	if nextID <= occupiedThrough {
		t.Fatalf("后续夹具ID必须越过已占用高位Key：occupied=%d next=%d", occupiedThrough, nextID)
	}
	if err := fixture.DB.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(?,?,?,'g6-seq',?,'编号回归Key','postpaid','allowlist','active',1)", nextID, fixture.ProjectID, fixture.ProjectID, fmt.Sprintf("fixture-sequence-%d", nextID)).Error; err != nil {
		t.Fatalf("后续显式Key必须能写入同一临时库：%v", err)
	}
}

func NewVideoImportHTTPFixture(t *testing.T) VideoImportHTTPFixture {
	t.Helper()
	ctx := context.Background()
	v := newVideoG6I2VFixture(t)
	db := v.legacy.db
	id := v.command.Caller.UserID
	now := time.Now().UTC()
	ensureVideoG6ImageBase(t, db, now)
	if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(?,?,?,?)", id, id, id, imageG5ModelCode).Error; err != nil {
		t.Fatal(err)
	}
	img := seedImageBillingRequestForExistingOwner(t, db, id, fmt.Sprintf("g6-import-http-%d", id), 1, now)
	provider := &videoSourceImageFake{raw: v.reference.Bytes}
	adapter, err := NewAttemptRecordingImageAdapter(provider, db)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imagegateway.NewImageProcessor(imagegateway.ImageProcessingLimits{MaxSourceBytes: 10 << 20, MaxNormalizedBytes: 10 << 20, MaxPixels: 16777216, MaxWidth: 4096, MaxHeight: 4096, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := imagegateway.NewImageGateway(adapter, imagegateway.NewFakeModerationAdapter(imagegateway.FakeModerationAllow), processor, img.store, repository.NewImageObjectCleanupRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	img.service.gateway = gateway
	mustReserveImageG5(t, img)
	result, err := img.service.Execute(ctx, img.requestID, img.command)
	if err != nil || result.BillingStatus != "settled" {
		t.Fatal("HTTP来源必须实际结算")
	}
	report, err := img.service.ReconcileRequest(ctx, img.requestID)
	if err != nil || !report.ZeroDifference() {
		t.Fatal("HTTP来源图片对账必须零差异")
	}
	store := &videoImportMemoryStore{source: img.store, objects: map[VideoImportObject][]byte{}, tombstones: map[VideoImportObject]bool{}}
	app, err := NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: v.legacy.service.quoteSecret, PromptSecret: v.legacy.service.promptSecret, IntentSecret: v.legacy.service.intentSecret, Protector: v.legacy.service.protector, Safety: v.legacy.service.safety}, VideoHTTPOptions{Imports: &VideoInputImportOptions{Store: store, NormalizedBucket: "g6-http-import", ModerationPolicyVersion: "g6-http-import-v1", MaxUserReservedBytes: 128 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	secret := func() string {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	hmacSecret, jwtSecret := secret(), secret()
	key, other := "sk-molin-"+secret(), "sk-molin-"+secret()
	if err := db.Exec("UPDATE api_keys SET key_hash=? WHERE id=?", crypto.HMAC256(key, hmacSecret), id).Error; err != nil {
		t.Fatal(err)
	}
	otherID := id + 9000000
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(?,?,?,'g6',?,'另一合成Key','postpaid','allowlist','active',1)", otherID, id, id, crypto.HMAC256(other, hmacSecret)).Error; err != nil {
		t.Fatal(err)
	}
	// 高位合成Key会推进MySQL的AUTO_INCREMENT；同步推进共享夹具序列，避免完整套件中的后续显式ID与其碰撞。
	ReserveVideoFixtureIDsThrough(otherID)
	for _, code := range []string{imageG5ModelCode, v.command.Model} {
		if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(?,?,?,?)", otherID, id, id, code).Error; err != nil {
			t.Fatal(err)
		}
	}
	revocations := &videoTestRevocations{revoked: map[string]bool{}}
	jwt, err := NewVideoJWTAuthenticator(db, jwtSecret, revocations)
	if err != nil {
		t.Fatal(err)
	}
	token, err := pkgjwt.Generate(id, "", jwtSecret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	pause := func() (<-chan struct{}, func()) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once, releaseOnce sync.Once
		store.Lock()
		store.afterRead = func() { once.Do(func() { close(entered); <-release }) }
		store.Unlock()
		return entered, func() { releaseOnce.Do(func() { close(release) }) }
	}
	sourceID := imageAssetPublicID(img.requestID, 0, "primary_output")
	inspectSource := func() bool {
		var location struct{ Bucket, ObjectKey, Hash string }
		if err := db.Table("ai_gateway_assets").Select("bucket,object_key,sha256 AS hash").Where("public_id=? AND user_id=?", sourceID, id).Take(&location).Error; err != nil {
			return false
		}
		body, err := img.store.Get(context.Background(), imagegateway.ObjectRef{Bucket: location.Bucket, Key: location.ObjectKey})
		return err == nil && len(body) > 0 && videoPayloadSHA256(body) == location.Hash
	}
	inspectInput := func(publicID string) bool {
		var location struct{ Bucket, ObjectKey string }
		if err := db.Table("ai_gateway_input_assets").Select("bucket,object_key").Where("public_id=? AND user_id=?", publicID, id).Take(&location).Error; err != nil {
			return false
		}
		store.Lock()
		defer store.Unlock()
		target := VideoImportObject{location.Bucket, location.ObjectKey}
		_, exists := store.objects[target]
		return store.tombstones[target] && !exists
	}
	inspectPresent := func(publicID string) bool {
		var location struct{ Bucket, ObjectKey, Hash string }
		if err := db.Table("ai_gateway_input_assets").Select("bucket,object_key,normalized_sha256 AS hash").Where("public_id=? AND user_id=?", publicID, id).Take(&location).Error; err != nil {
			return false
		}
		store.Lock()
		defer store.Unlock()
		data, exists := store.objects[VideoImportObject{location.Bucket, location.ObjectKey}]
		return exists && len(data) > 0 && videoPayloadSHA256(data) == location.Hash
	}
	return VideoImportHTTPFixture{TokenForUser: func(userID uint64) string {
		raw, err := pkgjwt.Generate(userID, "", jwtSecret, 3600)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}, FailJWTRevocations: func() { revocations.mu.Lock(); defer revocations.mu.Unlock(); revocations.unavailable = true }, ShortJWT: func(seconds int64) (string, time.Time) {
		raw, err := pkgjwt.Generate(id, "", jwtSecret, seconds)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := pkgjwt.Parse(raw, jwtSecret)
		if err != nil {
			t.Fatal(err)
		}
		return raw, claims.ExpiresAt.Time
	}, RevokeToken: func() {
		revocations.mu.Lock()
		defer revocations.mu.Unlock()
		revocations.revoked[crypto.SHA256Hex(token)] = true
	}, DB: db, App: app, Keys: authservice.NewAPIKeyService(authrepo.NewAPIKeyRepository(db), hmacSecret, nil), JWT: jwt, Key: key, OtherKey: other, Token: token, SourceID: sourceID, Model: v.command.Model, Policy: v.policyVersion, ProjectID: id, PauseRead: pause, ProviderCalls: provider.calls.Load, SourcePresent: inspectSource, InputDeleted: inspectInput, InputPresent: inspectPresent, Reference: append([]byte(nil), v.reference.Bytes...), OtherKeyID: otherID, SyntheticSDKKey: func() string {
		raw := "sk-molin-g6-fixture-" + secret()
		if err := db.Exec("UPDATE api_keys SET key_hash=? WHERE id=?", crypto.HMAC256(raw, hmacSecret), id).Error; err != nil {
			t.Fatal(err)
		}
		return raw
	}, WithUploads: func(uploadStore VideoUploadStore) *VideoHTTPService {
		inlineApp, inlineErr := NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: v.legacy.service.quoteSecret, PromptSecret: v.legacy.service.promptSecret, IntentSecret: v.legacy.service.intentSecret, Protector: v.legacy.service.protector, Safety: v.legacy.service.safety}, VideoHTTPOptions{Uploads: &VideoUploadOptions{Store: uploadStore, SourceBucket: "g6-inline-source", NormalizedBucket: "g6-inline-normalized", ModerationPolicyVersion: "g6-inline-v1", MaxUserReservedBytes: 128 << 20}})
		if inlineErr != nil {
			t.Fatal(inlineErr)
		}
		return inlineApp
	}, WithUploadsOnDB: func(targetDB *gorm.DB, uploadStore VideoUploadStore) *VideoHTTPService {
		inlineApp, inlineErr := NewVideoHTTPService(targetDB, VideoBillingOptions{QuoteSecret: v.legacy.service.quoteSecret, PromptSecret: v.legacy.service.promptSecret, IntentSecret: v.legacy.service.intentSecret, Protector: v.legacy.service.protector, Safety: v.legacy.service.safety}, VideoHTTPOptions{Uploads: &VideoUploadOptions{Store: uploadStore, SourceBucket: "g6-inline-source", NormalizedBucket: "g6-inline-normalized", ModerationPolicyVersion: "g6-inline-v1", MaxUserReservedBytes: 128 << 20}})
		if inlineErr != nil {
			t.Fatal(inlineErr)
		}
		return inlineApp
	}, WithDB: func(targetDB *gorm.DB) *VideoHTTPService {
		targetApp, targetErr := NewVideoHTTPService(targetDB, VideoBillingOptions{QuoteSecret: v.legacy.service.quoteSecret, PromptSecret: v.legacy.service.promptSecret, IntentSecret: v.legacy.service.intentSecret, Protector: v.legacy.service.protector, Safety: v.legacy.service.safety})
		if targetErr != nil {
			t.Fatal(targetErr)
		}
		return targetApp
	}}
}
