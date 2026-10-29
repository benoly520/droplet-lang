/* Droplet 运行时 · core：arena、字符串、日志 */
#include "rt.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct dp_block {
  struct dp_block* next;
  size_t cap;
  size_t used;
  unsigned char data[];
} dp_block;

struct dp_arena {
  dp_block* head;
  dp_block* tail;
  size_t block_size;
};

static size_t round_up8(size_t n) {
  return (n + 7u) & ~(size_t)7u;
}

dp_arena* dp_arena_new(size_t cap) {
  if (cap < 4096u) {
    cap = 4096u;
  }
  dp_arena* a = (dp_arena*)calloc(1, sizeof *a);
  if (!a) {
    return NULL;
  }
  dp_block* b = (dp_block*)malloc(sizeof(dp_block) + cap);
  if (!b) {
    free(a);
    return NULL;
  }
  b->next = NULL;
  b->cap = cap;
  b->used = 0;
  a->head = b;
  a->tail = b;
  a->block_size = cap;
  return a;
}

void* dp_alloc(dp_arena* a, size_t n) {
  n = round_up8(n);
  if (n == 0u) {
    n = 8u;
  }
  dp_block* b = a->tail;
  if (b->used + n > b->cap) {
    size_t cap = a->block_size;
    while (cap < n) {
      cap *= 2u;
    }
    dp_block* nb = (dp_block*)malloc(sizeof(dp_block) + cap);
    if (!nb) {
      abort();
    }
    nb->next = NULL;
    nb->cap = cap;
    nb->used = n;
    b->next = nb;
    a->tail = nb;
    return nb->data;
  }
  void* p = b->data + b->used;
  b->used += n;
  return p;
}

void dp_arena_reset(dp_arena* a) {
  dp_block* b = a->head->next;
  while (b) {
    dp_block* n = b->next;
    free(b);
    b = n;
  }
  a->head->next = NULL;
  a->head->used = 0;
  a->tail = a->head;
}

void dp_arena_free(dp_arena* a) {
  if (!a) {
    return;
  }
  dp_block* b = a->head;
  while (b) {
    dp_block* n = b->next;
    free(b);
    b = n;
  }
  free(a);
}

dp_str dp_str_from_cstr(const char* s) {
  dp_str out;
  out.p = s;
  out.n = s ? strlen(s) : 0u;
  return out;
}

dp_str dp_str_dup(dp_arena* a, dp_str s) {
  dp_str out;
  if (s.n == 0u) {
    out.p = "";
    out.n = 0u;
    return out;
  }
  char* buf = (char*)dp_alloc(a, s.n + 1u);
  memcpy(buf, s.p, s.n);
  buf[s.n] = '\0';
  out.p = buf;
  out.n = s.n;
  return out;
}

bool dp_str_eq(dp_str a, dp_str b) {
  if (a.n != b.n) {
    return false;
  }
  if (a.n == 0u) {
    return true;
  }
  return memcmp(a.p, b.p, a.n) == 0;
}

bool dp_bytes_eq(dp_bytes a, dp_bytes b) {
  if (a.n != b.n) {
    return false;
  }
  if (a.n == 0u) {
    return true;
  }
  return memcmp(a.p, b.p, a.n) == 0;
}

bool dp_val_eq(uint64_t a, uint64_t b) {
  return a == b;
}

uint64_t dp_sys_hash64(dp_bytes body) {
  /* FNV-1a 64：用于 ETag，不用于安全场景 */
  uint64_t h = 1469598103934665603ULL;
  for (size_t i = 0; i < body.n; i++) {
    h ^= (uint64_t)body.p[i];
    h *= 1099511628211ULL;
  }
  return h;
}

dp_str dp_sys_u64_str(dp_arena* a, uint64_t x) {
  char* buf = (char*)dp_alloc(a, 24u);
  int n = snprintf(buf, 24u, "%llu", (unsigned long long)x);
  dp_str s;
  s.p = buf;
  s.n = n < 0 ? 0u : (size_t)n;
  return s;
}

int64_t dp_sys_now_unix(void) {
  return (int64_t)time(NULL);
}

void dp_sys_log(dp_str msg) {
  if (msg.n > 0u) {
    fwrite(msg.p, 1u, msg.n, stdout);
  }
  fputc('\n', stdout);
  fflush(stdout);
}
