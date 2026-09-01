package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
	"net/http/httptest"
)

type videoSDKCase struct {
	CompletedVideoID string         `json:"completed_video_id"`
	RequestID        string         `json:"request_id"`
	MediaSizeBytes   uint64         `json:"media_size_bytes"`
	MediaSHA256      string         `json:"media_sha256"`
	BillingFacts     map[string]any `json:"billing_facts"`
}

func videoSDKCompletedCase(t *testing.T, f service.VideoContentHTTPFixture, taskID string) videoSDKCase {
	t.Helper()
	var task model.AIImageTask
	if err := f.DB.Where("public_id=?", taskID).Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	var request model.VideoBillingRequest
	if err := f.DB.Where("request_id=?", task.RequestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	var quote model.AIGatewayQuote
	if err := f.DB.First(&quote, task.QuoteID).Error; err != nil {
		t.Fatal(err)
	}
	var asset model.AIImageAsset
	if err := f.DB.Where("task_id=? AND asset_role='content'", task.ID).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if asset.SizeBytes == nil || asset.SHA256 == nil || request.SettledAmount == nil || request.BillingStatus != model.AIBillingSettled {
		t.Fatal("SDK完成夹具缺少媒体或结算事实")
	}
	return videoSDKCase{CompletedVideoID: task.PublicID, RequestID: task.RequestID, MediaSizeBytes: *asset.SizeBytes, MediaSHA256: *asset.SHA256, BillingFacts: map[string]any{"request_id": task.RequestID, "quote_id": quote.PublicID, "billing_status": request.BillingStatus, "settled_amount": request.SettledAmount.StringFixed(8)}}
}

func advanceVideoSDKQueuedTasks(t *testing.T, f service.VideoContentHTTPFixture) {
	t.Helper()
	keyID := f.ProjectID
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &keyID}
	var ids []string
	if err := f.DB.Model(&model.AIImageTask{}).Where("user_id=? AND project_id=? AND api_key_id=? AND capability=? AND status IN ?", f.ProjectID, f.ProjectID, f.ProjectID, model.AIVideoCapability, []string{model.AIImageTaskReserved, model.AIImageTaskQueued}).Pluck("public_id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		repo := repository.NewVideoTaskRepository(f.DB)
		task, err := repo.FindForOwner(t.Context(), id, owner)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == model.AIImageTaskReserved {
			task, err = repo.TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: id, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: task.RequestID + "_sdk_queued", Source: "worker", Now: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := repo.TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: id, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskSubmitting, Progress: 20, EventID: task.RequestID + "_sdk_submitting", Source: "worker", Now: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
}

func runVideoSDKClient(t *testing.T, command *exec.Cmd) map[string]any {
	t.Helper()
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("锁定SDK客户端失败: class=%T output=%s", err, strings.TrimSpace(output.String()))
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var report map[string]any
	if len(lines) == 0 || json.Unmarshal([]byte(lines[len(lines)-1]), &report) != nil || report["http_contract"] != "PASS" {
		t.Fatalf("锁定SDK未返回PASS: %s", strings.TrimSpace(output.String()))
	}
	return report
}

func TestVideoG6LockedSDKHTTPMySQL(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G6_SDK_EXECUTE") != "YES" {
		t.Skip("未启用VID-G6锁定SDK真实HTTP")
	}
	f := service.NewVideoContentHTTPFixture(t, true)
	inlineStore := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithInlineUploads(inlineStore)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-sdk-rights-accept-0001", RequestID: "g6-sdk-rights-request-0001"}); err != nil {
		t.Fatal(err)
	}
	pythonTask := f.CreateCompletedForKey(f.ProjectID)
	typescriptTask := f.CreateCompletedForKey(f.ProjectID)
	pythonCase := videoSDKCompletedCase(t, f, pythonTask)
	typescriptCase := videoSDKCompletedCase(t, f, typescriptTask)
	key := f.SyntheticSDKKey()

	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, app, f.Keys, true, f.JWT)
	server := httptest.NewServer(mux)
	defer server.Close()

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sdkDir := filepath.Join(repoRoot, "tests", "api", "video-gateway-vid-g6-sdk")
	temporary := t.TempDir()
	referencePath := filepath.Join(temporary, "reference.local.png")
	if err := os.WriteFile(referencePath, f.Reference, 0o600); err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(temporary, "fixture.local.json")
	fixture := map[string]any{"purpose": "isolated_synthetic_fixture", "disposable": true, "origin": server.URL, "run_id": fmt.Sprintf("g6sdk%d", f.ProjectID), "model": f.Model, "reference_image": filepath.Base(referencePath), "python": pythonCase, "typescript": typescriptCase}
	raw, _ := json.Marshal(fixture)
	if err := os.WriteFile(fixturePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	python := filepath.Join(sdkDir, ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(sdkDir, ".venv", "Scripts", "python.exe")
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatal("锁定Python虚拟环境不存在")
	}
	environment := append(os.Environ(), "VID_G6_SDK_APPROVED=ISOLATED_SYNTHETIC_ONLY", "VID_G6_SDK_SK="+key)
	pythonCommand := exec.Command(python, filepath.Join(sdkDir, "sdk_python.py"), "--execute", "--fixture", fixturePath)
	pythonCommand.Dir, pythonCommand.Env = sdkDir, environment
	pythonReport := runVideoSDKClient(t, pythonCommand)
	advanceVideoSDKQueuedTasks(t, f)

	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node24不存在")
	}
	nodeCommand := exec.Command(node, filepath.Join(sdkDir, "sdk_typescript.ts"), "--execute", "--fixture", fixturePath)
	nodeCommand.Dir, nodeCommand.Env = sdkDir, environment
	typescriptReport := runVideoSDKClient(t, nodeCommand)
	if len(pythonReport["cases"].([]any)) != 5 || len(typescriptReport["cases"].([]any)) != 5 {
		t.Fatal("双SDK必须完整执行五组HTTP合同")
	}
}
