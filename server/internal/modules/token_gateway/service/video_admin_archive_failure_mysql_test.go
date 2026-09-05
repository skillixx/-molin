package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG6AdminArchiveSafetyFailureMySQL(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "normal"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			f := newAdminCancelErrorFixture(t)
			f.f.PrepareArchive(f.task)
			owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
			repo := repository.NewVideoTaskRepository(f.f.DB)
			task, err := repo.FindForOwner(context.Background(), f.task, owner)
			if err != nil {
				t.Fatal(err)
			}
			if pending {
				task, err = repo.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: "pending_reconcile", Progress: task.Progress, Source: "worker", EventID: "vg6_archive_safety_pending", Now: time.Now().UTC()})
				if err != nil {
					t.Fatal(err)
				}
			}
			o := f.f.ArchiveOptions()
			o.Safety = video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationRejectFrames), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess))
			p, err := service.NewVideoAdminReasonProtector("g6-archive-reject-v1", f.secret)
			if err != nil {
				t.Fatal(err)
			}
			app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, Archive: &o})
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			before := f.f.FinancialSnapshot()
			body, _ := json.Marshal(map[string]any{"reason": "合成安全拒绝归档", "version_no": task.VersionNo})
			r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+f.task+"/archive-retry", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "g6-admin-archive-rejection")
			r.Header.Set("Authorization", "Bearer "+f.token)
			resp, err := srv.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var e struct {
				Code int                         `json:"code"`
				Data service.VideoAdminPollReply `json:"data"`
			}
			if json.Unmarshal(raw, &e) != nil || resp.StatusCode != 200 || e.Code != 0 || e.Data.Status != "unknown" {
				t.Fatalf("安全拒绝必须保留待核对回执，HTTP=%d", resp.StatusCode)
			}
			var root model.AIImageAsset
			if err := f.f.DB.Where("task_id=? AND asset_role='content'", task.ID).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			if root.ModerationStatus != "rejected" || root.LifecycleState != "quarantined" || root.Bucket == nil || *root.Bucket != "ai-quarantine" {
				t.Fatal("必须保留真实审核拒绝和隔离位置，不能丢弃安全事实")
			}
			current, err := repo.FindForOwner(context.Background(), f.task, owner)
			if err != nil {
				t.Fatal(err)
			}
			wantState := "failed"
			if pending {
				wantState = "pending_reconcile"
			}
			if current.Status != wantState || current.ArchiveTokenHash != nil || current.DeliveryStatus != "pending" {
				t.Fatal("不得伪造pending的原始审核阶段，或提前交付")
			}
			var origins int64
			if err := f.f.DB.Table("ai_gateway_task_events").Where("task_id=? AND failure_origin='moderation_rejected'", task.ID).Count(&origins).Error; err != nil {
				t.Fatal(err)
			}
			if (!pending && origins != 1) || (pending && origins != 0) {
				t.Fatal("明确失败来源只能属于真实moderating→failed，不能伪造退款依据")
			}
			var oldFunds, newFunds map[string][]string
			json.Unmarshal(before, &oldFunds)
			json.Unmarshal(f.f.FinancialSnapshot(), &newFunds)
			for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links"} {
				if !reflect.DeepEqual(oldFunds[table], newFunds[table]) {
					t.Fatal("安全拒绝不能在HTTP中自行退款或改变计量")
				}
			}
			var commands, audits int64
			if err := f.f.DB.Table("ai_video_admin_archive_commands").Where("actor_user_id=? AND status='unknown'", f.actor).Count(&commands).Error; err != nil || commands != 1 {
				t.Fatal("失败回执必须持久化")
			}
			if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&audits).Error; err != nil || audits != 2 {
				t.Fatal("安全事实和前后审计必须完整")
			}
		})
	}
}
