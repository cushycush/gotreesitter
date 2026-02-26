#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "api.h"

#ifndef TS_LANG_FN
#error "TS_LANG_FN macro is required"
#endif

extern const TSLanguage *TS_LANG_FN(void);

static uint64_t now_ns(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
}

static char *read_file(const char *path, size_t *out_len) {
  FILE *f = fopen(path, "rb");
  if (!f) {
    return NULL;
  }

  if (fseek(f, 0, SEEK_END) != 0) {
    fclose(f);
    return NULL;
  }

  long sz = ftell(f);
  if (sz < 0) {
    fclose(f);
    return NULL;
  }

  if (fseek(f, 0, SEEK_SET) != 0) {
    fclose(f);
    return NULL;
  }

  char *buf = (char *)malloc((size_t)sz + 1u);
  if (!buf) {
    fclose(f);
    return NULL;
  }

  size_t n = fread(buf, 1, (size_t)sz, f);
  fclose(f);
  buf[n] = '\0';
  *out_len = n;
  return buf;
}

static bool tree_ok(TSTree *tree) {
  if (!tree) {
    return false;
  }
  TSNode root = ts_tree_root_node(tree);
  return !ts_node_is_null(root);
}

int main(int argc, char **argv) {
  if (argc < 4) {
    fprintf(stderr, "usage: %s <label> <sample-file> <iters>\n", argv[0]);
    return 2;
  }

  const char *label = argv[1];
  const char *sample_path = argv[2];
  int iters = atoi(argv[3]);
  if (iters <= 0) {
    iters = 10000;
  }

  size_t source_len = 0;
  char *source = read_file(sample_path, &source_len);
  if (!source) {
    fprintf(stderr, "read failed: %s\n", sample_path);
    return 2;
  }

  TSParser *parser = ts_parser_new();
  if (!parser || !ts_parser_set_language(parser, TS_LANG_FN())) {
    fprintf(stderr, "parser init failed for %s\n", label);
    free(source);
    return 2;
  }

  TSTree *warm = ts_parser_parse_string(parser, NULL, source, (uint32_t)source_len);
  if (!tree_ok(warm)) {
    fprintf(stderr, "warmup parse failed for %s\n", label);
    ts_parser_delete(parser);
    free(source);
    return 2;
  }
  ts_tree_delete(warm);

  uint64_t t0 = now_ns();
  for (int i = 0; i < iters; i++) {
    TSTree *tree = ts_parser_parse_string(parser, NULL, source, (uint32_t)source_len);
    if (!tree_ok(tree)) {
      fprintf(stderr, "parse failed for %s at iter %d\n", label, i);
      ts_parser_delete(parser);
      free(source);
      return 2;
    }
    ts_tree_delete(tree);
  }
  uint64_t t1 = now_ns();

  double ns_op = (double)(t1 - t0) / (double)iters;
  double mbps = 0.0;
  if (ns_op > 0.0) {
    mbps = ((double)source_len / (1024.0 * 1024.0)) / (ns_op / 1e9);
  }

  printf("lang=%s bytes=%zu ns_op=%.2f MBps=%.2f\n", label, source_len, ns_op, mbps);

  ts_parser_delete(parser);
  free(source);
  return 0;
}
