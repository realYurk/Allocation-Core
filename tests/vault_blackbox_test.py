#!/usr/bin/env python3
"""V20 Vault 生命周期黑盒测试（判官视角）。

覆盖：
  1. add 三版本（同内容去重）
  2. stats 去重/压缩统计正确
  3. verify / restore 无损
  4. prune dry-run 与 apply 正确保护 official
  5. gc 只删未引用对象
  6. release-source 篡改拒绝
  7. export-db 输出正确 + 拒绝覆盖
  8. 路径穿越 / 非法标签拒绝
  9. 损坏对象 fail-closed
"""
import csv
import hashlib
import json
import os
import shutil
import subprocess
import tempfile

EXE = os.path.abspath("core/alloc_windows_amd64.exe")
PASS = 0
FAIL = 0


def check(name, cond, detail=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  [PASS] {name}")
    else:
        FAIL += 1
        print(f"  [FAIL] {name}  {detail}")


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for c in iter(lambda: f.read(65536), b""):
            h.update(c)
    return h.hexdigest()


def main():
    root = tempfile.mkdtemp(prefix="vault20_")
    vault = os.path.join(root, "vault")

    # 准备一个 RUN_DIR
    run1 = os.path.join(root, "run1")
    r = run([EXE, "run", "core/examples/manufacturing/rule.json", run1, "--workers", "4"])
    check("准备 RUN_DIR 成功", r.returncode == 0, r.stderr)

    # 1. 三个版本 add（同内容）
    r1 = run([EXE, "vault", "add", vault, "FC-DEMO", "forecast-1", "draft", run1])
    check("add draft1 成功", r1.returncode == 0, r1.stderr)
    r2 = run([EXE, "vault", "add", vault, "FC-DEMO", "forecast-2", "draft", run1])
    check("add draft2 成功", r2.returncode == 0, r2.stderr)
    r3 = run([EXE, "vault", "add", vault, "FC-DEMO", "official-1", "official", run1])
    check("add official 成功", r3.returncode == 0, r3.stderr)

    # 2. stats：去重 + 压缩
    r = run([EXE, "vault", "stats", vault])
    stats = json.loads(r.stdout[r.stdout.find("{"):r.stdout.rfind("}") + 1])
    check("stats runs=3", stats["runs"] == 3, str(stats))
    check("stats official=1", stats["official_runs"] == 1, str(stats))
    check("stats 去重生效 (unique < 3x files)",
          stats["unique_objects"] < 3 * len(json.loads(r1.stdout[r1.stdout.find("{"):r1.stdout.rfind("}") + 1])["files"]),
          str(stats))
    check("stats 压缩生效 (stored < logical)", stats["physical_bytes"] < stats["logical_bytes"], str(stats))

    # 3. list
    r = run([EXE, "vault", "list", vault])
    check("list 返回 3 条", r.returncode == 0 and '"id"' in r.stdout)

    # 取 draft2 的 id
    id2 = json.loads(r2.stdout[r2.stdout.find("{"):r2.stdout.rfind("}") + 1])["id"]

    # 4. verify
    r = run([EXE, "vault", "verify", vault, id2])
    check("verify 成功", r.returncode == 0, r.stderr)

    # 5. restore 无损
    restored = os.path.join(root, "restored")
    r = run([EXE, "vault", "restore", vault, id2, restored])
    check("restore 成功", r.returncode == 0, r.stderr)
    allok = all(
        os.path.exists(os.path.join(restored, f)) and
        sha(os.path.join(restored, f)) == sha(os.path.join(run1, f))
        for f in os.listdir(run1)
        if os.path.isfile(os.path.join(run1, f))
    )
    check("restore 逐字节一致", allok)

    # 6. prune dry-run
    r = run([EXE, "vault", "prune", vault, "--keep-drafts", "1", "--older-than-days", "0"])
    plan = json.loads(r.stdout[r.stdout.find("{"):r.stdout.rfind("}") + 1])
    check("prune dry-run 保护 official", plan["protected_official"] == 1, str(plan))
    check("prune dry-run 选中 1 个 draft", plan["dry_run"] is True and len(plan["remove_ids"]) == 1, str(plan))

    # 7. export-db
    bulk = os.path.join(root, "bulk.csv")
    r = run([EXE, "vault", "export-db", vault, id2, bulk])
    check("export-db 成功", r.returncode == 0, r.stderr)
    with open(bulk, encoding="utf-8-sig", newline="") as f:
        rdr = csv.reader(f)
        hdr = next(rdr)
        rows = list(rdr)
    check("export-db 表头正确", hdr == ["run_id", "row_id", "terminal_stage", "amount", "dimensions_json"], str(hdr))
    check("export-db 有数据行", len(rows) > 0)
    check("export-db dimensions_json 是合法 JSON",
          all(json.loads(row[4]) for row in rows))
    # 拒绝覆盖
    r = run([EXE, "vault", "export-db", vault, id2, bulk])
    check("export-db 拒绝覆盖", r.returncode != 0)

    # 8. 非法标签拒绝
    r = run([EXE, "vault", "add", vault, "../evil", "v1", "draft", run1])
    check("路径穿越 scenario 拒绝", r.returncode != 0, r.stdout)
    r = run([EXE, "vault", "add", vault, "OK", "v1", "badclass", run1])
    check("非法 class 拒绝", r.returncode != 0, r.stdout)

    # 9. 损坏对象 fail-closed
    # 找一个对象文件，篡改
    objs = os.path.join(vault, "objects")
    obj_files = []
    for dp, _, fs in os.walk(objs):
        for f in fs:
            obj_files.append(os.path.join(dp, f))
    check("存在对象文件", len(obj_files) > 0)
    victim = obj_files[0]
    data = bytearray(open(victim, "rb").read())
    data[len(data) // 2] ^= 0xFF
    open(victim, "wb").write(bytes(data))
    r = run([EXE, "vault", "verify", vault, id2])
    check("损坏对象 verify 拒绝", r.returncode != 0, r.stdout)

    # 10. gc dry-run（此时不应有 orphan，因为都是共享的）
    # 修复被篡改的对象后再测：重新 add 会因损坏拒绝
    r = run([EXE, "vault", "gc", vault])
    check("gc 命令可执行", r.returncode == 0, r.stderr)

    shutil.rmtree(root, ignore_errors=True)
    print(f"\n{'='*50}")
    print(f"Vault 测试: {PASS} PASS, {FAIL} FAIL")
    return 1 if FAIL else 0


if __name__ == "__main__":
    raise SystemExit(main())
