#!/usr/bin/env python3
"""使用真实 Redis 验证邮件锁原语，不读取 env 文件、不输出连接秘密。

必须由 run-redis-lock-contract.ps1 加载受控环境后执行。所有 key 使用本轮唯一前缀，
finally 中只精确 DEL 本脚本创建的 key；禁止 FLUSHDB、KEYS * 和模式删除。
"""

import os
import socket
import ssl
import sys
import time
import uuid


class RedisError(RuntimeError):
    pass


class Redis:
    def __init__(self):
        addr = os.getenv("REDIS_ADDR", "")
        if not addr or ":" not in addr:
            raise RedisError("缺少 REDIS_ADDR")
        host, port = addr.rsplit(":", 1)
        sock = socket.create_connection((host, int(port)), timeout=5)
        if os.getenv("REDIS_TLS", "").lower() in ("1", "true", "yes"):
            sock = ssl.create_default_context().wrap_socket(sock, server_hostname=host)
        self.sock = sock
        self.file = sock.makefile("rb")
        password = os.getenv("REDIS_PASSWORD", "")
        if password:
            self.command("AUTH", password)
        db = int(os.getenv("REDIS_DB", "0"))
        if db:
            self.command("SELECT", str(db))

    def command(self, *parts):
        payload = f"*{len(parts)}\r\n".encode()
        for part in parts:
            raw = str(part).encode()
            payload += f"${len(raw)}\r\n".encode() + raw + b"\r\n"
        self.sock.sendall(payload)
        return self._read()

    def _read(self):
        lead = self.file.read(1)
        line = self.file.readline().rstrip(b"\r\n")
        if lead == b"+":
            return line.decode()
        if lead == b"-":
            raise RedisError("Redis 返回错误（内容已隐藏）")
        if lead == b":":
            return int(line)
        if lead == b"$":
            size = int(line)
            if size < 0:
                return None
            data = self.file.read(size)
            self.file.read(2)
            return data.decode()
        raise RedisError("无法识别 Redis 响应")

    def close(self):
        self.file.close()
        self.sock.close()


RENEW = "if redis.call('get',KEYS[1])==ARGV[1] then return redis.call('pexpire',KEYS[1],ARGV[2]) else return 0 end"
RELEASE = "if redis.call('get',KEYS[1])==ARGV[1] then return redis.call('del',KEYS[1]) else return 0 end"


def main():
    if os.getenv("EMAIL_REDIS_TEST_ACK", "") != "I_UNDERSTAND_EXACT_CLEANUP":
        print("[SKIP] 未设置 EMAIL_REDIS_TEST_ACK=I_UNDERSTAND_EXACT_CLEANUP，未连接 Redis")
        return 0
    client = None
    created = []
    prefix = f"qa:email:phase2:{int(time.time())}:{uuid.uuid4().hex}"
    try:
        client = Redis()
        for suffix, ttl in (("sync", 30000), ("otp", 15000), ("test", 15000)):
            key = f"{prefix}:{suffix}"
            token = uuid.uuid4().hex + uuid.uuid4().hex
            other = uuid.uuid4().hex + uuid.uuid4().hex
            created.append(key)
            assert client.command("SET", key, token, "NX", "PX", ttl) == "OK"
            assert client.command("SET", key, other, "NX", "PX", ttl) is None
            assert client.command("EVAL", RENEW, 1, key, other, ttl) == 0
            assert client.command("EVAL", RENEW, 1, key, token, ttl) == 1
            assert client.command("EVAL", RELEASE, 1, key, other) == 0
            assert client.command("EVAL", RELEASE, 1, key, token) == 1
            print(f"[PASS] {suffix}：NX 互斥、所有权续租、所有权释放（key 已隐藏）")
        return 0
    except (AssertionError, OSError, RedisError, ValueError) as exc:
        print(f"[FAIL] Redis 锁原语：{type(exc).__name__}（连接信息与 Redis 错误内容不输出）")
        return 1
    finally:
        if client:
            for key in created:
                try:
                    client.command("DEL", key)
                except Exception:
                    print("[WARN] 一个唯一测试 key 精确清理失败；请按本轮受控记录人工精确删除，禁止模式扫描")
            client.close()


if __name__ == "__main__":
    sys.exit(main())
