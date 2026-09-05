package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// relayDropConfirmConn只在外部网络边界丢弃真实Broker的basic.ack，确保丢确认之前消息确实已被接受。
type relayDropConfirmConn struct {
	net.Conn
	pending []byte
	dropped *atomic.Bool
}

func (c *relayDropConfirmConn) Read(out []byte) (int, error) {
	if len(c.pending) == 0 {
		header := make([]byte, 7)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		size := binary.BigEndian.Uint32(header[3:7])
		if size > 4*1024*1024 {
			return 0, io.ErrUnexpectedEOF
		}
		body := make([]byte, int(size)+1)
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return 0, err
		}
		if header[0] == 1 && size >= 4 && binary.BigEndian.Uint16(body[:2]) == 60 && binary.BigEndian.Uint16(body[2:4]) == 80 {
			c.dropped.Store(true)
			_ = c.Conn.Close()
			return 0, io.EOF
		}
		c.pending = append(header, body...)
	}
	n := copy(out, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// relayTestOpener只允许本轮loopback或严格临时容器名，拒绝应用环境或真实Broker作为测试目标。
func relayTestOpener(t *testing.T, drop *atomic.Bool, dropped *atomic.Bool) video.TaskConnectionOpener {
	t.Helper()
	raw := os.Getenv("MOLIN_VIDEO_G7_RELAY_AMQP_URL")
	if raw == "" {
		t.Skip("未配置G7专用MySQL/RabbitMQ联合验收")
	}
	u, err := url.Parse(raw)
	allowed := regexp.MustCompile(`^molin-vidg7-outbox-rabbit-[0-9a-f]{12}$`)
	if err != nil || os.Getenv("MOLIN_VIDEO_G7_RELAY_ISOLATED") != "YES" || u.Scheme != "amqp" || u.Path != "/vid_g7" || u.User == nil || (u.Hostname() != "127.0.0.1" && !allowed.MatchString(u.Hostname())) {
		t.Fatal("拒绝非隔离Broker")
	}
	dsn, err := mysqldriver.ParseDSN(os.Getenv("MOLIN_VIDEO_G5_MYSQL_DSN"))
	if err != nil {
		t.Fatal("隔离DSN无效")
	}
	host, _, err := net.SplitHostPort(dsn.Addr)
	if err != nil || dsn.Net != "tcp" || dsn.DBName != "molin_video_g5_contract" || (host != "127.0.0.1" && !regexp.MustCompile(`^molin-vidg7-outbox-mysql-[0-9a-f]{12}$`).MatchString(host)) {
		t.Fatal("拒绝非隔离MySQL")
	}
	return func(ctx context.Context) (*amqp.Connection, error) {
		var stop func() bool
		conn, err := amqp.DialConfig(raw, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			stop = context.AfterFunc(ctx, func() { _ = c.Close() })
			if drop != nil && drop.Load() {
				return &relayDropConfirmConn{Conn: c, dropped: dropped}, nil
			}
			return c, nil
		}})
		if stop != nil {
			stop()
		}
		return conn, err
	}
}

// 财务快照排除预期变化的运输字段，其他七张表仍逐行核对；Task唯一性另行验证。
func relayMoneySnapshot(t *testing.T, f videoG5ReservationFixture) []byte {
	t.Helper()
	var all map[string]json.RawMessage
	if err := json.Unmarshal(mediaDeleteFinanceSnapshot(t, f.db, f.owner.UserID), &all); err != nil {
		t.Fatal(err)
	}
	delete(all, "ai_outbox_events")
	body, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestVideoG7OutboxRelayMySQLRabbit 使用真实预占事务、共享Worker、生产发布器及真实Broker验证联合恢复。
func TestVideoG7OutboxRelayMySQLRabbit(t *testing.T) {
	opener := relayTestOpener(t, nil, nil)
	db := openVideoG5MySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	admin, err := opener(ctx)
	if err != nil {
		t.Fatal("Broker连接失败")
	}
	defer admin.CloseDeadline(time.Now().Add(time.Second))
	ch, err := admin.Channel()
	if err != nil {
		t.Fatal("Broker通道失败")
	}
	for _, mode := range []string{"t2v", "i2v", "unroutable", "confirm_lost", "db_ack_failed", "concurrent", "invalid_fact"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			inputID := ""
			if mode == "i2v" {
				inputID = prepareVideoG5I2V(t, &f).PublicID
			}
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			before := relayMoneySnapshot(t, f)
			topology, err := video.NewTaskTopology(fmt.Sprintf("molin.video.relay.%x", f.owner.UserID))
			if err != nil || topology.Declare(ch) != nil {
				t.Fatal("声明联合测试拓扑失败")
			}
			route, _ := topology.Route(video.TaskSubmit)
			var drop, dropped atomic.Bool
			transport, err := video.NewTaskPublisher(topology, relayTestOpener(t, &drop, &dropped), 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			publisher, err := NewVideoOutboxPublisher(db, transport)
			if err != nil {
				t.Fatal(err)
			}
			repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
			worker := NewOutboxWorker(repo, publisher)
			now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			worker.now = func() time.Time { return now }
			var original model.AIOutboxEvent
			if err := db.Where("aggregate_id=?", f.command.RequestID).Take(&original).Error; err != nil {
				t.Fatal(err)
			}
			read := func() {
				t.Helper()
				until := time.Now().Add(5 * time.Second)
				for time.Now().Before(until) {
					d, ok, err := ch.Get(route.Queue, false)
					if err != nil {
						t.Fatal("Broker读取失败")
					}
					if ok {
						message, err := video.DecodeTaskMessage(d.Body)
						if err != nil || message != (video.TaskMessage{TaskID: f.command.TaskID, RequestID: f.command.RequestID, InputAssetID: inputID}) || d.DeliveryMode != amqp.Persistent {
							t.Fatal("消息必须只有原任务低敏引用")
						}
						if d.Ack(false) != nil {
							t.Fatal("Broker ACK失败")
						}
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("未找到实际Broker消息")
			}
			noMessage := func() {
				t.Helper()
				if _, ok, err := ch.Get(route.Queue, false); err != nil || ok {
					t.Fatal("不应存在额外Broker消息")
				}
			}
			if mode == "unroutable" {
				if err := ch.QueueUnbind(route.Queue, route.RoutingKey, route.WorkExchange, nil); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "confirm_lost" {
				drop.Store(true)
			}
			if mode == "invalid_fact" {
				if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", original.ID).Update("payload_json", gorm.Expr("JSON_SET(payload_json,'$.currency','USD')")).Error; err != nil {
					t.Fatal(err)
				}
			}
			var failDB atomic.Bool
			hook := fmt.Sprintf("g7_relay_ack_%d", f.owner.UserID)
			if mode == "db_ack_failed" {
				failDB.Store(true)
				if err := db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
					values, ok := tx.Statement.Dest.(map[string]interface{})
					if ok && tx.Statement.Table == "ai_outbox_events" && values["status"] == model.AIOutboxPublished && failDB.CompareAndSwap(true, false) {
						tx.AddError(errors.New("合成确认持久化故障"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Update().Remove(hook)
			}
			if mode == "concurrent" {
				var wg sync.WaitGroup
				var published atomic.Int64
				start := make(chan struct{})
				for i := 0; i < 100; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						n, err := worker.RunOnce(ctx, 1)
						if err != nil {
							t.Error(err)
						}
						published.Add(int64(n))
					}()
				}
				close(start)
				wg.Wait()
				if published.Load() != 1 {
					t.Fatal("百路Outbox发布必须只有一个正常确认赢家")
				}
				read()
				noMessage()
			} else {
				n, err := worker.RunOnce(ctx, 1)
				var event model.AIOutboxEvent
				if e := db.First(&event, original.ID).Error; e != nil {
					t.Fatal(e)
				}
				switch mode {
				case "unroutable", "confirm_lost", "invalid_fact":
					if err != nil || n != 0 || event.Status != model.AIOutboxPending || event.RetryCount != 1 || event.LockedAt == nil {
						t.Fatalf("发布失败必须保留原事件待重试: %v", err)
					}
					if mode == "confirm_lost" {
						if !dropped.Load() {
							t.Fatal("未真实丢弃Broker确认")
						}
						read()
					} else {
						noMessage()
					}
					if mode == "invalid_fact" {
						break
					}
					if mode == "unroutable" {
						if err := ch.QueueBind(route.Queue, route.RoutingKey, route.WorkExchange, false, nil); err != nil {
							t.Fatal(err)
						}
					}
					drop.Store(false)
					now = now.Add(5 * time.Second)
					if n, err := worker.RunOnce(ctx, 1); err != nil || n != 1 {
						t.Fatalf("恢复应发布原事件: %v", err)
					}
					read()
					noMessage()
				case "db_ack_failed":
					if err == nil || n != 0 || failDB.Load() || event.Status != model.AIOutboxPublishing || event.LockedAt == nil {
						t.Fatal("Broker成功而DB确认失败必须保留原publishing")
					}
					read()
					now = now.Add(3 * time.Minute)
					if n, err := worker.RunOnce(ctx, 1); err != nil || n != 1 {
						t.Fatalf("过期接管应重发原引用: %v", err)
					}
					read()
					noMessage()
				default:
					if err != nil || n != 1 || event.Status != model.AIOutboxPublished || event.ProcessedAt == nil {
						t.Fatalf("成功发布必须确认原Outbox: %v", err)
					}
					read()
					noMessage()
				}
			}
			if !bytes.Equal(before, relayMoneySnapshot(t, f)) {
				t.Fatal("运输失败/重试不能改变七张财务表")
			}
			var tasks, events int64
			if err := db.Model(&model.AIImageTask{}).Where("request_id=?", f.command.RequestID).Count(&tasks).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=?", f.command.RequestID).Count(&events).Error; err != nil {
				t.Fatal(err)
			}
			if tasks != 1 || events != 1 {
				t.Fatal("恢复不得创建平行Task或Outbox")
			}
			var final model.AIOutboxEvent
			if err := db.First(&final, original.ID).Error; err != nil {
				t.Fatal(err)
			}
			if final.EventID != original.EventID || (mode != "invalid_fact" && final.Status != model.AIOutboxPublished) {
				t.Fatal("必须保留并完成原事件")
			}
		})
	}
}
