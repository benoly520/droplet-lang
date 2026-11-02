#!/usr/bin/env python3
"""变异覆盖分：每一条断言都要能被“弄红”，否则它只是摆设。

三个方向，缺一不可：

  L（验收表自检）—— 把第 N 条断言的期望值改成不可能成立，跑整张表。
     必须恰好红 #N、其余全绿。红别的 = 表之间有隐藏耦合；不红 = 这条断言
     根本没被执行（或者期望值与实际值无关）。

  E（样例自检）—— 把 DP_EXAMPLES 第 N 条的期望改成不可能成立，跑 oracle。
     必须恰好挂 #N。这一相管的是“样例是不是真判据”：样例只在决定层跑，
     没有 L 那样的服务端响应可看，最容易退化成摆设。

  S（实现灵敏度）—— 在源码里注入“看起来像真 bug”的改动，逐条跑验收表。
     事先声明这条变异**应该**弄红哪几条断言；实际红集合必须包含声明的集合，
     否则归因不符（红在别处 = 我们的解释不成立，不算抓住）。
     每条变异都在自己的临时树里构建，绝不碰工作区。

覆盖分 = 至少被一条 S 变异弄红的断言数 / 断言总数（从验收表数出来，不写死）。低于 floor 就是红。
floor 默认 30 = 断言总数（v0.37 起钉死）：加一条断言就得同时交出“能把它弄红的那一改”，否则覆盖分红。
想留余地就显式传更小的 --floor，但那等于当场承认有几条断言是摆设。
它证明的是“断言能分辨实现的好坏”，不证明“实现一定对”——后者要靠需求条款本身正确。

最后一相对账（v0.37）：req/TRANS.tsv 的“证据”列说这条反例由 S<n>/E<n> 管，
这里就拿实测结果查它 —— 那个变异必须真的弄红**本条款声明的**某条断言，那个样例必须真的活。
说管不住 = 红。于是“反例”从一句措辞变成一张有对账人的账。

用法：
  python3 code/sup/mutation-score.py [--binary BIN] [--phase L|S|E|all] [--floor 30]
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
DECIDE_H = os.path.join(ROOT, "code", "sup", "dp_decide.h")
REQ_T = os.path.join(ROOT, "req", "REQ.tsv")
TRANS_T = os.path.join(ROOT, "req", "TRANS.tsv")
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
    # 新增条款（S14/R17）自己也要能被弄红：否则 #27 只是摆设。
    dict(id="S21", note="清单退回裸数组（外壳没了）", expect={27}, edits=[
        ("code/runtime/rt_blob.c", '  const char* pfx = "{\\"keys\\":[";\n  size_t pfx_n = 9u;',
         '  const char* pfx = "[";\n  size_t pfx_n = 1u;'),
        ("code/runtime/rt_blob.c", "  buf[o++] = ']';\n  buf[o++] = '}';", "  buf[o++] = ']';")]),
    dict(id="S22", note="清单排完又倒过来（顺序承诺被破坏）", expect={27}, edits=[
        ("code/runtime/rt_blob.c", "  qsort(names, cnt, sizeof(dp_str), cmp_str);",
         "  qsort(names, cnt, sizeof(dp_str), cmp_str);\n"
         "  for (size_t i = 0; i < cnt / 2u; i++) {\n"
         "    dp_str t = names[i];\n    names[i] = names[cnt - 1u - i];\n"
         "    names[cnt - 1u - i] = t;\n  }")]),
    # v0.37 的两条新断言也得能被弄红：#29 是“空目录也是合法对象”，#28 是 Content-Type。
    dict(id="S23", note="空清单发空 body（外壳只在有元素时才算数）", expect={29}, edits=[
        ("code/runtime/rt_blob.c", "  out.n = o;", "  out.n = cnt == 0u ? 0u : o;")]),
    dict(id="S24", note="成功投影声明 text/plain（JSON 承诺没了）", expect={28}, edits=[
        ("code/sup/dp_sup.h", '  out->content_type = dp_str_lit("application/json");',
         '  out->content_type = dp_str_lit("text/plain; charset=utf-8");')]),
    # R19/#30：目录类型这半边判掉，服务就敢带着一个普通文件当数据目录开机。
    dict(id="S25", note="数据目录不做目录检查（带病开机，每个请求都废）", expect={30}, edits=[
        ("code/runtime/rt_http.c", "  if (stat(rootbuf, &st) != 0 || !S_ISDIR(st.st_mode)) {",
         "  if (stat(rootbuf, &st) != 0) {")]),
]


# --------------------------------------------------------------------------

def sh(cmd, cwd, timeout, env=None):
    e = dict(os.environ)
    if env:
        e.update(env)
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True,
                          timeout=timeout, env=e)


def sample_ids(dec_text):
    return sorted(int(m) for m in re.findall(r"^\s*X\((\d+),", dec_text, re.M))


def flip_example(text, eid):
    """把 DP_EXAMPLES 第 eid 行的期望取反（`(!(期望))`）。锚点必须唯一，否则是假变异。

    样例行有两种排版：期望跟在这一行 `), ` 后面，或者因为折行落到下一行的续行里
    （`X(10, ...),  \\` 换行 `DP_ERR_PRECONDITION_FAILED, "..."`）。两种都要认。
    """
    pat = re.compile(r"^([ \t]*X\(%d, .*?\),[ \t]*(?:\\\n[ \t]*)?)([^,\n]+)(,)" % eid, re.M | re.S)
    hits = pat.findall(text)
    if len(hits) != 1:
        return None, len(hits)
    return pat.sub(lambda m: "%s(!(%s))%s" % (m.group(1), m.group(2), m.group(3)),
                   text, count=1), 1


def ledger():
    """读 REQ.tsv + TRANS.tsv：每条条款声明的断言/样例，和它认领的反例 catcher。"""
    def rows(path, ncol):
        out = []
        for line in open(path, encoding="utf-8"):
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            f = [x.strip() for x in line.rstrip("\n").split("\t")]
            if len(f) == ncol:
                out.append(f)
        return out

    def cell(s):
        return [] if s in ("", "-") else [x.strip() for x in s.split(",") if x.strip()]

    claim = {}
    for f in rows(REQ_T, 7):
        claim[f[0]] = (set(cell(f[5])), set(cell(f[6])), f[3])
    ev = [(f[0], cell(f[5])) for f in rows(TRANS_T, 6)]
    return claim, ev


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
    ap.add_argument("--phase", choices=["L", "S", "E", "all"], default="all")
    ap.add_argument("--l-port", type=int, default=18300)
    ap.add_argument("--s-port", type=int, default=18400)
    ap.add_argument("--floor", type=int, default=30)
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
    e_live = set()
    measured = {}

    # 断言总数从验收表里数出来，不写死：加一条断言不该改这个脚本。
    nassert = max((int(m) for m in re.findall(r"^ck (\d+) ", table, re.M)), default=0)
    if not nassert:
        print("mutation-score: 验收表里没数出断言编号", file=sys.stderr)
        return 2

    # ---- 参照必须先全绿：否则下面的“红”都不可归因 ----
    ref = accept(binary, TABLE, "msref", args.l_port, ROOT)
    ok, bad = parse_accept(ref.stdout + ref.stderr)
    if ref.returncode != 0 or bad or len(ok) != nassert:
        problems.append("参照不绿：%d 过 / %d 挂（先修实现，再谈变异）"
                        % (len(ok), len(bad)))
        print("\n".join(problems), file=sys.stderr)
        return 1
    print("mutation-score：参照 %d/%d 绿" % (nassert, nassert))

    if args.phase in ("L", "all"):
        tree = fresh_tree("L")
        tpath = os.path.join(tree, "code", "webapi", "deploy", "accept-table.sh")
        orig_mode = os.stat(tpath).st_mode
        print("\n== L：验收表自检（改期望值，必须恰好红它自己）==")
        for n in range(1, nassert + 1):
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
            elif b == {n} and len(o) == nassert - 1:
                l_live += 1
                print("  L #%-2d 活（红 #%d，其余 %d 绿）" % (n, n, nassert - 1))
            else:
                problems.append("L #%d：期望红 {%d}，实际红 %s（绿 %d）"
                                % (n, n, sorted(b), len(o)))
                print("  L #%-2d 归因不符：红 %s" % (n, sorted(b)))
        # 恢复原表，避免后续误用
        with open(tpath, "w", encoding="utf-8") as f:
            f.write(table)
        os.chmod(tpath, orig_mode)
        print("  L 小计：%d/%d 条断言“改期望必红”" % (l_live, nassert))

    if args.phase in ("E", "all"):
        print("\n== E：样例自检（改期望必挂，且只挂它自己）==")
        dec = open(DECIDE_H, encoding="utf-8").read()
        for n in sample_ids(dec):
            tree = fresh_tree("E%d" % n)
            flipped, cnt = flip_example(dec, n)
            if flipped is None:
                problems.append("E #%d：样例行在 dp_decide.h 里找到 %d 处（假变异）" % (n, cnt))
                print("  E #%-2d 假变异" % n)
                continue
            hp = os.path.join(tree, "code", "sup", "dp_decide.h")
            with open(hp, "w", encoding="utf-8") as f:
                f.write(flipped)
            r = sh(["sh", os.path.join(tree, "code", "sup", "oracle-sup.sh"), "me%d" % n],
                   tree, 180)
            o, b = parse_accept(r.stdout + r.stderr)
            e_live.add(n)
            if b == {n}:
                print("  E #%-2d 活（挂 #%d，其余 %d 过）" % (n, n, len(o)))
            else:
                e_live.discard(n)
                problems.append("E #%d：期望取反后应当只挂 #%.2d，实际挂 %s（过 %d，退出码 %d）"
                                % (n, n, sorted(b), len(o), r.returncode))
                print("  E #%-2d 归因不符：挂 %s" % (n, sorted(b)))
        print("  E 小计：%d/%d 条样例“改期望必挂”" % (len(e_live), len(sample_ids(dec))))

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
            measured[m["id"]] = set(bad)
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

    cover = sorted(covered & set(range(1, nassert + 1)))
    if args.phase == "all":
        print("\n== 台账对账：TRANS 的证据列说谁管这条反例，实测就得是谁 ==")
        claim, ev = ledger()
        ids = set()
        for rid, cell in ev:
            a_claim, e_claim, typ = claim.get(rid, (set(), set(), ""))
            for x in cell:
                ids.add(x)
                if x.startswith("S"):
                    mid = x
                    if mid not in measured:
                        problems.append("%s 认领了 %s，但变异清单里没有它（或它编不过，实测无从谈起）"
                                        % (rid, x))
                        continue
                    hit = measured[mid] & {int(v) for v in a_claim}
                    if not hit:
                        problems.append("%s 说反例由 %s 管，但 %s 实测弄红 %s，跟本条款声明的断言 %s 无交集"
                                        % (rid, x, x, sorted(measured[mid]), sorted(int(v) for v in a_claim)))
                elif x.startswith("E"):
                    n = int(x[1:])
                    if x[1:] not in e_claim:
                        problems.append("%s 认领了样例 %s，却没在 REQ 的“样例”栏声明它" % (rid, x))
                    if n not in e_live:
                        problems.append("%s 认领了样例 %s，但这一条在 E 相里没通过“改期望必挂”" % (rid, x))
        for x in set(m["id"] for m in MUTS) - ids:
            print("  提示：%s 没被任何条款的反例认领（覆盖分算它，但它不是一条写下来的语义差异）" % x)
    if args.phase in ("S", "all"):
        print("\n断言覆盖：%d/%d 至少被一条实现变异弄红（floor %d）"
              % (len(cover), nassert, args.floor))
        if len(cover) < args.floor:
            miss = sorted(set(range(1, nassert + 1)) - covered)
            problems.append("覆盖分低于 floor：%d < %d；没被弄红的断言 %s"
                            % (len(cover), args.floor, miss))
    if problems:
        print("\n红：", file=sys.stderr)
        for p in problems:
            print("  - " + p, file=sys.stderr)
        return 1
    parts = []
    if args.phase in ("L", "all"):
        parts.append("L %d/%d" % (l_live, nassert))
    if args.phase in ("E", "all"):
        parts.append("E %d/%d" % (len(e_live), len(sample_ids(open(DECIDE_H, encoding="utf-8").read()))))
    if args.phase in ("S", "all"):
        parts.append("S %d/%d" % (s_ok, len(MUTS)))
        parts.append("覆盖 %d/%d" % (len(cover), nassert))
    print("mutation-score：绿（%s）" % "；".join(parts))
    return 0


if __name__ == "__main__":
    sys.exit(main())
