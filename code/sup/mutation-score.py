#!/usr/bin/env python3
"""变异覆盖分：每一条断言都要能被“弄红”，否则它只是摆设。

两个方向，缺一不可：

  L（验收表自检）—— 把第 N 条断言的期望值改成不可能成立，跑整张表。
     必须恰好红 #N、其余 25 条绿。红别的 = 表之间有隐藏耦合；不红 = 这条断言
     根本没被执行（或者期望值与实际值无关）。

  S（实现灵敏度）—— 在源码里注入“看起来像真 bug”的改动，逐条跑验收表。
     事先声明这条变异**应该**弄红哪几条断言；实际红集合必须包含声明的集合，
     否则归因不符（红在别处 = 我们的解释不成立，不算抓住）。
     每条变异都在自己的临时树里构建，绝不碰工作区。

覆盖分 = 至少被一条 S 变异弄红的断言数 / 26。低于 floor 就是红。
它证明的是“断言能分辨实现的好坏”，不证明“实现一定对”——后者要靠需求条款本身正确。

用法：
  python3 code/sup/mutation-score.py [--binary BIN] [--phase L|S|all] [--floor 24]
         [--l-port 18300] [--s-port 18400]
不给二进制就先构建参考实现（标签 msref）。
"""
import argparse
import os
import re
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
TABLE = os.path.join(ROOT, "code", "webapi", "deploy", "accept-table.sh")
WORK = os.path.join(ROOT, ".build", "mutation-score")


# --------------------------------------------------------------------------
# 实现变异清单：锚点必须唯一；expect 是“应该被弄红”的断言编号。
# 改动都发生在临时树里，工作区不受影响。
# --------------------------------------------------------------------------

HEALTH_OLD = """static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  *body = dp_bytes_lit("{\\"ok\\":true}");
  return 0;
}"""
HEALTH_NEW = """static int h_health(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  *body = dp_bytes_lit("{\\"ok\\":true}");
  return (int)DP_ERR_IO;
}"""

LIST_OLD = """static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)rq;
  int err = 0;
  *body = dp_blob_list(a, dp_conf.root, &err);
  if (err != 0) {
    return err;
  }
  return 0;
}"""
LIST_ERR = """static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  (void)body;
  return (int)DP_ERR_IO;
}"""
LIST_EMPTY = """static int h_keys_list(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  *body = dp_bytes_lit("{\\"keys\\":[]}");
  return 0;
}"""

GET_OLD = """static int h_blob_get(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  int err = 0;
  *body = dp_blob_read(a, dp_conf.root, rq->key, &err);
  if (err != 0) {
    return err; /* 状态码与空 body 由运行时按 tag 映射，这里不写数字 */
  }
  return 0;
}"""
GET_FIXED = """static int h_blob_get(dp_arena* a, const dp_request* rq, dp_bytes* body) {
  (void)a;
  (void)rq;
  *body = dp_bytes_lit("hello3");
  return 0;
}"""

PRE_OLD = """int dec_precondition_result(int read_err, bool exists, bool tag_matches) {
  if (read_err != 0 && read_err != (int)DP_ERR_NOT_FOUND) {
    return read_err;
  }
  if (!exists) {
    return (int)DP_ERR_PRECONDITION_FAILED;
  }
  return tag_matches ? 0 : (int)DP_ERR_PRECONDITION_FAILED;
}"""
PRE_ALWAYS = """int dec_precondition_result(int read_err, bool exists, bool tag_matches) {
  (void)read_err;
  (void)exists;
  (void)tag_matches;
  return 0;
}"""

CREATE_RET = "  return fresh ? 0 : (int)DP_ERR_CONFLICT;"
CREATE_NOFRESH = "  (void)fresh;\n  return 0;"

MUTS = [
    dict(id="S01", note="health 处理返回 IO 错误", expect={1}, edits=[
        ("code/sup/blobd.c", HEALTH_OLD, HEALTH_NEW)]),
    dict(id="S02", note="清单处理一律报 IO 错误", expect={2, 11, 24}, edits=[
        ("code/sup/blobd.c", LIST_OLD, LIST_ERR)]),
    dict(id="S03", note="204 成功投影改成 200", expect={3, 5, 8}, edits=[
        ("code/sup/dp_sup.h", "    case DP_OK_NO_CONTENT:\n      out->status = 204u;",
         "    case DP_OK_NO_CONTENT:\n      out->status = 200u;")]),
    dict(id="S04", note="成功响应不再发 ETag", expect={4}, edits=[
        ("code/runtime/rt_http.c", "  if (resp.body.n > 0u) {", "  if (false) {")]),
    dict(id="S05", note="条件请求一律放行（忽略 If-Match）", expect={6}, edits=[
        ("code/sup/dp_decide.c", PRE_OLD, PRE_ALWAYS)]),
    dict(id="S06", note="错误响应带上 body", expect={7, 14, 17}, edits=[
        ("code/runtime/rt_http.c",
         '    send_response(fd, status_for_tag(rc), "application/json", (dp_bytes){0}, dp_str_lit(""));',
         '    send_response(fd, status_for_tag(rc), "application/json", dp_bytes_lit("error"), dp_str_lit(""));')]),
    dict(id="S07", note="204 投影改成 200 且带 body（空 body 承诺被破坏）", expect={9}, edits=[
        ("code/sup/dp_sup.h",
         '    case DP_OK_NO_CONTENT:\n      out->status = 204u;\n      out->body = dp_bytes_lit("");',
         '    case DP_OK_NO_CONTENT:\n      out->status = 200u;\n      out->body = dp_bytes_lit("x");')]),
    dict(id="S08", note="201 成功投影改成 200", expect={10}, edits=[
        ("code/sup/dp_sup.h", "    case DP_OK_CREATED:\n      out->status = 201u;",
         "    case DP_OK_CREATED:\n      out->status = 200u;")]),
    dict(id="S09", note="201 投影带上 body（Content-Length 不再为 0）", expect={12}, edits=[
        ("code/sup/dp_sup.h",
         '    case DP_OK_CREATED:\n      out->status = 201u;\n      out->body = dp_bytes_lit("");',
         '    case DP_OK_CREATED:\n      out->status = 201u;\n      out->body = dp_bytes_lit("x");')]),
    dict(id="S10", note="NOT_FOUND 映射改 200", expect={13}, edits=[
        ("code/runtime/rt_http.c", "    case (int)DP_ERR_NOT_FOUND: return 404;",
         "    case (int)DP_ERR_NOT_FOUND: return 200;")]),
    dict(id="S11", note="key 合法性判断失效", expect={15}, edits=[
        ("code/runtime/rt_blob.c", """bool dp_valid_key(dp_str key) {
  if (key.n == 0u || key.n > DP_KEY_MAX) {
    return false;
  }
  for (size_t i = 0; i < key.n; i++) {
    char c = key.p[i];
    bool ok = (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-';
    if (!ok) {
      return false;
    }
  }
  return true;
}""", """bool dp_valid_key(dp_str key) {
  (void)key;
  return true;
}""")]),
    dict(id="S12", note="NOT_ALLOWED 映射改 200", expect={16}, edits=[
        ("code/runtime/rt_http.c", "    case (int)DP_ERR_NOT_ALLOWED: return 405;",
         "    case (int)DP_ERR_NOT_ALLOWED: return 200;")]),
    dict(id="S13", note="错误响应带上 ETag", expect={18}, edits=[
        ("code/runtime/rt_http.c",
         '    send_response(fd, status_for_tag(rc), "application/json", (dp_bytes){0}, dp_str_lit(""));',
         '    send_response(fd, status_for_tag(rc), "application/json", (dp_bytes){0}, dp_str_lit("fixed"));')]),
    dict(id="S14", note="body 上限放大 16 倍", expect={19}, edits=[
        ("code/runtime/rt_http.c",
         "  if (dec_body_too_large(content_len, cfg->max_body)) {",
         "  if (dec_body_too_large(content_len, cfg->max_body * 16u)) {")]),
    dict(id="S15", note="创建忽略“已存在”", expect={20}, edits=[
        ("code/sup/dp_decide.c", CREATE_RET, CREATE_NOFRESH)]),
    dict(id="S16", note="GET 返回固定字节", expect={21}, edits=[
        ("code/sup/blobd.c", GET_OLD, GET_FIXED)]),
    dict(id="S17", note="写之前不做快照备份", expect={22}, edits=[
        ("code/sup/blobd.c", "  dp_blob_backup(a, dp_conf.root, rq->key);",
         "  (void)dp_blob_backup;")]),
    dict(id="S18", note="写入不再触发提交子进程", expect={23}, edits=[
        ("code/sup/blobd.c", "  dp_ver_commit_soon(dp_conf.root, rq->key);",
         "  (void)dp_ver_commit_soon;")]),
    dict(id="S19", note="清单返回常量空数组", expect={24}, edits=[
        ("code/sup/blobd.c", LIST_OLD, LIST_EMPTY)]),
    dict(id="S20", note="鉴权结果算出来但不拦（一律放行）", expect={25, 26}, edits=[
        ("code/runtime/rt_http.c",
         "  if (!dec_auth_ok(cfg->token.n > 0u, is_health, header_present, header_matches)) {",
         "  volatile bool _auth_gate = dec_auth_ok(cfg->token.n > 0u, is_health, header_present, header_matches);\n"
         "  (void)_auth_gate;\n  if (false) {")]),
]


# --------------------------------------------------------------------------

def sh(cmd, cwd, timeout, env=None):
    e = dict(os.environ)
    if env:
        e.update(env)
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True,
                          timeout=timeout, env=e)


def parse_accept(out):
    ok, bad = set(), set()
    for line in out.splitlines():
        m = re.match(r"\s+(ok|FAIL)\s+#(\d+)", line)
        if m:
            (ok if m.group(1) == "ok" else bad).add(int(m.group(2)))
    return ok, bad


def accept(binary, table, label, port, cwd, timeout=180):
    return sh(["sh", table, os.path.abspath(binary), str(port), label], cwd, timeout)


def fresh_tree(name):
    d = os.path.join(WORK, name)
    if os.path.isdir(d):
        shutil.rmtree(d)
    shutil.copytree(os.path.join(ROOT, "code"), os.path.join(d, "code"))
    os.makedirs(os.path.join(d, ".build"), exist_ok=True)
    return d


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="")
    ap.add_argument("--phase", choices=["L", "S", "all"], default="all")
    ap.add_argument("--l-port", type=int, default=18300)
    ap.add_argument("--s-port", type=int, default=18400)
    ap.add_argument("--floor", type=int, default=24)
    args = ap.parse_args()

    os.makedirs(WORK, exist_ok=True)
    table = open(TABLE, encoding="utf-8").read()

    binary = args.binary
    if not binary:
        r = sh(["sh", "code/sup/build.sh", "msref"], ROOT, 180)
        if r.returncode != 0:
            print("mutation-score: 参考实现编不过：\n" + r.stderr[-2000:], file=sys.stderr)
            return 2
        binary = os.path.join(ROOT, ".build", "msref", "blobd")
    if not os.path.isfile(binary):
        print("mutation-score: 找不到二进制 %s" % binary, file=sys.stderr)
        return 2

    problems = []
    l_live = 0
    s_ok = 0
    covered = set()

    # ---- 参照必须先全绿：否则下面的“红”都不可归因 ----
    ref = accept(binary, TABLE, "msref", args.l_port, ROOT)
    ok, bad = parse_accept(ref.stdout + ref.stderr)
    if ref.returncode != 0 or bad or len(ok) != 26:
        problems.append("参照不绿：%d 过 / %d 挂（先修实现，再谈变异）"
                        % (len(ok), len(bad)))
        print("\n".join(problems), file=sys.stderr)
        return 1
    print("mutation-score：参照 26/26 绿")

    if args.phase in ("L", "all"):
        tree = fresh_tree("L")
        tpath = os.path.join(tree, "code", "webapi", "deploy", "accept-table.sh")
        orig_mode = os.stat(tpath).st_mode
        print("\n== L：验收表自检（改期望值，必须恰好红它自己）==")
        for n in range(1, 27):
            pat = re.compile(r"^(ck %d )(\S+)" % n, re.M)
            if len(pat.findall(table)) != 1:
                problems.append("L #%d：验收表里找到 %d 处，断言编号不唯一"
                                % (n, len(pat.findall(table))))
                continue
            with open(tpath, "w", encoding="utf-8") as f:
                f.write(pat.sub(r"\1__IMPOSSIBLE__", table, count=1))
            os.chmod(tpath, orig_mode)
            r = accept(binary, tpath, "ml%d" % n, args.l_port + n, tree)
            o, b = parse_accept(r.stdout + r.stderr)
            if r.returncode == 0:
                problems.append("L #%d：期望改成不可能，整张表仍然全绿 —— 这条断言没被执行" % n)
                print("  L #%-2d 假绿" % n)
            elif b == {n} and len(o) == 25:
                l_live += 1
                print("  L #%-2d 活（红 #%d，其余 25 绿）" % (n, n))
            else:
                problems.append("L #%d：期望红 {%d}，实际红 %s（绿 %d）"
                                % (n, n, sorted(b), len(o)))
                print("  L #%-2d 归因不符：红 %s" % (n, sorted(b)))
        # 恢复原表，避免后续误用
        with open(tpath, "w", encoding="utf-8") as f:
            f.write(table)
        os.chmod(tpath, orig_mode)
        print("  L 小计：%d/26 条断言“改期望必红”" % l_live)

    if args.phase in ("S", "all"):
        print("\n== S：实现变异（声明的红集合必须是实际红集合的子集）==")
        for i, m in enumerate(MUTS, 1):
            tree = fresh_tree(m["id"])
            bad_anchor = False
            for rel, old, new in m["edits"]:
                p = os.path.join(tree, rel)
                src = open(p, encoding="utf-8").read()
                cnt = src.count(old)
                if cnt != 1:
                    problems.append("%s：锚点在 %s 里出现 %d 次（假变异）" % (m["id"], rel, cnt))
                    bad_anchor = True
                    break
                open(p, "w", encoding="utf-8").write(src.replace(old, new, 1))
            if bad_anchor:
                print("  %s 假变异" % m["id"])
                continue

            b = sh(["sh", os.path.join(tree, "code", "sup", "build.sh"), m["id"]], tree, 180)
            binp = os.path.join(tree, ".build", m["id"], "blobd")
            if b.returncode != 0 or not os.path.isfile(binp):
                problems.append("%s：变异后编不过（归因不符）%s"
                                % (m["id"], b.stderr.strip().splitlines()[-1:] or ""))
                print("  %s 编不过" % m["id"])
                continue

            tp = os.path.join(tree, "code", "webapi", "deploy", "accept-table.sh")
            r = accept(binp, tp, "ms" + m["id"], args.s_port + i, tree)
            o, bad = parse_accept(r.stdout + r.stderr)
            missing = m["expect"] - bad
            if missing:
                problems.append("%s：声明应红 %s，实际没红 %s（实际红 %s）"
                                % (m["id"], sorted(m["expect"]), sorted(missing), sorted(bad)))
                print("  %s 归因不符：漏红 %s" % (m["id"], sorted(missing)))
            else:
                s_ok += 1
                covered |= bad
                extra = sorted(bad - m["expect"])
                tail = "，另红 %s" % extra if extra else ""
                print("  %s 抓住：{%s}%s" % (m["id"], ",".join(str(x) for x in sorted(bad)), tail))

        print("  S 小计：%d/%d 条变异归因相符" % (s_ok, len(MUTS)))

    cover = sorted(covered & set(range(1, 27)))
    if args.phase in ("S", "all"):
        print("\n断言覆盖：%d/26 至少被一条实现变异弄红（floor %d）" % (len(cover), args.floor))
        if len(cover) < args.floor:
            miss = sorted(set(range(1, 27)) - covered)
            problems.append("覆盖分低于 floor：%d < %d；没被弄红的断言 %s"
                            % (len(cover), args.floor, miss))
    if problems:
        print("\n红：", file=sys.stderr)
        for p in problems:
            print("  - " + p, file=sys.stderr)
        return 1
    parts = []
    if args.phase in ("L", "all"):
        parts.append("L %d/26" % l_live)
    if args.phase in ("S", "all"):
        parts.append("S %d/%d" % (s_ok, len(MUTS)))
        parts.append("覆盖 %d/26" % len(cover))
    print("mutation-score：绿（%s）" % "；".join(parts))
    return 0


if __name__ == "__main__":
    sys.exit(main())
