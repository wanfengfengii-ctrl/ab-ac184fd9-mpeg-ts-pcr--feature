# MPEG-TS 单节目片段归档前核验服务

广播监测中心在归档前用该服务核验单节目 MPEG-TS 片段，发现播放器容错通常
掩盖的问题：节目表损坏、丢包（连续计数断裂）、加扰、传输错误、不连续标志、
PCR 时间线倒退或间隔超限。

- `POST /api/mpegts/audit?maxPcrGapMs=<1..10000>`
- 可选恒定复用速率模式：追加 `expectedMuxRateBps=<100000..200000000>&maxRateErrorPpm=<1..100000>`，
  两个参数必须同时出现；同时省略时行为与原来完全一致
- 请求体：`Content-Type: application/octet-stream`，原始 MPEG-TS，不超过 8 MiB
- 只接受由 188 字节包组成的单节目流；任一规则失败即整体拒绝，不返回部分结果

## 核验规则

1. 报文长度必须是 188 的整数倍，每个包同步字节为 `0x47`。
2. 拒绝传输错误指示（TEI）、加扰（scrambling_control ≠ 00）、适配字段
   不连续标志；adaptation_field_control = 00（保留值）拒绝。
3. PAT/PMT 段必须在单个 188 字节包内完整（以 PUSI 起始、section_length
   不越包），CRC-32 正确；重复出现的 PAT/PMT 必须版本号相同且段内容逐字节
   一致。
4. PAT 必须且只能声明一个节目（program_number = 0 的网络 PID 条目不计数），
   PMT 必须在 PAT 指定的 PID 上且 program_number 匹配。
5. 除 PID 0、空包（0x1FFF）和 PAT/PMT 声明的 PID 外，不允许出现其他 PID。
6. PAT、PMT、PCR PID 及已声明媒体 PID 上：含载荷包的连续计数必须模 16
   递增；仅适配字段包必须保持计数不变；带载荷重复同一计数视为错误。
7. PCR 只能出现在 PMT 指定的 PCR PID 上；按 33 位基值回绕展开后不得倒退，
   相邻 PCR 间隔不得超过 `maxPcrGapMs`。
8. （可选，给定 `expectedMuxRateBps` 与 `maxRateErrorPpm` 时）以相邻 PCR
   所在包的零基起始位置与展开后的 27 MHz 时钟计算每个区间的传输速率：
   `区间字节数*8*27MHz/时钟增量`，逐区间与期望速率比较，相对误差均不得
   超过给定百万分比。至少需要两个 PCR，时钟增量必须为正，33 位回绕正确
   展开；参数缺失/越界、区间不可计算或超差均整体拒绝，不返回部分报告。

## 成功响应

`200 OK`

```json
{
  "ok": true,
  "report": {
    "programNumber": 1,
    "pmtPID": 4096,
    "pcrPID": 256,
    "mediaPIDs": [257, 258],
    "packetCount": 17,
    "firstPCR": { "packet": 2, "base": 0, "extension": 0, "value27mhz": 0 },
    "lastPCR":  { "packet": 14, "base": 4320000, "extension": 0, "value27mhz": 1296000000 },
    "duration27mhz": 4320000,
    "durationMs": 160,
    "payloadBytes": 1840,
    "media": [
      { "pid": 257, "packetCount": 5, "payloadBytes": 920 },
      { "pid": 258, "packetCount": 5, "payloadBytes": 920 }
    ]
  }
}
```

启用恒定复用速率模式时，`report` 内额外携带（省略两个参数时该字段缺省）：

```json
"muxRate": { "expectedMuxRateBps": 1504000, "tolerancePpm": 1000, "intervalsChecked": 3 }
```

## 失败响应

`422 Unprocessable Entity`（请求类错误为 400/413/415/405），包含包序号、
相关 PID 与稳定错误码，且不含任何部分结果：

```json
{
  "ok": false,
  "error": { "code": "TS_PCR_REVERSED", "message": "PCR moved backwards after 33-bit unwrap" },
  "packet": 4,
  "pid": 256
}
```

稳定错误码见 `internal/tsaudit/errors.go`（`TS_BAD_SYNC_BYTE`、
`TS_TRANSPORT_ERROR`、`TS_SCRAMBLED`、`TS_DISCONTINUITY_FLAG`、
`TS_MULTI_PROGRAM`、`TS_SECTION_CRC`、`TS_CC_GAP`、
`TS_CC_DUPLICATE_WITH_PAYLOAD`、`TS_PCR_ON_WRONG_PID`、
`TS_PCR_REVERSED`、`TS_PCR_GAP_EXCEEDED`、
`TS_MUX_RATE_INTERVAL_UNCOMPUTABLE`、`TS_MUX_RATE_MISMATCH` 等）。

速率模式失败时，错误定位到最早不合格区间的**后一个** PCR（其包序号与
PID）：区间不可计算（不足两个 PCR 或时钟增量非正）为
`TS_MUX_RATE_INTERVAL_UNCOMPUTABLE`，超差为 `TS_MUX_RATE_MISMATCH`。
请求类参数错误为 400：只给一个参数时 `TS_MISSING_MUX_RATE_PARAM`，
取值无法解析或越界时 `TS_INVALID_MUX_RATE_PARAM`。

## 本地开发（仅需 Go 1.23+）

```bash
go test ./...          # 单元测试
go run ./cmd/verify    # 一次性核验：vet + 测试 + 构建 + 等待健康 + 合法/异常流冒烟
go run ./cmd/server    # 启动 HTTP 服务（PORT 环境变量，默认 8080）
```

`cmd/verify` 在未设置 `AUDIT_BASE_URL` 时会自行构建并启动临时实例；
设置后则等待外部实例健康再做冒烟（容器内使用该模式）。

## Docker

镜像为多阶段构建：`runtime` target 是最小运行镜像（非 root，内置
HEALTHCHECK），`verify` target 携带完整源码与工具链用于一次性核验。

```bash
# 启动服务，宿主机端口可通过 HOST_PORT 配置
HOST_PORT=9090 docker compose up -d app

# 一次性核验服务：等待 app 健康后运行代码测试、应用构建及合法/异常流冒烟，
# 以退出码报告结果并自行退出（可在清洁环境反复重跑）
docker compose run --build --rm verify
echo "exit code: $?"
```

健康检查：`GET /healthz` → `200 {"status":"ok"}`。

## 仓库结构

```
cmd/server/          HTTP 服务入口
cmd/verify/          一次性核验（测试 + 构建 + 健康等待 + 冒烟）
internal/tsaudit/    TS 解析、节目表/CC/PCR 核验、HTTP handler
internal/tsbuild/    测试与冒烟用的最小 MPEG-TS 流构造器
Dockerfile           runtime / verify 两个 target
docker-compose.yml   app（端口可配 + 健康检查）与 verify（一次性）
```
