package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacityMultiProcess2MySQLRedis(t *testing.T) { runVideoCapacityProcesses(t, 2) }
func TestVideoG7CapacityMultiProcess4MySQLRedis(t *testing.T) { runVideoCapacityProcesses(t, 4) }
func TestVideoG7CapacityMultiProcess8MySQLRedis(t *testing.T) { runVideoCapacityProcesses(t, 8) }

func runVideoCapacityProcesses(t *testing.T, processes int) {
	t.Helper()
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixtures := make([]videoCapacityQueuedFixture, 0, processes)
	for index := 0; index < processes; index++ {
		operation := "text_to_video"
		if index%2 == 1 {
			operation = "image_to_video"
		}
		fixture := prepareVideoG7CapacityQueued(t, db, operation)
		if err := repository.NewVideoWorkerLeaseRepository(db).Release(ctx, fixture.proof); err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture)
	}
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, fmt.Sprintf("multi-process-%d", processes), hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil || prepared.Summary().Queued != processes {
		t.Fatalf("多进程前必须恢复全部queued: %+v err=%v", prepared.Summary(), err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type child struct {
		cmd *exec.Cmd
		out bytes.Buffer
	}
	children := make([]*child, 0, processes)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for index := range fixtures {
		fixture := fixtures[index]
		keyID := uint64(0)
		if fixture.base.owner.APIKeyID != nil {
			keyID = *fixture.base.owner.APIKeyID
		}
		item := &child{}
		item.cmd = exec.Command(executable, "-test.run=^TestVideoCapacityProcessHelper$", "-test.v")
		item.cmd.Env = append(os.Environ(),
			"MOLIN_VIDEO_G7_PROCESS_HELPER=YES",
			"MOLIN_VIDEO_G7_PROCESS_BARRIER="+listener.Addr().String(),
			"MOLIN_VIDEO_G7_PROCESS_TASK="+fixture.queued.TaskID,
			"MOLIN_VIDEO_G7_PROCESS_USER="+strconv.FormatUint(fixture.base.owner.UserID, 10),
			"MOLIN_VIDEO_G7_PROCESS_PROJECT="+strconv.FormatUint(fixture.base.owner.ProjectID, 10),
			"MOLIN_VIDEO_G7_PROCESS_KEY="+strconv.FormatUint(keyID, 10),
			"MOLIN_VIDEO_G7_PROCESS_INDEX="+strconv.Itoa(index),
		)
		item.cmd.Stdout, item.cmd.Stderr = &item.out, &item.out
		if err := item.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, item)
	}
	t.Cleanup(func() {
		for _, item := range children {
			if item.cmd.Process != nil && item.cmd.ProcessState == nil {
				_ = item.cmd.Process.Kill()
				_, _ = item.cmd.Process.Wait()
			}
		}
	})
	connections := make([]net.Conn, 0, processes)
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Second))
	for len(connections) < processes {
		connection, err := listener.Accept()
		if err != nil {
			t.Fatalf("等待子进程就绪失败: %v", err)
		}
		ready := []byte{0}
		if _, err := io.ReadFull(connection, ready); err != nil || ready[0] != 'R' {
			t.Fatalf("子进程就绪协议失败: %v", err)
		}
		connections = append(connections, connection)
	}
	for _, connection := range connections {
		if _, err := connection.Write([]byte{'S'}); err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}
	winners, full := 0, 0
	for _, item := range children {
		if err := item.cmd.Wait(); err != nil {
			t.Fatalf("容量子进程失败: %v\n%s", err, item.out.String())
		}
		switch {
		case strings.Contains(item.out.String(), "VID_G7_PROCESS_RESULT=RUNNING"):
			winners++
		case strings.Contains(item.out.String(), "VID_G7_PROCESS_RESULT=FULL"):
			full++
		default:
			t.Fatalf("容量子进程缺少结果: %s", item.out.String())
		}
	}
	if winners != min(processes, 2) || full != processes-winners {
		t.Fatalf("%d进程Provider=2裁决错误: running=%d full=%d", processes, winners, full)
	}
	var running, queued int64
	if err := db.Table("ai_gateway_tasks").Where("capability='video.generate' AND status='submitting'").Count(&running).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ai_gateway_tasks").Where("capability='video.generate' AND status='queued'").Count(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if running != int64(winners) || queued != int64(full) {
		t.Fatalf("MySQL多进程终态错误: running=%d queued=%d", running, queued)
	}
	assertVideoCapacityPhases(t, client, winners, full)
}

func TestVideoCapacityProcessHelper(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_PROCESS_HELPER") != "YES" {
		t.Skip("仅由多进程容量父测试启动")
	}
	connection, err := net.DialTimeout("tcp", os.Getenv("MOLIN_VIDEO_G7_PROCESS_BARRIER"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte{'R'}); err != nil {
		t.Fatal(err)
	}
	start := []byte{0}
	if _, err := io.ReadFull(connection, start); err != nil || start[0] != 'S' {
		t.Fatal("父进程起跑协议失败")
	}
	_ = connection.Close()
	db := openVideoG5MySQL(t)
	client, _ := openVideoG7CapacityRedis(t)
	user, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_USER"), 10, 64)
	project, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_PROJECT"), 10, 64)
	keyID, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_KEY"), 10, 64)
	owner := repository.VideoOwner{UserID: user, ProjectID: project, APIKeyID: &keyID}
	worker, err := repository.NewVideoWorkerLeaseRepository(db).Claim(context.Background(), os.Getenv("MOLIN_VIDEO_G7_PROCESS_TASK"), owner, "process-"+os.Getenv("MOLIN_VIDEO_G7_PROCESS_INDEX"), "submit")
	if err != nil {
		t.Fatal(err)
	}
	owned := repository.WithVideoWorkerLease(context.Background(), worker)
	protector, err := NewVideoTaskPayloadProtector("g5-fixture-v1", []byte(strings.Repeat("c", 32)))
	if err != nil {
		t.Fatal(err)
	}
	loader := func(_ context.Context, asset model.AIGatewayInputAsset) (*videogateway.NormalizedReferenceImage, error) {
		data := videoG4TestPNG(t)
		if asset.NormalizedSHA256 == nil || asset.MIMEType == nil || asset.SizeBytes == nil || asset.Width == nil || asset.Height == nil {
			return nil, ErrVideoGovernanceUnavailable
		}
		return &videogateway.NormalizedReferenceImage{Bytes: data, MIMEType: *asset.MIMEType, Width: int(*asset.Width), Height: int(*asset.Height), SizeBytes: *asset.SizeBytes, OriginalSHA256: asset.OriginalSHA256, NormalizedSHA256: *asset.NormalizedSHA256}, nil
	}
	ledger := NewVideoBillingTaskLedger(db, owner, protector, videoG4TestLocationFactory{}, loader)
	policy, _ := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewVideoCapacityExecutionCoordinator(ledger, repository.NewVideoCapacityRecoveryRepository(db), store, mustVideoCapacityNonceKey(t))
	task, err := ledger.Load(owned, os.Getenv("MOLIN_VIDEO_G7_PROCESS_TASK"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.PromoteAndPlan(owned, task.TaskID, task.Version)
	if err == nil {
		fmt.Println("VID_G7_PROCESS_RESULT=RUNNING")
		return
	}
	if errors.Is(err, videogateway.ErrGatewayRunningCapacity) {
		fmt.Println("VID_G7_PROCESS_RESULT=FULL")
		return
	}
	t.Fatal(err)
}

func assertVideoCapacityPhases(t *testing.T, client *redis.Client, running, queued int) {
	t.Helper()
	var state struct {
		Records map[string]struct {
			Phase string `json:"phase"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(videoG7CapacityRaw(t, client)), &state); err != nil {
		t.Fatal(err)
	}
	actualRunning, actualQueued := 0, 0
	for _, row := range state.Records {
		if row.Phase == "running" {
			actualRunning++
		}
		if row.Phase == "queued" {
			actualQueued++
		}
	}
	if actualRunning != running || actualQueued != queued {
		t.Fatalf("Redis多进程终态错误: running=%d queued=%d", actualRunning, actualQueued)
	}
}
