// rssshim —— 量一个命令真花掉多少常驻内存。为什么需要它：
// fork 出来的孩子在 exec 之前就继承了父进程的 RSS，ru_maxrss 又是高水位，
// 所以在 Python 里 wait4 量小目标，量到的其实是「父进程 + 目标」的最大值（本轮踩过）。
// 这个壳自己只有 ~1.3 MB，fork + execvp + wait4 之后把孩子的峰值打出来。
// 用法：rssshim 命令 [参数...]     输出：peak_rss_kb=<KB> exit=<退出码>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/wait.h>
#include <sys/resource.h>

int main(int argc, char **argv) {
    if (argc < 2) { fprintf(stderr, "用法: rssshim 命令 [参数...]\n"); return 2; }
    pid_t p = fork();
    if (p == 0) {
        int fd = open("/dev/null", O_WRONLY);
        if (fd >= 0) { dup2(fd, 1); dup2(fd, 2); close(fd); }
        execvp(argv[1], argv + 1);
        _exit(127);
    }
    if (p < 0) { perror("fork"); return 2; }
    int st = 0;
    struct rusage ru;
    if (wait4(p, &st, 0, &ru) < 0) { perror("wait4"); return 2; }
    printf("peak_rss_kb=%ld exit=%d\n", (long)ru.ru_maxrss, WEXITSTATUS(st));
    return 0;
}
