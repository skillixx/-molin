package service

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 所有字面量取自已批准的非商业F01-F12，不能从业务计算器反推预期。
type videoGoldenCase struct {
	id, scenario, operation, quote, userQuantity, sale, cost, providerQuantity, settled, released, balance, frozen string
	execution, billing, delivery, compensation, events, lifecycle                                                  string
	assets, submits                                                                                                int
	closed                                                                                                         bool
}

func videoG5GoldenCases() []videoGoldenCase {
	return []videoGoldenCase{
		{"F01", "success", "text_to_video", "0.50", "5", "0.50", "0.20", "5", "0.50", "0", "9.50", "0", "succeeded", "settled", "available", "none", "A,H,S", "available", 6, 1, true},
		{"F02", "success", "image_to_video", "0.75", "5", "0.75", "0.30", "5", "0.75", "0", "9.25", "0", "succeeded", "settled", "available", "none", "A,H,S", "available", 6, 1, true},
		{"F03", "moderation_rejected", "text_to_video", "0.50", "0", "0", "0.20", "5", "0", "0.50", "10", "0", "failed", "released", "rejected", "none", "H,J,R", "quarantined", 1, 1, true},
		{"F04", "provider_failed", "text_to_video", "0.50", "0", "0", "0", "0", "0", "0.50", "10", "0", "failed", "released", "rejected", "none", "H,J,R", "none", 0, 1, true},
		{"F05", "store_failed", "text_to_video", "0.50", "-", "-", "0.20", "5", "-", "0", "9.50", "0.50", "failed", "settlement_pending", "pending", "pending", "C,H,P", "none", 0, 1, false},
		{"F06", "settlement_compensated", "text_to_video", "0.50", "5", "0.50", "0.20", "5", "0.50", "0", "9.50", "0", "succeeded", "settled", "available", "completed", "A,C,H,P,S", "available", 6, 1, true},
		{"F07", "queued_cancelled", "text_to_video", "0.50", "0", "0", "0", "-", "0", "0.50", "10", "0", "cancelled", "released", "rejected", "none", "H,J,R", "none", 0, 0, true},
		{"F08", "cancel_accepted", "text_to_video", "0.50", "0", "0", "0", "0", "0", "0.50", "10", "0", "cancelled", "released", "rejected", "none", "H,J,R", "none", 0, 1, true},
		{"F09", "cancel_rejected", "text_to_video", "0.50", "-", "-", "-", "-", "-", "0", "9.50", "0.50", "submitted", "held", "pending", "none", "H", "none", 0, 1, false},
		{"F10", "late_success", "text_to_video", "0.50", "5", "0.50", "0.20", "5", "0.50", "0", "9.50", "0", "succeeded", "settled", "available", "none", "A,H,S", "available", 6, 1, true},
		{"F11", "unknown", "text_to_video", "0.50", "-", "-", "-", "-", "-", "0", "9.50", "0.50", "pending_reconcile", "settlement_pending", "pending", "retry", "C,H,P", "none", 0, 1, false},
		{"F12", "usage_conflict", "text_to_video", "0.50", "-", "-", "0.24", "6", "-", "0", "9.50", "0.50", "pending_reconcile", "settlement_pending", "pending", "manual_review", "C,H,P", "temporary", 6, 1, false},
	}
}

func TestVideoG5GoldenMySQLTwelveFinancialOutcomes(t *testing.T) {
	db := openVideoG5MySQL(t)
	var observations []videoGoldenObservation
	for _, c := range videoG5GoldenCases() {
		t.Run(c.id, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if c.operation == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			before := readVideoGoldenWallet(t, f)
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			mode := videogateway.FakeVideoSuccess
			if c.scenario == "provider_failed" {
				mode = videogateway.FakeVideoExplicitFailure
			}
			if c.scenario == "unknown" {
				mode = videogateway.FakeVideoResultUnknown
			}
			if c.scenario == "cancel_rejected" {
				mode = videogateway.FakeVideoCancelRejected
			}
			adapter := videogateway.NewFakeAsyncVideoAdapter(mode)
			var provider videogateway.VideoProviderAdapter = adapter
			if c.scenario == "usage_conflict" {
				provider = videoG5UsageMismatchAdapter{adapter}
			}
			moderation := videogateway.FakeVideoModerationAllow
			if c.scenario == "moderation_rejected" {
				moderation = videogateway.FakeVideoModerationRejectFrames
			}
			var store videogateway.VideoObjectStore = videogateway.NewFakeVideoObjectStore()
			if c.scenario == "store_failed" {
				store = videoG5ReleaseTestStore{store}
			}
			ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: provider, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(moderation), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
			ctx := context.Background()
			if c.scenario == "queued_cancelled" {
				task, err := ledger.Load(ctx, f.command.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Advance(ctx, task.TaskID, task.Version, videogateway.TaskQueued, "worker", "state_advanced", nil); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.CancelBeforeSubmit(ctx, task.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := gateway.Submit(ctx, f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				if c.scenario == "late_success" {
					task, err := gateway.Query(ctx, f.command.TaskID)
					if err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 2; i++ {
						if _, err := adapter.Query(ctx, videogateway.QueryRequest{ProviderTaskID: task.ProviderTaskID}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if c.scenario == "cancel_accepted" || c.scenario == "cancel_rejected" || c.scenario == "late_success" {
					_, err := gateway.Cancel(ctx, f.command.TaskID)
					if c.scenario == "cancel_rejected" {
						if !errors.Is(err, videogateway.ErrProviderCancelRejected) {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
				} else {
					for i := 0; i < 2; i++ {
						_, err := gateway.Poll(ctx, f.command.TaskID)
						if err != nil && c.scenario != "provider_failed" && c.scenario != "unknown" {
							t.Fatal(err)
						}
					}
				}
				if c.scenario != "cancel_accepted" && c.scenario != "cancel_rejected" && c.scenario != "unknown" && c.scenario != "provider_failed" {
					_, err := gateway.FetchAndFinalize(ctx, f.command.TaskID)
					if c.scenario == "store_failed" || c.scenario == "moderation_rejected" {
						if err == nil {
							t.Fatal("注入失败必须被观察到")
						}
					} else if err != nil {
						t.Fatal(err)
					}
				}
				if c.scenario == "cancel_accepted" || c.scenario == "provider_failed" || c.scenario == "moderation_rejected" {
					if _, err := f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				if c.scenario == "success" || c.scenario == "late_success" || c.scenario == "settlement_compensated" {
					if c.scenario == "settlement_compensated" {
						f.service.fault = func(at string) error {
							if at == "settle_hold" {
								return errors.New("金样结算故障")
							}
							return nil
						}
						if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err == nil {
							t.Fatal("应复现结算回滚")
						}
						f.service.fault = nil
						mid := c
						mid.id = "F06_before_recovery"
						mid.userQuantity = "-"
						mid.sale = "-"
						mid.settled = "-"
						mid.frozen = "0.50"
						mid.billing = "settlement_pending"
						mid.delivery = "pending"
						mid.compensation = "pending"
						mid.events = "C,H,P"
						mid.lifecycle = "temporary"
						mid.closed = false
						observeVideoGolden(t, f, adapter, before, mid)
						worker, err := NewVideoCompensationWorker(f.service, "golden-worker")
						if err != nil {
							t.Fatal(err)
						}
						if result, err := worker.RunOne(ctx, f.command.RequestID); err != nil || result.Status != "completed" {
							t.Fatalf("补偿应闭合: %+v %v", result, err)
						}
					} else {
						if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil {
							t.Fatal(err)
						}
						if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if c.scenario == "unknown" {
				worker, err := NewVideoCompensationWorker(f.service, "golden-unknown")
				if err != nil {
					t.Fatal(err)
				}
				if result, err := worker.RunOne(ctx, f.command.RequestID); err != nil || result.Status != "retry" {
					t.Fatalf("未知结果只能进入有界retry: %+v %v", result, err)
				}
			}
			if c.scenario == "usage_conflict" {
				initial := c
				initial.id = "F12_before_review"
				initial.compensation = "pending"
				observeVideoGolden(t, f, adapter, before, initial)
				checker := f.owner.UserID + 900000
				if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
					t.Fatal(err)
				}
				repo := repository.NewVideoCompensationRepository(db)
				lease, err := repo.ClaimManual(ctx, f.command.RequestID, "golden-review", f.owner.UserID, checker)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Transaction(func(tx *gorm.DB) error { return repo.FinishTx(tx, *lease, "manual_review", "facts_conflict") }); err != nil {
					t.Fatal(err)
				}
			}
			observations = append(observations, observeVideoGolden(t, f, adapter, before, c))
			// 查询与财务重放不得再次提交，未闭合结果也不能被read gate公开。
			if !c.closed {
				if reader, err := gateway.ReadContent(ctx, f.command.TaskID, 0, 1); err == nil {
					if reader != nil {
						reader.Close()
					}
					t.Error("未闭合金样禁止交付")
				}
			}
			if adapter.SubmitCalls() != c.submits {
				t.Fatal("金样调用次数不符合授权")
			}
			if c.scenario == "cancel_rejected" || c.scenario == "late_success" {
				task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || task.CancelRequestedAt == nil {
					t.Fatal("必须保留取消意图")
				}
			}
		})
	}
	assertVideoGoldenTotals(t, observations)
}
