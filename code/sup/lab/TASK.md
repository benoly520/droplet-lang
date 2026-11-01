# 任务：从声明写实现

实现 `dp_decide.h` 里声明的两条纯决定，写进本目录的 `answer.c`。

## 约束

- 函数体里只能有条件与常量：不读环境变量、不碰文件系统、不碰 arena、不做 IO。
- 只写 `answer.c`，不要改 `dp_decide.h`（声明是题面，不是草稿）。
- 不要求注释，不要求解释；判据是下面命令的输出。

## 命令

```sh
cc -std=c11 -O2 -Wall -Wextra -Werror -I. -c answer.c -o answer.o
cc -std=c11 -O2 -Wall -Wextra -Werror -I. dp_oracle.c answer.o -o oracle
./oracle
```

`./oracle` 逐条打印 12 行 `ok`/`FAIL`；最后一行必须是
`oracle: 12 条样例，0 条挂`。

完成后，你的最终消息里只写：`DONE answer.c`（不要贴代码）。
