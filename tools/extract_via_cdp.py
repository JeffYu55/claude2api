#!/usr/bin/env python3
"""
claude.ai sessionKey 提取工具 —— OAuth / 独立配置档方案

为什么是这个方案（都是实测结论）：
  * Chrome 127+ 的 v20 App-Bound 加密，离线解密不可行
  * 把已有配置档复制到别处，Chrome 会丢弃那些 cookie（ABE 绑定原始路径）
  * 对默认配置档开 --remote-debugging-port，Chrome 会拒绝
  * 但：**用独立配置档启动的浏览器，它自己写入的 cookie 它能自己解密**
    → CDP 的 Storage.getCookies 直接返回明文，HttpOnly 也拿得到

所以流程是：
  1. 用独立配置档启动一个浏览器实例（不动你日常用的 Chrome）
  2. 打开 claude.ai/login，你随便用哪种方式登录
     —— Google OAuth / Apple / 邮箱验证码都行，OAuth 也完全没问题
  3. 工具轮询直到 sessionKey 出现
  4. 可选：直接导入 claude2api
  5. 配置档保留，下次免登录

用法：
  python tools/extract_via_cdp.py                  # 交互式登录并提取
  python tools/extract_via_cdp.py --import         # 提取后直接导入
  python tools/extract_via_cdp.py --browser edge   # 用 Edge
  python tools/extract_via_cdp.py --timeout 600    # 等 10 分钟
  python tools/extract_via_cdp.py --reset-profile  # 清掉上次登录状态

导入环境变量：
  C2A_BASE      默认 https://claude.onlinespace.website
  C2A_PASSWORD  管理密码
"""
import argparse
import asyncio
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

import aiohttp

CHROME_CANDIDATES = [
    r"%ProgramFiles%\Google\Chrome\Application\chrome.exe",
    r"%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe",
    r"%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe",
]
EDGE_CANDIDATES = [
    r"%ProgramFiles%\Microsoft\Edge\Application\msedge.exe",
    r"%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe",
]

LOGIN_URL = "https://claude.ai/login"


def find_exe(browser: str) -> str | None:
    for c in (CHROME_CANDIDATES if browser == "chrome" else EDGE_CANDIDATES):
        p = os.path.expandvars(c)
        if os.path.isfile(p):
            return p
    return None


def profile_dir(browser: str) -> str:
    """独立配置档：放在临时目录，不碰用户日常浏览器数据。"""
    return os.path.join(tempfile.gettempdir(), f"c2a-login-{browser}")


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_devtools(port: int, timeout: float = 40.0) -> str | None:
    url = f"http://127.0.0.1:{port}/json/version"
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as r:
                return json.loads(r.read()).get("webSocketDebuggerUrl")
        except Exception:
            time.sleep(0.4)
    return None


async def cdp(ws_url: str, method: str, params: dict | None = None, timeout: float = 30):
    async with aiohttp.ClientSession() as sess:
        async with sess.ws_connect(ws_url, max_msg_size=64 * 1024 * 1024) as ws:
            await ws.send_json({"id": 1, "method": method, "params": params or {}})
            while True:
                msg = await asyncio.wait_for(ws.receive_json(), timeout=timeout)
                if msg.get("id") == 1:
                    if "error" in msg:
                        raise RuntimeError(msg["error"])
                    return msg.get("result", {})


def launch(exe: str, data_dir: str, port: int) -> subprocess.Popen:
    """独立配置档 + 调试端口。路径含空格时由 Python 负责加引号。"""
    return subprocess.Popen([
        exe,
        f"--user-data-dir={data_dir}",
        f"--remote-debugging-port={port}",
        "--remote-allow-origins=*",
        "--no-first-run",
        "--no-default-browser-check",
        LOGIN_URL,
    ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def wait_for_session_key(ws_url: str, timeout: int) -> str | None:
    deadline = time.time() + timeout
    tick = 0
    while time.time() < deadline:
        try:
            result = asyncio.run(cdp(ws_url, "Storage.getCookies"))
            cookies = result.get("cookies", [])
        except Exception as e:
            print(f"  读取异常（重试）: {e}")
            time.sleep(2)
            continue

        for c in cookies:
            if c.get("name") == "sessionKey" and c.get("value"):
                return c["value"]

        remain = int(deadline - time.time())
        if tick % 5 == 0:
            names = sorted({c["name"] for c in cookies if "claude" in c.get("domain", "")})
            print(f"  等待登录… 剩余 {remain}s | claude cookie: {', '.join(names) or '（暂无）'}")
        tick += 1
        time.sleep(2)
    return None


def do_import(key: str) -> None:
    base = os.environ.get("C2A_BASE", "https://claude.onlinespace.website")
    pw = os.environ.get("C2A_PASSWORD", "")
    if not pw:
        print("\n导入需要环境变量 C2A_PASSWORD（管理密码）")
        return
    payload = json.dumps({"session_keys": key}).encode()
    req = urllib.request.Request(
        base.rstrip("/") + "/api/accounts/import",
        data=payload,
        headers={"Authorization": "Bearer " + pw, "Content-Type": "application/json"},
        method="POST",
    )
    print(f"\n正在导入到 {base} …")
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            for raw in resp:
                line = raw.decode("utf-8", "replace").rstrip("\n")
                if line.startswith("data:"):
                    print("  " + line[5:].strip())
    except Exception as e:
        print(f"导入失败: {e}")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--browser", default="chrome", choices=["chrome", "edge"])
    ap.add_argument("--import", dest="do_import", action="store_true")
    ap.add_argument("--timeout", type=int, default=300, help="等待登录秒数，默认 300")
    ap.add_argument("--reset-profile", action="store_true", help="清除上次登录状态")
    ap.add_argument("--keep-open", action="store_true", help="提取后不关闭浏览器")
    args = ap.parse_args()

    exe = find_exe(args.browser)
    if not exe:
        print(f"未找到 {args.browser}")
        return 1

    data_dir = profile_dir(args.browser)
    if args.reset_profile and os.path.isdir(data_dir):
        shutil.rmtree(data_dir, ignore_errors=True)
        print(f"已清除配置档: {data_dir}")

    port = free_port()
    print(f"浏览器  : {exe}")
    print(f"独立配置档: {data_dir}")
    print(f"调试端口: {port}\n")
    print("即将打开 claude.ai 登录页。")
    print("请用任意方式登录（Google OAuth / Apple / 邮箱验证码均可）。")
    print(f"最多等待 {args.timeout} 秒。\n")

    proc = launch(exe, data_dir, port)
    key = None
    try:
        ws = wait_devtools(port)
        if not ws:
            print("调试端口未就绪。可能原因：")
            print("  - 该配置档已被另一个实例占用（先关掉工具开的那个浏览器窗口）")
            print("  - 安全软件拦截了本地端口")
            return 1
        print("DevTools 已连接，等待你完成登录…\n")
        key = wait_for_session_key(ws, args.timeout)
    finally:
        if not args.keep_open:
            try:
                proc.terminate()
                proc.wait(timeout=8)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass

    if not key:
        print("\n未捕获到 sessionKey。")
        print("检查：登录是否真的完成（页面进入对话界面才算）、是否用了别的浏览器窗口。")
        print("重试：加 --timeout 600 延长等待，或 --reset-profile 清状态。")
        return 1

    print(f"\n✅ 捕获成功，sessionKey 长度 {len(key)}")
    print(f"\n{key}\n")
    print("该配置档已保留，下次运行无需重新登录。")

    if args.do_import:
        do_import(key)
    else:
        print("\n直接导入的命令：")
        print(f'  C2A_PASSWORD=<管理密码> python tools/extract_via_cdp.py --import')
        print("\n或手动 curl：")
        print(f'  curl -N -X POST $B/api/accounts/import \\\n'
              f'    -H "Authorization: Bearer $PW" -H "Content-Type: application/json" \\\n'
              f'    -d \'{{"session_keys":"{key}"}}\'')
    return 0


if __name__ == "__main__":
    sys.exit(main())
