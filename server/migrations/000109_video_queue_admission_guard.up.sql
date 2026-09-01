-- G6关闭态排队门闩只序列化Task账本计数，不保存第二套队列深度或运行租约。
CREATE TABLE IF NOT EXISTS ai_video_queue_admission_guard (
 id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
 version_no BIGINT UNSIGNED NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 CONSTRAINT chk_video_queue_admission_guard CHECK(id=1 AND version_no>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
INSERT INTO ai_video_queue_admission_guard(id,version_no,updated_at)
VALUES(1,1,UTC_TIMESTAMP(6))
ON DUPLICATE KEY UPDATE id=VALUES(id);
