#!/usr/bin/env python3
"""静态校验所有 Nginx API 反代入口的来源头边界。"""

from pathlib import Path
import re
import sys


配置目录 = Path(__file__).resolve().parent
配置文件 = ("admin.conf", "user.conf", "api.conf")
控制台配置文件 = {"admin.conf", "user.conf"}
BOOTSTRAP_路径 = "/api/internal/email/bootstrap/admin-verify"
TRACE_归一化位置 = "@bootstrap_path_not_found"


def 规范化块头(块头: str) -> str:
    """压缩块头空白，避免缩进差异影响结构判断。"""
    return re.sub(r"\s+", " ", 块头.strip())


def 提取全部配置块(内容: str) -> list[tuple[str, str, str | None]]:
    """提取块头、块内容和直接父块头，用于区分 server 级与 location 内指令。"""
    块栈: list[tuple[int, str, str | None]] = []
    配置块: list[tuple[str, str, str | None]] = []

    for 匹配 in re.finditer(r"[{}]", 内容):
        if 匹配.group() == "{":
            行首 = 内容.rfind("\n", 0, 匹配.start()) + 1
            块头 = 规范化块头(内容[行首 : 匹配.start()])
            父块头 = 块栈[-1][1] if 块栈 else None
            块栈.append((匹配.end(), 块头, 父块头))
            continue

        if not 块栈:
            raise ValueError("存在未配对的右花括号")
        起点, 块头, 父块头 = 块栈.pop()
        配置块.append((块头, 内容[起点 : 匹配.start()], 父块头))

    if 块栈:
        raise ValueError("存在未闭合的配置块")
    return 配置块


def 提取直接内容(块内容: str) -> str:
    """移除嵌套块内容，确保 server 级断言不会被 location 内同名指令误导。"""
    深度 = 0
    结果: list[str] = []
    for 字符 in 块内容:
        if 字符 == "{":
            深度 += 1
            continue
        if 字符 == "}":
            深度 -= 1
            if 深度 < 0:
                raise ValueError("嵌套块深度非法")
            continue
        if 深度 == 0:
            结果.append(字符)
        elif 字符 == "\n":
            结果.append("\n")
    if 深度 != 0:
        raise ValueError("嵌套块未闭合")
    return "".join(结果)


def 校验控制台全方法拒绝(内容: str) -> list[str]:
    """验证控制台对 TRACE 的核心 405 仅在精确 bootstrap URI 上归一化为 404。"""
    错误: list[str] = []
    try:
        配置块 = 提取全部配置块(内容)
    except ValueError as 异常:
        return [str(异常)]

    服务块 = [块 for 块 in 配置块 if 块[0] == "server" and 块[2] is None]
    if len(服务块) != 1:
        return [f"顶层 server 块数量为 {len(服务块)}，应为 1"]

    服务直接内容 = 提取直接内容(服务块[0][1])
    重映射模式 = rf"^\s*error_page\s+405\s*=\s*{re.escape(TRACE_归一化位置)}\s*;\s*$"
    重映射数量 = len(re.findall(重映射模式, 服务直接内容, flags=re.MULTILINE))
    if 重映射数量 != 1:
        错误.append(f"server 级 TRACE 405 重映射数量为 {重映射数量}，应为 1")

    目标块头模式 = rf"location\s+\^~\s+{re.escape(BOOTSTRAP_路径)}"
    目标位置块 = [
        块
        for 块 in 配置块
        if re.fullmatch(目标块头模式, 块[0]) and 块[2] == "server"
    ]
    if len(目标位置块) != 1:
        错误.append(f"bootstrap 专用 404 location 数量为 {len(目标位置块)}，应为 1")
    else:
        目标直接内容 = 提取直接内容(目标位置块[0][1])
        if len(re.findall(r"^\s*return\s+404\s*;\s*$", 目标直接内容, re.MULTILINE)) != 1:
            错误.append("bootstrap 专用 location 未唯一固定返回 404")
        if "proxy_pass" in 目标位置块[0][1]:
            错误.append("bootstrap 专用 location 禁止配置 proxy_pass")

    命名块头 = f"location {TRACE_归一化位置}"
    命名位置块 = [块 for 块 in 配置块 if 块[0] == 命名块头 and 块[2] == "server"]
    if len(命名位置块) != 1:
        错误.append(f"TRACE 归一化命名 location 数量为 {len(命名位置块)}，应为 1")
        return 错误

    命名块内容 = 命名位置块[0][1]
    命名直接内容 = 提取直接内容(命名块内容)
    if "proxy_pass" in 命名块内容:
        错误.append("TRACE 归一化命名 location 禁止配置 proxy_pass")
    if len(re.findall(r"^\s*return\s+405\s*;\s*$", 命名直接内容, re.MULTILINE)) != 1:
        错误.append("TRACE 归一化命名 location 必须对其他 URI 唯一返回 405")
    if re.search(r"^\s*return\s+404\s*;\s*$", 命名直接内容, re.MULTILINE):
        错误.append("TRACE 归一化命名 location 禁止在顶层对所有 URI 返回 404")

    精确条件模式 = rf'if\s*\(\s*\$uri\s*=\s*"{re.escape(BOOTSTRAP_路径)}"\s*\)'
    精确条件块 = [
        块
        for 块 in 配置块
        if re.fullmatch(精确条件模式, 块[0]) and 块[2] == 命名块头
    ]
    if len(精确条件块) != 1:
        错误.append(f"TRACE 精确 URI 条件数量为 {len(精确条件块)}，应为 1")
    else:
        条件直接内容 = 提取直接内容(精确条件块[0][1])
        if len(re.findall(r"^\s*return\s+404\s*;\s*$", 条件直接内容, re.MULTILINE)) != 1:
            错误.append("TRACE 精确 URI 条件未唯一返回 404")

    return 错误


def 提取反代块(内容: str) -> list[str]:
    """按花括号提取包含 API 上游 proxy_pass 的最内层配置块。"""
    块栈: list[tuple[int, str]] = []
    反代块: list[str] = []

    for 匹配 in re.finditer(r"[{}]", 内容):
        if 匹配.group() == "{":
            行首 = 内容.rfind("\n", 0, 匹配.start()) + 1
            块头 = 内容[行首 : 匹配.start()].strip()
            块栈.append((匹配.end(), 块头))
            continue

        if not 块栈:
            raise ValueError("存在未配对的右花括号")
        起点, 块头 = 块栈.pop()
        块内容 = 内容[起点 : 匹配.start()]
        if 块头.startswith("location ") and re.search(
            r"proxy_pass\s+http://(?:api:8080|molin_api)\s*;", 块内容
        ):
            反代块.append(块内容)

    if 块栈:
        raise ValueError("存在未闭合的配置块")
    return 反代块


def 校验单个配置(路径: Path) -> list[str]:
    """确保每个 API 上游入口覆盖单值来源，并明确删除两种转发链头。"""
    内容 = 路径.read_text(encoding="utf-8")
    错误: list[str] = []

    if "$proxy_add_x_forwarded_for" in 内容:
        错误.append("仍使用 $proxy_add_x_forwarded_for 追加客户端转发链")

    if 路径.name in 控制台配置文件:
        错误.extend(校验控制台全方法拒绝(内容))

    # 三类公开入口都必须在通用 /api/ 反代前显式截断内部 bootstrap 路径。
    拒绝模式 = re.compile(
        r"location\s+\^~\s+/api/internal/email/bootstrap/admin-verify\s*\{"
        r"(?P<body>.*?)\}",
        flags=re.DOTALL,
    )
    拒绝块 = list(拒绝模式.finditer(内容))
    if len(拒绝块) != 1:
        错误.append(f"bootstrap 公开拒绝入口数量为 {len(拒绝块)}，应为 1")
    else:
        拒绝内容 = 拒绝块[0].group("body")
        if not re.search(r"^\s*return\s+404\s*;\s*$", 拒绝内容, flags=re.MULTILINE):
            错误.append("bootstrap 公开拒绝入口未固定返回 404")
        if "proxy_pass" in 拒绝内容:
            错误.append("bootstrap 公开拒绝入口禁止配置 proxy_pass")

    try:
        反代块 = 提取反代块(内容)
    except ValueError as 异常:
        return [str(异常)]

    if not 反代块:
        错误.append("未找到 API 上游反代入口")
        return 错误

    必需指令 = {
        "X-Real-IP": r"^\s*proxy_set_header\s+X-Real-IP\s+\$remote_addr\s*;\s*$",
        "X-Forwarded-For": r'^\s*proxy_set_header\s+X-Forwarded-For\s+""\s*;\s*$',
        "Forwarded": r'^\s*proxy_set_header\s+Forwarded\s+""\s*;\s*$',
    }
    for 序号, 块内容 in enumerate(反代块, start=1):
        for 头名称, 模式 in 必需指令.items():
            命中数 = len(re.findall(模式, 块内容, flags=re.MULTILINE))
            if 命中数 != 1:
                错误.append(f"第 {序号} 个 API 反代入口的 {头名称} 安全指令数量为 {命中数}，应为 1")

    return 错误


def 主函数() -> int:
    """扫描固定三份配置并输出适合本地或 CI 使用的结果。"""
    全部错误: list[str] = []
    for 文件名 in 配置文件:
        路径 = 配置目录 / 文件名
        if not 路径.is_file():
            全部错误.append(f"{文件名}: 配置文件不存在")
            continue
        for 错误 in 校验单个配置(路径):
            全部错误.append(f"{文件名}: {错误}")

    if 全部错误:
        print("Nginx 来源头静态校验失败：", file=sys.stderr)
        for 错误 in 全部错误:
            print(f"- {错误}", file=sys.stderr)
        return 1

    print(
        "Nginx 静态校验通过：admin/user 均具备 bootstrap 全方法 404 与 TRACE 精确归一化，"
        "三个公开入口的其他 API 反代均覆盖 X-Real-IP 并删除 XFF/Forwarded。"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(主函数())
