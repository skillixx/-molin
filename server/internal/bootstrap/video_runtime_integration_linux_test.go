//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/config"
	"molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7BootstrapClosedRuntimeMySQLRedisRabbitMinIO(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_RUNTIME_ISOLATED") != "YES" {
		t.Skip("VID-G7只允许隔离全运行时门禁执行")
	}
	db, err := gorm.Open(mysql.Open(os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MYSQL_DSN")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	minioEndpoint := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_ENDPOINT")
	internalURL, err := url.Parse("http://" + minioEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	publicProxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(internalURL))
	defer publicProxy.Close()
	providerServer := httptest.NewServer(http.NotFoundHandler())
	defer providerServer.Close()
	secretDir := t.TempDir()
	writeSecret := func(name, value string) string {
		t.Helper()
		path := filepath.Join(secretDir, name)
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	minioAccess := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_ACCESS")
	minioSecret := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_MINIO_SECRET")
	rabbitPassword := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_RABBIT_PASSWORD")
	redisPassword := os.Getenv("MOLIN_VIDEO_G7_RUNTIME_REDIS_PASSWORD")
	paths := config.VideoSecretPaths{
		RepositoryRoot: "/src",
		Quote:          writeSecret("quote", strings.Repeat("q", 32)), Payload: writeSecret("payload", strings.Repeat("p", 32)),
		Callback: writeSecret("callback", strings.Repeat("c", 32)), AdminReason: writeSecret("admin", strings.Repeat("a", 32)), Download: writeSecret("download", strings.Repeat("d", 32)),
		MinIOAccess: writeSecret("minio-access", minioAccess), MinIOSecret: writeSecret("minio-secret", minioSecret),
		RabbitPassword: writeSecret("rabbit-password", rabbitPassword), RedisPassword: writeSecret("redis-password", redisPassword), CapacityNonce: writeSecret("capacity", strings.Repeat("n", 32)),
	}
	cfg := config.Config{AppEnv: "test", JWTSecret: strings.Repeat("j", 32), AdminVerifyExpireHours: 24, VideoGateway: config.VideoGatewayConfig{
		Enabled: true, TrafficEnabled: false, LocalFakeTest: false, ExecutionDriver: "native_async", Secrets: paths,
		Infrastructure: config.VideoInfrastructureConfig{MinIOEndpoint: minioEndpoint, MinIOPublicUploadEndpoint: publicProxy.URL, FakeProviderEndpoint: providerServer.URL, RabbitEndpoint: "rabbit:5672", RabbitUser: "vidg7fake", RabbitVHost: "/", RedisAddr: "redis:6379", WorkerID: "runtime-bootstrap"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// 安装动作与普通启动分离；测试夹具先创建私有共享bucket，再验证build只读检查。
	installer, err := video.NewMinIOVideoObjectStore(video.MinIOVideoObjectStoreConfig{Endpoint: minioEndpoint, AccessKey: minioAccess, SecretKey: minioSecret, Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine", "ai-user-assets"}, TempDirectory: t.TempDir(), VerifyArchiveFence: service.NewVideoArchiveObjectFenceVerifier(db)})
	if err != nil || installer.EnsureBuckets(ctx) != nil {
		t.Fatalf("隔离MinIO安装失败: %v", err)
	}
	metrics := service.NewAIGatewayMetrics(service.NewAIGatewayDBGaugeCollector(db))
	runtime, closeRuntime, err := buildVideoRuntime(ctx, cfg, db, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	if runtime.UserApp == nil || runtime.AdminApp == nil || runtime.CallbackApp == nil || runtime.Outbox == nil || runtime.Consumer == nil || runtime.TaskHandler == nil || runtime.ObjectScanner == nil || runtime.OrphanCleanup == nil || runtime.MissingRepair == nil || runtime.InputCleanup == nil || runtime.OutputCleanup == nil {
		t.Fatal("关闭态运行时必须完整装配收口依赖")
	}
	runCtx, stop := context.WithCancel(context.Background())
	if err := runtime.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	defer stop()
	rabbitURL := fmt.Sprintf("amqp://vidg7fake:%s@rabbit:5672/", url.QueryEscape(rabbitPassword))
	opener, err := video.NewTaskConnectionOpener(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		connection, err := opener(ctx)
		if err == nil {
			channel, channelErr := connection.Channel()
			ready := channelErr == nil
			for _, queue := range []string{"molin.video.submit", "molin.video.poll", "molin.video.fetch"} {
				if ready {
					inspection, inspectErr := channel.QueueInspect(queue)
					ready = inspectErr == nil && inspection.Consumers == 2
				}
			}
			if channel != nil {
				_ = channel.Close()
			}
			_ = connection.Close()
			if ready {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("三个阶段必须各启动两个共享Worker")
		}
		time.Sleep(50 * time.Millisecond)
	}
	components := []string{"outbox", "orphan_cleanup", "missing_repair", "input_retention", "output_retention", "object_scanner", "consumer_submit", "consumer_poll", "consumer_fetch"}
	healthDeadline := time.Now().Add(15 * time.Second)
	for {
		health := runtime.HealthSnapshot()
		ready := true
		for _, component := range components {
			state, ok := health[component]
			ready = ready && ok && state.Up && !state.LastSuccessAt.IsZero()
		}
		if ready {
			break
		}
		if time.Now().After(healthDeadline) {
			t.Fatalf("运行组件未在期限内形成首次成功健康事实: %+v", health)
		}
		time.Sleep(50 * time.Millisecond)
	}
	metricsText, err := metrics.AIGatewayPrometheus(ctx)
	if err != nil {
		t.Fatalf("视频指标采集失败: %v", err)
	}
	for _, family := range []string{"molin_ai_gateway_video_tasks", "molin_ai_gateway_video_queue_depth", "molin_ai_gateway_video_capacity_leases", "molin_ai_gateway_video_unsettled_holds", "molin_ai_gateway_video_object_observations", "molin_ai_gateway_video_object_compensations", "molin_ai_gateway_video_component_up", "molin_ai_gateway_video_component_failures_total", "molin_ai_gateway_video_component_last_success_age_seconds", "molin_ai_gateway_video_object_bytes", "molin_ai_gateway_video_cleanup_failures"} {
		if !strings.Contains(metricsText, family) {
			t.Fatalf("缺少视频指标族: %s", family)
		}
	}
	for _, forbidden := range []string{"request_id=", "task_id=", "user_id=", "project_id=", "prompt=", "minio:9000", "rabbit:5672"} {
		if strings.Contains(strings.ToLower(metricsText), forbidden) {
			t.Fatalf("视频指标不得包含高基数或基础设施字段: %s", forbidden)
		}
	}
	mux := http.NewServeMux()
	token_gateway.RegisterVideoUserRoutes(mux, runtime.UserApp, nil, false)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/videos", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "video_gateway_traffic_closed") {
		t.Fatalf("模块装配但流量关闭必须返回稳定关闭态: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "minio") || strings.Contains(response.Body.String(), "rabbit") || strings.Contains(response.Body.String(), "redis") {
		t.Fatal("关闭态响应不得泄漏基础设施")
	}
	// 模块关闭时bootstrap不会调用任何RegisterVideo方法，因此相同路径保持404。
	closedMux := http.NewServeMux()
	closedResponse := httptest.NewRecorder()
	closedMux.ServeHTTP(closedResponse, httptest.NewRequest(http.MethodPost, "/v1/videos", nil))
	if closedResponse.Code != http.StatusNotFound {
		t.Fatal("模块关闭不得注册视频路由")
	}
	stop()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	if err := runtime.Shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("运行时必须等待全部Worker和后台任务退出: %v", err)
	}
	cancelShutdown()
	health := runtime.HealthSnapshot()
	for _, component := range components {
		if state, ok := health[component]; !ok || state.LastSuccessAt.IsZero() {
			t.Fatalf("运行时必须保留组件健康事实: component=%s state=%+v", component, state)
		}
	}
	redisClient := redis.NewClient(&redis.Options{Addr: "redis:6379", Password: redisPassword})
	defer redisClient.Close()
	runID, err := service.ReadVideoRedisRunID(ctx, redisClient)
	if err != nil {
		t.Fatalf("运行时结束前Redis身份应可读取: %v", err)
	}
	// Rabbit故障时MySQL与Redis指标仍必须可抓，并以component_up=0明确暴露故障。
	var guard struct{ CapacityEpoch uint64 }
	if err := db.Table("ai_video_queue_admission_guard").Select("capacity_epoch").Where("id=1").Take(&guard).Error; err != nil {
		t.Fatal(err)
	}
	metricRedis := redis.NewClient(&redis.Options{Addr: "redis:6379", Password: redisPassword, MaxRetries: -1, ContextTimeoutEnabled: true})
	defer metricRedis.Close()
	policy, _ := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	capacityStore, err := service.NewRedisVideoCapacityStore(metricRedis, guard.CapacityEpoch, policy)
	if err != nil {
		t.Fatal(err)
	}
	topology, _ := video.NewTaskTopology("molin.video")
	brokenRabbit := video.TaskConnectionOpener(func(context.Context) (*amqp.Connection, error) { return nil, errors.New("合成Rabbit故障") })
	partial, err := service.NewVideoRuntimeMetricsCollector(db, topology, brokenRabbit, capacityStore, runID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := partial.CollectVideoGauges(ctx, time.Now().UTC())
	if err != nil || snapshot.ComponentUp["mysql"] != 1 || snapshot.ComponentUp["redis"] != 1 || snapshot.ComponentUp["rabbitmq"] != 0 {
		t.Fatalf("单依赖故障不得抹掉其余视频指标: up=%+v err=%v", snapshot.ComponentUp, err)
	}
	_, _ = io.Copy(io.Discard, response.Result().Body)
}
