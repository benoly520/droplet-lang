/* Droplet 运行时 · 文件哑存储
 *
 * 契约：服务端只做字节读写，不解构 payload。
 * key 的合法性是安全边界，必须在任何路径拼接之前校验。
 */
#include "rt.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#define DP_KEY_MAX 32u
#define DP_SNAP_DIR ".snapshots"
#define DP_SNAPSHOT_DEFAULT_KEEP 8

/* 每个 key 保留几份快照。0 = 不轮转（留全量历史）。
 * 单线程服务，读一次缓存即可。 */
static int snapshot_keep(void) {
  static int cached = -1;
  if (cached >= 0) {
    return cached;
  }
  cached = DP_SNAPSHOT_DEFAULT_KEEP;
  const char* v = getenv("DP_SNAPSHOT_KEEP");
  if (v && v[0] != '\0') {
    char* end = NULL;
    long n = strtol(v, &end, 10);
    if (end == v || *end != '\0' || n < 0 || n > 100000) {
      fprintf(stderr, "droplet: DP_SNAPSHOT_KEEP=%s 不是 0..100000 的整数，回退 %d\n",
              v, DP_SNAPSHOT_DEFAULT_KEEP);
    } else {
      cached = (int)n;
    }
  }
  return cached;
}

bool dp_valid_key(dp_str key) {
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
}

/* 拼接 root/[sub/]key[suffix][extra]，结果以 NUL 结尾。
 * 调用前必须保证 key 已通过 dp_valid_key —— 这里不再做路径检查。 */
static char* make_path(dp_arena* a, dp_str root, const char* sub, dp_str key,
                       const char* suffix, const char* extra) {
  size_t sub_n = sub ? strlen(sub) : 0u;
  size_t suf_n = suffix ? strlen(suffix) : 0u;
  size_t ex_n = extra ? strlen(extra) : 0u;
  size_t n = root.n + 1u + (sub_n ? sub_n + 1u : 0u) + key.n + suf_n + ex_n + 1u;

  char* p = (char*)dp_alloc(a, n);
  size_t o = 0;
  memcpy(p + o, root.p, root.n);
  o += root.n;
  p[o++] = '/';
  if (sub_n > 0u) {
    memcpy(p + o, sub, sub_n);
    o += sub_n;
    p[o++] = '/';
  }
  memcpy(p + o, key.p, key.n);
  o += key.n;
  if (suf_n > 0u) {
    memcpy(p + o, suffix, suf_n);
    o += suf_n;
  }
  if (ex_n > 0u) {
    memcpy(p + o, extra, ex_n);
    o += ex_n;
  }
  p[o] = '\0';
  return p;
}

dp_bytes dp_blob_read(dp_arena* a, dp_str root, dp_str key, int* err) {
  dp_bytes out;
  out.p = (const uint8_t*)"";
  out.n = 0u;
  if (err) {
    *err = 0;
  }
  if (!dp_valid_key(key)) {
    if (err) {
      *err = (int)DP_ERR_BAD_REQUEST;
    }
    return out;
  }

  char* path = make_path(a, root, NULL, key, NULL, NULL);
  int fd = open(path, O_RDONLY);
  if (fd < 0) {
    if (err) {
      *err = (errno == ENOENT) ? (int)DP_ERR_NOT_FOUND : (int)DP_ERR_IO;
    }
    return out;
  }

  struct stat st;
  if (fstat(fd, &st) < 0 || st.st_size < 0) {
    close(fd);
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return out;
  }

  size_t want = (size_t)st.st_size;
  uint8_t* buf = (uint8_t*)dp_alloc(a, want + 1u);
  size_t got = 0;
  while (got < want) {
    ssize_t r = read(fd, buf + got, want - got);
    if (r < 0) {
      if (errno == EINTR) {
        continue;
      }
      close(fd);
      if (err) {
        *err = (int)DP_ERR_IO;
      }
      return out;
    }
    if (r == 0) {
      break;
    }
    got += (size_t)r;
  }
  close(fd);

  out.p = buf;
  out.n = got;
  return out;
}

static bool write_file_atomic(dp_arena* a, dp_str root, dp_str key, dp_bytes body, int* err) {
  static unsigned long seq = 0;
  char suffix[64];
  snprintf(suffix, sizeof suffix, ".tmp.%ld.%lu", (long)getpid(), ++seq);

  char* tmp = make_path(a, root, NULL, key, NULL, suffix);
  char* final_path = make_path(a, root, NULL, key, NULL, NULL);

  int fd = open(tmp, O_WRONLY | O_CREAT | O_TRUNC, 0644);
  if (fd < 0) {
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return false;
  }

  size_t off = 0;
  while (off < body.n) {
    ssize_t w = write(fd, body.p + off, body.n - off);
    if (w < 0) {
      if (errno == EINTR) {
        continue;
      }
      close(fd);
      unlink(tmp);
      if (err) {
        *err = (int)DP_ERR_IO;
      }
      return false;
    }
    off += (size_t)w;
  }
  if (fsync(fd) < 0) {
    close(fd);
    unlink(tmp);
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return false;
  }
  if (close(fd) < 0) {
    unlink(tmp);
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return false;
  }
  if (rename(tmp, final_path) < 0) {
    unlink(tmp);
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return false;
  }
  return true;
}

uint64_t dp_blob_write(dp_arena* a, dp_str root, dp_str key, dp_bytes body, int* err) {
  if (err) {
    *err = 0;
  }
  if (!dp_valid_key(key)) {
    if (err) {
      *err = (int)DP_ERR_BAD_REQUEST;
    }
    return 0u;
  }
  if (!write_file_atomic(a, root, key, body, err)) {
    return 0u;
  }
  return dp_sys_hash64(body);
}

/* 只保留每个 key 最近的 keep 份快照。文件名唯一格式：<key>.<unix>.<seq>，
 * 其它一律当杂项忽略（旧格式没有 seq，转轮转时会被跳过）。 */
static void prune_snapshots(dp_arena* a, const char* dir, dp_str key) {
  int keep = snapshot_keep();
  if (keep <= 0) {
    return;
  }

  DIR* d = opendir(dir);
  if (!d) {
    return;
  }

  size_t cap = 16, cnt = 0;
  long long* stamps = (long long*)dp_alloc(a, cap * sizeof(long long));
  unsigned long* seqs = (unsigned long*)dp_alloc(a, cap * sizeof(unsigned long));
  char** names = (char**)dp_alloc(a, cap * sizeof(char*));

  struct dirent* ent;
  while ((ent = readdir(d)) != NULL) {
    const char* nm = ent->d_name;
    if (strncmp(nm, key.p, key.n) != 0 || nm[key.n] != '.') {
      continue;
    }
    char* end = NULL;
    long long ts = strtoll(nm + key.n + 1u, &end, 10);
    if (end == nm + key.n + 1u || *end != '.') {
      continue;
    }
    char* end2 = NULL;
    unsigned long sq = strtoul(end + 1, &end2, 10);
    if (end2 == end + 1 || *end2 != '\0') {
      continue;
    }

    if (cnt == cap) {
      size_t ncap = cap * 2u;
      long long* t2 = (long long*)dp_alloc(a, ncap * sizeof(long long));
      memcpy(t2, stamps, cap * sizeof(long long));
      stamps = t2;
      unsigned long* q2 = (unsigned long*)dp_alloc(a, ncap * sizeof(unsigned long));
      memcpy(q2, seqs, cap * sizeof(unsigned long));
      seqs = q2;
      char** m2 = (char**)dp_alloc(a, ncap * sizeof(char*));
      memcpy(m2, names, cap * sizeof(char*));
      names = m2;
      cap = ncap;
    }
    size_t len = strlen(nm) + 1u;
    names[cnt] = (char*)dp_alloc(a, len);
    memcpy(names[cnt], nm, len);
    stamps[cnt] = ts;
    seqs[cnt] = sq;
    cnt++;
  }
  closedir(d);

  if (cnt <= (size_t)keep) {
    return;
  }

  /* 按 (unix, seq) 升序，最旧的先删；单 key 的快照数很小，插入排序足够 */
  for (size_t i = 1; i < cnt; i++) {
    long long ts = stamps[i];
    unsigned long sq = seqs[i];
    char* nm = names[i];
    size_t j = i;
    while (j > 0 && (stamps[j - 1u] > ts ||
                     (stamps[j - 1u] == ts && seqs[j - 1u] > sq))) {
      stamps[j] = stamps[j - 1u];
      seqs[j] = seqs[j - 1u];
      names[j] = names[j - 1u];
      j--;
    }
    stamps[j] = ts;
    seqs[j] = sq;
    names[j] = nm;
  }

  for (size_t i = 0; i + (size_t)keep < cnt; i++) {
    size_t pn = strlen(dir) + 1u + strlen(names[i]) + 1u;
    char* path = (char*)dp_alloc(a, pn);
    snprintf(path, pn, "%s/%s", dir, names[i]);
    if (unlink(path) < 0 && errno != ENOENT) {
      fprintf(stderr, "droplet: 删旧快照 %s 失败：%s\n", path, strerror(errno));
    }
  }
}

void dp_blob_backup(dp_arena* a, dp_str root, dp_str key) {
  if (!dp_valid_key(key)) {
    return;
  }

  int err = 0;
  dp_bytes cur = dp_blob_read(a, root, key, &err);
  if (err != 0) {
    return; /* 没有旧版本就没有快照，这是正常路径 */
  }

  size_t n = root.n + 1u + sizeof(DP_SNAP_DIR);
  char* snap_dir = (char*)dp_alloc(a, n);
  snprintf(snap_dir, n, "%.*s/%s", (int)root.n, root.p, DP_SNAP_DIR);
  if (mkdir(snap_dir, 0755) < 0 && errno != EEXIST) {
    return;
  }

  /* unix 只到秒：不带 seq 的话，同一秒的两次写会写成同一个文件，
   * 后一份静默盖掉前一份，轮转也就无从谈起。 */
  static unsigned long snap_seq = 0;
  char stamp[48];
  snprintf(stamp, sizeof stamp, ".%lld.%lu", (long long)dp_sys_now_unix(), ++snap_seq);
  char* snap = make_path(a, root, DP_SNAP_DIR, key, NULL, stamp);

  int fd = open(snap, O_WRONLY | O_CREAT | O_TRUNC, 0644);
  if (fd < 0) {
    return;
  }
  size_t off = 0;
  while (off < cur.n) {
    ssize_t w = write(fd, cur.p + off, cur.n - off);
    if (w < 0) {
      if (errno == EINTR) {
        continue;
      }
      break;
    }
    off += (size_t)w;
  }
  close(fd);

  prune_snapshots(a, snap_dir, key);
}

bool dp_blob_create(dp_arena* a, dp_str root, dp_str key, int* err) {
  if (err) {
    *err = 0;
  }
  if (!dp_valid_key(key)) {
    if (err) {
      *err = (int)DP_ERR_BAD_REQUEST;
    }
    return false;
  }

  char* path = make_path(a, root, NULL, key, NULL, NULL);
  int fd = open(path, O_WRONLY | O_CREAT | O_EXCL, 0644);
  if (fd < 0) {
    if (errno == EEXIST) {
      return false; /* 已存在不是错误，是布尔结果 */
    }
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return false;
  }
  close(fd);
  return true;
}

static int cmp_str(const void* a, const void* b) {
  const dp_str* x = (const dp_str*)a;
  const dp_str* y = (const dp_str*)b;
  size_t n = x->n < y->n ? x->n : y->n;
  int c = memcmp(x->p, y->p, n);
  if (c != 0) {
    return c;
  }
  return (x->n > y->n) - (x->n < y->n);
}

dp_bytes dp_blob_list(dp_arena* a, dp_str root, int* err) {
  dp_bytes out;
  out.p = (const uint8_t*)"[]";
  out.n = 2u;
  if (err) {
    *err = 0;
  }

  char* dir_path = (char*)dp_alloc(a, root.n + 1u);
  memcpy(dir_path, root.p, root.n);
  dir_path[root.n] = '\0';

  DIR* d = opendir(dir_path);
  if (!d) {
    if (err) {
      *err = (int)DP_ERR_IO;
    }
    return out;
  }

  size_t cap = 16;
  size_t cnt = 0;
  dp_str* names = (dp_str*)dp_alloc(a, cap * sizeof(dp_str));

  struct dirent* ent;
  while ((ent = readdir(d)) != NULL) {
    const char* nm = ent->d_name;
    if (nm[0] == '.') {
      continue; /* . .. .snapshots 与临时文件都不进清单 */
    }
    size_t len = strlen(nm);
    if (len == 0u || len > DP_KEY_MAX) {
      continue;
    }
    dp_str s;
    s.p = dp_str_dup(a, dp_str_from_cstr(nm)).p;
    s.n = len;
    if (!dp_valid_key(s)) {
      continue; /* 目录里的杂项文件不进清单 */
    }
    if (cnt == cap) {
      dp_str* bigger = (dp_str*)dp_alloc(a, cap * 2u * sizeof(dp_str));
      memcpy(bigger, names, cap * sizeof(dp_str));
      names = bigger;
      cap *= 2u;
    }
    names[cnt++] = s;
  }
  closedir(d);

  qsort(names, cnt, sizeof(dp_str), cmp_str);

  size_t total = 2u + (cnt > 0u ? cnt - 1u : 0u);
  for (size_t i = 0; i < cnt; i++) {
    total += names[i].n + 2u; /* 引号 */
  }
  char* buf = (char*)dp_alloc(a, total + 1u);
  size_t o = 0;
  buf[o++] = '[';
  for (size_t i = 0; i < cnt; i++) {
    if (i > 0u) {
      buf[o++] = ',';
    }
    buf[o++] = '"';
    memcpy(buf + o, names[i].p, names[i].n);
    o += names[i].n;
    buf[o++] = '"';
  }
  buf[o++] = ']';

  out.p = (const uint8_t*)buf;
  out.n = o;
  return out;
}
