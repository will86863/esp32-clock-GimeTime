#!/usr/bin/env python3
"""把历史上误记到 Tomoyo After 的会话改判为 CLANNAD SS。

背景：v1.2.0 之前只按进程名匹配，两个游戏的 exe 都叫 RealLiveEn.exe，
一律被记到配置里第一个同名条目（Tomoyo After）名下。
用户确认这 4 条实际都是 CLANNAD Side Stories。
"""
import json
import os
import sys

path = os.path.join(os.environ["APPDATA"], "GameTime", "sessions.jsonl")
CLANNAD_ID = "646366ca64d008ae"
CLANNAD_DISPLAY = "CLANNAD SS"
PROCESS = "RealLiveEn.exe"

changed = 0
out = []
with open(path, "r", encoding="utf-8") as f:
    for line in f:
        s = line.strip()
        if not s:
            continue
        try:
            r = json.loads(s)
        except json.JSONDecodeError:
            out.append(line)
            continue
        if r.get("process") == PROCESS and r.get("display") == "Tomoyo After":
            r["game_id"] = CLANNAD_ID
            r["display"] = CLANNAD_DISPLAY
            changed += 1
            out.append(json.dumps(r, ensure_ascii=False) + "\n")
        else:
            out.append(line if line.endswith("\n") else line + "\n")

with open(path, "w", encoding="utf-8", newline="") as f:
    f.writelines(out)

print(f"改判 {changed} 条为 {CLANNAD_DISPLAY}")
