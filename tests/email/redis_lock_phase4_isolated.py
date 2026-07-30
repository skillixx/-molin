#!/usr/bin/env python3
"""以本轮冻结前缀验证邮件 Redis 锁原语，并提供无网络自测。"""

import hashlib
import os
import re
import socket
import ssl
import sys


前缀格式 = re.compile(r"^qa:email:phase4:[0-9a-f]{32}$")
后缀与租期 = (("sync", 30000), ("otp", 15000), ("test", 15000))
续租脚本 = "if redis.call('get',KEYS[1])==ARGV[1] then return redis.call('pexpire',KEYS[1],ARGV[2]) else return 0 end"
释放脚本 = "if redis.call('get',KEYS[1])==ARGV[1] then return redis.call('del',KEYS[1]) else return 0 end"


class Redis协议错误(RuntimeError):
    """表示连接失败或 Redis 返回了不符合契约的响应。"""


class 自测致命异常(BaseException):
    """仅用于证明最外层门禁仍会在 finally 清理后失败关闭。"""


class Redis客户端:
    """只实现本验收需要的 RESP 子集，不读取配置文件。"""

    def __init__(self):
        地址 = os.getenv("REDIS_ADDR", "")
        if not 地址 or ":" not in 地址:
            raise Redis协议错误("配置缺失")
        主机, 端口 = 地址.rsplit(":", 1)
        连接 = socket.create_connection((主机, int(端口)), timeout=5)
        if os.getenv("REDIS_TLS", "").lower() in ("1", "true", "yes"):
            连接 = ssl.create_default_context().wrap_socket(连接, server_hostname=主机)
        self.连接 = 连接
        self.读取器 = 连接.makefile("rb")
        密码 = os.getenv("REDIS_PASSWORD", "")
        if 密码:
            self.命令("AUTH", 密码)
        库号 = int(os.getenv("REDIS_DB", "0"))
        if 库号:
            self.命令("SELECT", str(库号))

    def 命令(self, *参数):
        """发送一条参数化命令，调用方只能传入精确 key。"""
        报文 = f"*{len(参数)}\r\n".encode()
        for 参数 in 参数:
            原始值 = str(参数).encode()
            报文 += f"${len(原始值)}\r\n".encode() + 原始值 + b"\r\n"
        self.连接.sendall(报文)
        return self._读取响应()

    def _读取响应(self):
        类型 = self.读取器.read(1)
        首行 = self.读取器.readline().rstrip(b"\r\n")
        if 类型 == b"+":
            return 首行.decode()
        if 类型 == b"-":
            raise Redis协议错误("服务端拒绝")
        if 类型 == b":":
            return int(首行)
        if 类型 == b"$":
            长度 = int(首行)
            if 长度 < 0:
                return None
            内容 = self.读取器.read(长度)
            self.读取器.read(2)
            return 内容.decode()
        raise Redis协议错误("响应类型非法")

    def 关闭(self):
        self.读取器.close()
        self.连接.close()


def 执行验收(客户端工厂, 前缀, 输出=True):
    """执行创建前检查、锁原语和新连接清理复核。"""
    if not 前缀格式.fullmatch(前缀):
        return "precondition", 0, 0
    键列表 = [f"{前缀}:{后缀}" for 后缀, _ in 后缀与租期]
    已创建 = []
    客户端 = None
    分类 = "pass"
    前检查数 = 0
    后检查数 = 0
    try:
        客户端 = 客户端工厂()
        if 客户端.命令("PING") != "PONG":
            raise Redis协议错误("连接复核失败")
        for 键 in 键列表:
            if 客户端.命令("EXISTS", 键) != 0:
                分类 = "preexisting_key"
                break
            前检查数 += 1
        for (后缀, 租期), 键 in (() if 分类 != "pass" else zip(后缀与租期, 键列表)):
            所有者 = hashlib.sha256((前缀 + 后缀 + "owner").encode()).hexdigest()
            其他者 = hashlib.sha256((前缀 + 后缀 + "other").encode()).hexdigest()
            if 客户端.命令("SET", 键, 所有者, "NX", "PX", 租期) != "OK":
                分类 = "set_conflict"
                break
            已创建.append(键)
            if 客户端.命令("SET", 键, 其他者, "NX", "PX", 租期) is not None:
                分类 = "mutex"
                break
            if 客户端.命令("EVAL", 续租脚本, 1, 键, 其他者, 租期) != 0:
                分类 = "wrong_owner_renew"
                break
            if 客户端.命令("EVAL", 续租脚本, 1, 键, 所有者, 租期) != 1:
                分类 = "owner_renew"
                break
            if 客户端.命令("EVAL", 释放脚本, 1, 键, 其他者) != 0:
                分类 = "wrong_owner_release"
                break
            if 客户端.命令("EVAL", 释放脚本, 1, 键, 所有者) != 1:
                分类 = "owner_release"
                break
    except (OSError, ValueError, Redis协议错误):
        分类 = "connection_or_protocol"
    except Exception:
        # 未预期异常只暴露固定分类，禁止回显类型、原文或连接信息。
        分类 = "unexpected"
    finally:
        清理失败 = False
        if 客户端 is not None:
            for 键 in 已创建:
                try:
                    客户端.命令("DEL", 键)
                except Exception:
                    清理失败 = True
            try:
                客户端.关闭()
            except Exception:
                清理失败 = True
        if 清理失败:
            分类 = "cleanup"

    # 若创建前发现外部同名 key，绝不删除或把它误判为本轮清理残留。
    if 分类 != "preexisting_key":
        try:
            复核客户端 = 客户端工厂()
            try:
                for 键 in 键列表:
                    if 复核客户端.命令("EXISTS", 键) != 0:
                        if 分类 != "cleanup":
                            分类 = "post_cleanup"
                    else:
                        后检查数 += 1
            finally:
                复核客户端.关闭()
        except Exception:
            if 分类 != "cleanup":
                分类 = "post_cleanup_connection"
    if 输出:
        摘要 = hashlib.sha256(前缀.encode()).hexdigest()
        状态 = "PASS" if 分类 == "pass" else "FAIL"
        print(f"[{状态}] mode=redis prefix_sha256={摘要} classification={分类} keys=3 pre_exists_zero={前检查数} post_exists_zero={后检查数}")
    return 分类, 前检查数, 后检查数


def 安全执行(客户端工厂, 前缀, 输出=True):
    """捕获普通验收边界之外的致命异常，并返回固定安全分类。"""
    try:
        return 执行验收(客户端工厂, 前缀, 输出)
    except BaseException:
        if 输出:
            print("[FAIL] mode=redis prefix_sha256=" + hashlib.sha256(前缀.encode()).hexdigest() + " classification=fatal keys=3 pre_exists_zero=0 post_exists_zero=0")
        return "fatal", 0, 0


class 假Redis状态:
    def __init__(self):
        self.数据 = {}
        self.清理失败 = False
        self.原语异常 = False
        self.未预期异常 = False
        self.致命异常 = False


class 假Redis客户端:
    """仅供进程内自测，不打开端口。"""

    def __init__(self, 状态):
        self.状态 = 状态

    def 命令(self, 名称, *参数):
        if 名称 == "PING":
            return "PONG"
        if 名称 == "EXISTS":
            return int(参数[0] in self.状态.数据)
        if 名称 == "SET":
            键, 值 = 参数[0], 参数[1]
            if 键 in self.状态.数据:
                return None
            self.状态.数据[键] = 值
            return "OK"
        if 名称 == "EVAL":
            if self.状态.未预期异常:
                self.状态.未预期异常 = False
                raise RuntimeError("注入未预期异常")
            if self.状态.致命异常:
                self.状态.致命异常 = False
                raise 自测致命异常()
            if self.状态.原语异常:
                self.状态.原语异常 = False
                raise Redis协议错误("注入异常")
            脚本, _, 键, 所有者 = 参数[:4]
            if self.状态.数据.get(键) != 所有者:
                return 0
            if 脚本 == 释放脚本:
                del self.状态.数据[键]
            return 1
        if 名称 == "DEL":
            if self.状态.清理失败:
                raise Redis协议错误("注入清理失败")
            return int(self.状态.数据.pop(参数[0], None) is not None)
        raise Redis协议错误("自测命令越界")

    def 关闭(self):
        return None


def 运行自测():
    前缀 = "qa:email:phase4:" + "1" * 32
    通过数 = 0

    状态 = 假Redis状态()
    if 执行验收(lambda: 假Redis客户端(状态), 前缀, False) == ("pass", 3, 3):
        通过数 += 1

    状态 = 假Redis状态()
    状态.数据[前缀 + ":sync"] = "foreign"
    if 执行验收(lambda: 假Redis客户端(状态), 前缀, False)[0] == "preexisting_key" and 状态.数据[前缀 + ":sync"] == "foreign":
        通过数 += 1

    状态 = 假Redis状态()
    状态.原语异常 = True
    if 执行验收(lambda: 假Redis客户端(状态), 前缀, False)[0] == "connection_or_protocol" and not 状态.数据:
        通过数 += 1

    状态 = 假Redis状态()
    状态.原语异常 = True
    状态.清理失败 = True
    if 执行验收(lambda: 假Redis客户端(状态), 前缀, False)[0] in ("cleanup", "post_cleanup"):
        通过数 += 1

    状态 = 假Redis状态()
    状态.未预期异常 = True
    if 执行验收(lambda: 假Redis客户端(状态), 前缀, False)[0] == "unexpected" and not 状态.数据:
        通过数 += 1

    状态 = 假Redis状态()
    状态.致命异常 = True
    if 安全执行(lambda: 假Redis客户端(状态), 前缀, False)[0] == "fatal" and not 状态.数据:
        通过数 += 1

    状态文本 = "PASS" if 通过数 == 6 else "FAIL"
    print(f"[{状态文本}] mode=selftest cases=6 passed={通过数} external_access=false keys_created=0")
    return 0 if 通过数 == 6 else 1


def main():
    if "--self-test" in sys.argv:
        return 运行自测()
    if os.getenv("EMAIL_REDIS_TEST_ACK", "") != "I_CONFIRM_PHASE4_EXACT_THREE_KEYS":
        print("[SKIP] mode=redis classification=ack_missing keys=0")
        return 0
    前缀 = os.getenv("EMAIL_REDIS_TEST_PREFIX", "")
    分类, _, _ = 安全执行(Redis客户端, 前缀)
    return 0 if 分类 == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
