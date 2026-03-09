# ClawGo

Go 实现的智能 LLM 路由代理，使用 OpenRouter 作为后端。移植自 [ClawRouter](https://github.com/BlockRunAI/ClawRouter) 的 14 维智能路由引擎。

## 目录

- [快速开始](#快速开始)
- [架构概览](#架构概览)
- [技术原理](#技术原理)
  - [14 维请求分类器](#14-维请求分类器)
  - [路由配置](#路由配置)
  - [回退链机制](#回退链机制)
  - [请求去重](#请求去重)
  - [响应缓存](#响应缓存)
  - [会话固定](#会话固定)
  - [余额监控](#余额监控)
  - [SSE 流式转发](#sse-流式转发)
- [源码分析](#源码分析)
- [OpenClaw 集成](#openclaw-集成)
- [配置参考](#配置参考)
- [API 参考](#api-参考)

---

## 快速开始

### 编译

```bash
cd clawgo
go build -o bin/clawgo ./cmd/clawgo
```

### 运行

```bash
export OPENROUTER_API_KEY=sk-or-v1-xxx
./bin/clawgo --port 8402 --profile auto
```

### 发送请求

```bash
# 智能路由 (model=auto 自动选最优模型)
curl -X POST http://localhost:8402/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "hello"}]
  }'

# 指定配置: eco (最省钱) / premium (最高质量)
curl -X POST http://localhost:8402/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "eco",
    "messages": [{"role": "user", "content": "what is 2+2?"}]
  }'

# 流式响应
curl -N -X POST http://localhost:8402/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "write a haiku"}],
    "stream": true
  }'

# 直接指定 OpenRouter 模型 (跳过智能路由)
curl -X POST http://localhost:8402/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic/claude-sonnet-4",
    "messages": [{"role": "user", "content": "hello"}]
  }'

# 健康检查
curl http://localhost:8402/health
```

### Docker

```bash
docker build -t clawgo .
docker run -e OPENROUTER_API_KEY=sk-or-v1-xxx -p 8402:8402 clawgo
```

---

## 架构概览

```
用户应用 (OpenAI 兼容请求)
        │
        ▼
┌──────────────────────────────────────────────┐
│        ClawGo 本地代理 (:8402)                │
│                                              │
│  ┌──────────┐    ┌──────────┐                │
│  │ 请求去重  │───▶│ 响应缓存  │                │
│  │ SHA-256  │    │ LRU 200  │                │
│  │ 30s TTL  │    │ 10min    │                │
│  └────┬─────┘    └──────────┘                │
│       │                                      │
│  ┌────▼──────────────────────────┐           │
│  │      14 维智能路由引擎          │           │
│  │  reasoningMarkers  (0.18)     │           │
│  │  codePresence      (0.15)     │           │
│  │  multiStepPatterns  (0.12)    │           │
│  │  technicalTerms    (0.10)     │           │
│  │  ... 10 more dimensions       │           │
│  │                               │           │
│  │  ──▶ SIMPLE / MEDIUM /        │           │
│  │      COMPLEX / REASONING      │           │
│  └────┬──────────────────────────┘           │
│       │                                      │
│  ┌────▼─────┐    ┌──────────┐                │
│  │ 模型选择  │    │ 会话固定  │                │
│  │ + 回退链  │◀──▶│ 30min    │                │
│  └────┬─────┘    └──────────┘                │
│       │                                      │
│  ┌────▼─────┐                                │
│  │ 余额检查  │  OpenRouter /api/v1/auth/key   │
│  │ 30s 缓存  │                                │
│  └────┬─────┘                                │
│       │                                      │
└───────┼──────────────────────────────────────┘
        │
        ▼
   OpenRouter API
   https://openrouter.ai/api/v1
   (Authorization: Bearer <api-key>)
```

### 请求处理流程

```
1. 收到 POST /v1/chat/completions
2. SHA-256 哈希请求体 → 检查去重缓存
   ├─ 命中 → 直接返回缓存响应
   └─ 未命中 → 继续
3. 解析 model 字段
   ├─ "auto"/"eco"/"premium" → 进入智能路由
   │   ├─ 14 维评分 → 加权求和 → 分层
   │   ├─ 检查会话固定 → 复用已有模型
   │   └─ 选择主模型 + 构建回退链
   └─ 具体模型名 → 直接转发
4. 检查响应缓存 (非流式)
   ├─ 命中 → 返回
   └─ 未命中 → 继续
5. 转发到 OpenRouter
   ├─ 流式 → SSE 透传 + 2s 心跳保活
   └─ 非流式 → 等待完整响应
6. 出错 (429/5xx) → 尝试回退链下一个模型 (最多 5 次)
7. 缓存响应 + 完成去重 + 更新会话
```

---

## 技术原理

### 14 维请求分类器

ClawGo 的核心是一个纯本地、零网络调用的请求分类器，在 <1ms 内将请求分为四个复杂度层级。

#### 工作原理

对用户输入的 prompt（小写化后）进行 14 个维度的评分，每个维度返回 [-1, 1] 的分数，加权求和后映射到层级：

| # | 维度 | 权重 | 评分逻辑 |
|---|------|------|---------|
| 1 | reasoningMarkers | 0.18 | "prove", "theorem", "step by step", "证明", "定理" 等 |
| 2 | codePresence | 0.15 | "function", "class", "import", "函数", "类" 等 |
| 3 | multiStepPatterns | 0.12 | 正则匹配: `first.*then`, `step \d`, `\d\.\s` |
| 4 | technicalTerms | 0.10 | "algorithm", "kubernetes", "算法", "架构" 等 |
| 5 | tokenCount | 0.08 | 短 (<50 tokens) → -1.0, 长 (>500) → 1.0 |
| 6 | questionComplexity | 0.05 | 问号数量 >3 → 0.5 |
| 7 | creativeMarkers | 0.05 | "story", "poem", "故事", "诗" 等 |
| 8 | constraintCount | 0.04 | "at most", "budget", "不超过", "预算" 等 |
| 9 | agenticTask | 0.04 | "edit", "deploy", "fix", "编辑", "部署" 等 |
| 10 | imperativeVerbs | 0.03 | "build", "create", "implement", "构建" 等 |
| 11 | outputFormat | 0.03 | "json", "yaml", "table", "表格" 等 |
| 12 | simpleIndicators | 0.02 | "what is", "define", "什么是" → **负分** (-1.0) |
| 13 | referenceComplexity | 0.02 | "the docs", "above", "文档" 等 |
| 14 | domainSpecificity | 0.02 | "quantum", "fpga", "genomics", "量子" 等 |
| 15 | negationComplexity | 0.01 | "don't", "avoid", "不要", "避免" 等 |

**多语言支持：** 每个维度的关键词覆盖 9 种语言 — 英语、中文、日语、俄语、德语、西班牙语、葡萄牙语、韩语、阿拉伯语。

#### 层级划分

```
加权分数    层级
──────────────────────────
< 0.0       SIMPLE      简单问答，翻译，问候
0.0 - 0.3   MEDIUM      一般编程，技术问题
0.3 - 0.5   COMPLEX     复杂架构，多步骤任务
> 0.5       REASONING   数学证明，形式推理

特殊规则：
- 2+ 推理关键词命中 → 强制 REASONING
- 置信度 < 0.7 → 降级为 MEDIUM (兜底)
```

#### 置信度计算

使用 Sigmoid 函数将距离最近边界的距离映射为置信度：

```
confidence = 1 / (1 + exp(-12 * distanceFromBoundary))
```

距离越远 → 置信度越高 → 分类越确定。置信度 <0.7 时，分类结果为 "模糊"，默认降级到 MEDIUM。

#### Agentic 检测

独立于 14 维评分器，还有一个 Agentic 分数用于检测多步骤自主任务：

```
4+ agentic 关键词 → agenticScore = 1.0
3 个 → agenticScore = 0.6 (触发 agentic 模式)
1-2 个 → agenticScore = 0.2
0 个 → agenticScore = 0.0
```

### 路由配置

三种路由配置，用户通过 `model` 字段选择：

#### Auto (默认，均衡)

| 层级 | 主模型 | 回退 |
|------|--------|------|
| SIMPLE | google/gemini-2.5-flash-lite | deepseek-chat |
| MEDIUM | google/gemini-2.5-flash | deepseek-chat, flash-lite |
| COMPLEX | google/gemini-2.5-pro | flash, deepseek-chat |
| REASONING | anthropic/claude-sonnet-4 | deepseek-r1, o3 |

#### Eco (极致省钱)

| 层级 | 主模型 | 回退 |
|------|--------|------|
| SIMPLE | google/gemini-2.5-flash-lite | deepseek-chat |
| MEDIUM | google/gemini-2.5-flash-lite | deepseek-chat |
| COMPLEX | google/gemini-2.5-flash | deepseek-chat |
| REASONING | deepseek/deepseek-r1 | flash |

#### Premium (最高质量)

| 层级 | 主模型 | 回退 |
|------|--------|------|
| SIMPLE | anthropic/claude-haiku | flash |
| MEDIUM | anthropic/claude-sonnet-4 | gemini-pro, gpt-4o |
| COMPLEX | anthropic/claude-opus-4 | gpt-4o, sonnet-4 |
| REASONING | openai/o3-pro | sonnet-4, o3 |

#### 价格自动分层

对于不在默认配置中的模型，按价格自动归类：

```
平均价格 (input + output) / 2    层级
──────────────────────────────────────
< $1/M tokens                    SIMPLE
$1 - $8/M                       MEDIUM
$8 - $20/M                      COMPLEX
> $20/M                         REASONING
```

### 回退链机制

当主模型返回 429 (限流) 或 5xx (服务器错误) 时，自动尝试回退链中的下一个模型，最多重试 5 次：

```
请求 → model_A (429) → model_B (500) → model_C (200 ✓)
```

回退链顺序：`[主模型, 回退模型1, 回退模型2, ...]`，每次切换会重新计算成本估算。

### 请求去重

防止客户端超时重试导致的重复请求和重复计费：

```
请求1 (hash=abc) → 标记 in-flight → 发送到 OpenRouter
请求1 重试 (hash=abc) → 发现 in-flight → 等待原始请求完成 → 复用响应
请求2 (hash=def) → 不同哈希 → 独立请求
```

**哈希算法：**
1. JSON 解析请求体
2. 递归排序所有对象键（消除字段顺序差异）
3. 剥离时间戳前缀（`[SUN 2026-02-07 13:30 PST]` 格式）
4. SHA-256 哈希，取前 16 个十六进制字符

**缓存策略：** 完成的响应缓存 30 秒，最大 1MB。

### 响应缓存

对于相同的非流式请求，直接返回缓存的 LLM 响应：

```
请求 "2+2=?" → API → 缓存 (10 min TTL)
再次 "2+2=?" → 缓存命中 → 立即返回 (0ms)
```

**缓存键生成：** 与去重哈希类似，但额外剥离 `stream`, `user`, `request_id` 字段（这些不影响响应内容）。

**LRU 淘汰：** 最多 200 条缓存，超出时淘汰最早插入的条目。错误响应 (status >= 400) 不缓存。

### 会话固定

防止同一对话中途切换模型导致的上下文不连贯：

```
Turn 1: "写一个 React 应用" → 路由到 gemini-pro
Turn 2: "加上暗色模式"     → 固定到 gemini-pro (不重新路由)
Turn 3: "写测试"          → 固定到 gemini-pro
... 30 分钟无活动后解除固定
```

**Session ID 来源：**
1. 请求头 `X-Session-ID`（优先）
2. 自动从第一条用户消息推导（SHA-256 前 4 字节）

**三击升级机制：** 如果同一会话连续 3 次发送相似请求（内容哈希相同），自动将模型升级到下一层级（SIMPLE → MEDIUM → COMPLEX → REASONING），避免简单模型反复失败。

### 余额监控

通过 OpenRouter 的 `/api/v1/auth/key` 接口查询余额：

```
可用余额 = limit - usage
```

- 每 30 秒缓存一次，避免频繁 API 调用
- 余额 < $1 时打印警告
- limit = 0 视为无限额度

### SSE 流式转发

流式请求的处理：

```
0s:  客户端发送 POST (stream=true)
0s:  ← 返回 200 + Content-Type: text/event-stream
0s:  ← : heartbeat
2s:  ← : heartbeat     ← 每 2 秒心跳，防止客户端超时
4s:  ← data: {"choices":[...]}   ← OpenRouter 开始响应
4s:  ← data: {"choices":[...]}
...
8s:  ← data: [DONE]
```

心跳以 SSE 注释格式（`: heartbeat\n\n`）发送，不会干扰 SSE 解析器。

---

## 源码分析

### 项目结构

```
clawgo/
├── cmd/
│   └── clawgo/
│       └── main.go              # CLI 入口 (69 行)
├── clawgo/
│   ├── schema/
│   │   ├── api.go               # OpenAI 请求/响应类型 (72 行)
│   │   ├── model.go             # 模型/OpenRouter 类型 (63 行)
│   │   ├── router.go            # 路由/分层类型 (56 行)
│   │   └── error.go             # 错误码常量 (13 行)
│   ├── clawgo.go                # 主入口 New/Run/Close (81 行)
│   ├── proxy.go                 # HTTP 代理 + SSE (465 行)
│   ├── router.go                # 14维分类器 (467 行)
│   ├── selector.go              # 模型选择 + 成本 (156 行)
│   ├── models.go                # 模型目录 (190 行)
│   ├── balance.go               # 余额监控 (101 行)
│   ├── dedup.go                 # 请求去重 (198 行)
│   ├── cache.go                 # 响应缓存 (148 行)
│   ├── session.go               # 会话固定 (208 行)
│   └── config.go                # 配置加载 (78 行)
├── example/basic/main.go        # 使用示例
├── Dockerfile                   # 多阶段构建
├── Makefile                     # 构建/测试/格式化
├── go.mod
└── go.sum

总计: 2,064 行 Go 源码, 42 个测试
```

### 模块依赖关系

```
cmd/clawgo/main.go
    └── clawgo.go (New / Run / Close)
            ├── config.go         ← 环境变量 + YAML
            ├── router.go         ← 14 维分类器 (无外部依赖)
            ├── models.go         ← OpenRouter API 拉取
            ├── selector.go       ← 层级→模型 + 成本
            ├── balance.go        ← 余额查询 + 缓存
            ├── session.go        ← 会话固定 + 升级
            ├── dedup.go          ← 请求去重
            ├── cache.go          ← 响应缓存 LRU
            └── proxy.go          ← HTTP 代理 (组装所有模块)
                    └── schema/*  ← 类型定义
```

### 代码风格 (everFinance 规范)

- **包名 = 文件夹名：** `clawgo/clawgo/` → `package clawgo`
- **入口三件套：** `New()` 构造, `Run()` 启动, `Close()` 关闭
- **schema 包：** 所有类型定义集中在 `schema/` 子包
- **错误定义：** `error.go` 使用 `ERR_SNAKE_CASE` 常量
- **公有在前：** 导出函数在文件顶部，私有函数在底部
- **并发安全：** `sync.RWMutex` 保护所有共享状态
- **无 init()：** 所有初始化在 `New()` 中完成

### 核心模块详解

#### router.go — 14 维分类器

```go
type Router struct {
    codeKeywords      []string  // 11 语言 × ~11 关键词
    reasoningKeywords []string  // 11 语言 × ~9 关键词
    // ... 13 more keyword lists
    multiStepRegexps  []*regexp.Regexp  // 编译后的正则
    dimensionWeights  map[string]float64
    tierBoundaries    TierBoundaries
}

func (r *Router) Classify(prompt, systemPrompt string, estimatedTokens int64) schema.ScoringResult {
    text := strings.ToLower(prompt)  // 只评分用户输入，不评分 system prompt

    // 15 个维度打分
    dimensions := []schema.DimensionScore{...}

    // 加权求和
    weightedScore := sum(d.Score * weights[d.Name])

    // 2+ 推理关键词 → 强制 REASONING
    if reasoningMatches >= 2 { return REASONING }

    // 边界映射 + Sigmoid 置信度
    tier := mapToTier(weightedScore, boundaries)
    confidence := sigmoid(distanceFromBoundary, steepness=12)

    if confidence < 0.7 { tier = nil }  // 模糊 → 降级
}
```

#### proxy.go — HTTP 代理

```go
func (p *Proxy) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
    body := readBody(r)
    req := parseJSON(body)

    // 去重检查
    dedupKey := DedupHash(body)
    if cached := p.dedup.GetCached(dedupKey); cached != nil { return cached }
    if ch := p.dedup.GetInflight(dedupKey); ch != nil { return <-ch }
    p.dedup.MarkInflight(dedupKey)

    // 智能路由
    if isRoutingProfile(req.Model) {
        decision := p.routeRequest(req)
        model = decision.Model
    }

    // 会话固定
    if session := p.session.Get(sessionID); session != nil {
        model = session.Model  // 复用已有模型
    }

    // 回退链转发
    for _, tryModel := range fallbackChain {
        err := p.forward(w, body, tryModel)  // 流式或非流式
        if err == nil { return }              // 成功
        // 429/5xx → 尝试下一个
    }
}
```

#### dedup.go — 请求去重

```go
// 核心数据结构
type Deduplicator struct {
    inflight  map[string]*inflightEntry   // 正在处理的请求
    completed map[string]*CachedResponse  // 已完成的缓存
}

// 等待者通过 channel 接收结果
type inflightEntry struct {
    waiters []chan *CachedResponse
}

// 哈希 = SHA-256(canonicalize(stripTimestamps(JSON)))
func DedupHash(body []byte) string { ... }
```

---

## OpenClaw 集成

ClawGo 作为独立代理运行，OpenClaw 通过标准 OpenAI API 与之通信。

### 方式一：修改 openclaw.yaml

在 OpenClaw 的配置文件中添加 ClawGo 作为 provider：

```yaml
# ~/.openclaw/openclaw.yaml (或项目目录下的 openclaw.yaml)
models:
  providers:
    clawgo:
      baseUrl: http://localhost:8402
      api: openai-completions
      models:
        # 智能路由 - 自动选模型
        - id: auto
          name: "ClawGo Auto Router"
          reasoning: false
          input: [text, image]
          contextWindow: 1000000
          maxTokens: 16384
          cost:
            input: 0.5
            output: 2.0
            cacheRead: 0
            cacheWrite: 0

        # 省钱模式
        - id: eco
          name: "ClawGo Eco Router"
          reasoning: false
          input: [text]
          contextWindow: 1000000
          maxTokens: 16384
          cost:
            input: 0.1
            output: 0.4
            cacheRead: 0
            cacheWrite: 0

        # 高质量模式
        - id: premium
          name: "ClawGo Premium Router"
          reasoning: true
          input: [text, image]
          contextWindow: 200000
          maxTokens: 16384
          cost:
            input: 5.0
            output: 25.0
            cacheRead: 0
            cacheWrite: 0
```

然后在 OpenClaw 中选择模型：

```bash
openclaw models set clawgo/auto
```

### 方式二：作为通用 OpenAI 代理

任何支持自定义 OpenAI 端点的工具都可以直接使用：

```bash
# 设置环境变量
export OPENAI_API_BASE=http://localhost:8402/v1
export OPENAI_API_KEY=unused  # ClawGo 使用自己的 OpenRouter key

# 在任何 OpenAI SDK 兼容的工具中使用
python -c "
from openai import OpenAI
client = OpenAI(base_url='http://localhost:8402/v1', api_key='unused')
resp = client.chat.completions.create(
    model='auto',
    messages=[{'role': 'user', 'content': 'hello'}]
)
print(resp.choices[0].message.content)
"
```

### 方式三：配合 systemd / PM2 常驻

```bash
# systemd 服务文件 /etc/systemd/system/clawgo.service
[Unit]
Description=ClawGo LLM Router
After=network.target

[Service]
Type=simple
User=jay
Environment=OPENROUTER_API_KEY=sk-or-v1-xxx
ExecStart=/usr/local/bin/clawgo --port 8402 --profile auto
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable clawgo
sudo systemctl start clawgo
```

### 启动顺序

```
1. 启动 ClawGo (端口 8402)
   └── 拉取 OpenRouter 模型列表
   └── 查询余额
   └── 就绪

2. 启动 OpenClaw
   └── 加载 openclaw.yaml
   └── 发现 clawgo provider
   └── 请求发往 http://localhost:8402

3. 用户交互
   └── openclaw models set clawgo/auto
   └── 正常对话
```

---

## 配置参考

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `OPENROUTER_API_KEY` | (必填) | OpenRouter API Key |
| `CLAWGO_PORT` | `8402` | 代理监听端口 |
| `CLAWGO_PROFILE` | `auto` | 默认路由配置 |
| `CLAWGO_CONFIG` | `~/.clawgo/config.yaml` | 配置文件路径 |

### CLI 参数

```bash
clawgo [options]

Options:
  --api-key      OpenRouter API key (覆盖环境变量)
  --port         监听端口 (默认 8402)
  --profile      路由配置 auto/eco/premium (默认 auto)
  --version, -v  显示版本
  --help, -h     显示帮助
```

### YAML 配置文件

`~/.clawgo/config.yaml`:

```yaml
port: 8402
profile: auto

# 自定义层级模型映射 (覆盖默认值)
profiles:
  auto:
    simple:
      - google/gemini-2.5-flash-lite
    medium:
      - google/gemini-2.5-flash
    complex:
      - google/gemini-2.5-pro
    reasoning:
      - anthropic/claude-sonnet-4

  # 可以定义自己的配置
  my-custom:
    simple:
      - deepseek/deepseek-chat
    medium:
      - openai/gpt-4o-mini
    complex:
      - openai/gpt-4o
    reasoning:
      - openai/o3
```

---

## API 参考

### POST /v1/chat/completions

OpenAI 兼容的聊天补全接口。

**请求体：**

```json
{
  "model": "auto",
  "messages": [
    {"role": "system", "content": "you are helpful"},
    {"role": "user", "content": "hello"}
  ],
  "stream": false,
  "max_tokens": 4096,
  "temperature": 0.7,
  "tools": []
}
```

**model 可选值：**
- `auto` / `clawgo/auto` — 智能路由 (均衡)
- `eco` / `clawgo/eco` — 智能路由 (省钱)
- `premium` / `clawgo/premium` — 智能路由 (高质量)
- `openai/gpt-4o`, `anthropic/claude-sonnet-4`, ... — 直接指定 OpenRouter 模型

**响应：** 标准 OpenAI 格式 (ChatCompletionResponse)。

### GET /health

健康检查。

```json
{
  "status": "ok",
  "version": "0.1.0",
  "port": 8402,
  "profile": "auto",
  "balance": 42.50,
  "models": 300
}
```

### GET /v1/models

透传 OpenRouter 的模型列表。

---

## 与 ClawRouter (TypeScript) 的对比

| 方面 | ClawRouter (TS) | ClawGo |
|------|----------------|--------|
| 语言 | TypeScript | Go |
| 部署 | npm 包 / OpenClaw 插件 | 单二进制 (12MB) |
| 后端 | BlockRun API (x402 支付) | OpenRouter (API Key) |
| 支付 | USDC 区块链微支付 | OpenRouter 余额 |
| 路由引擎 | 14 维分类器 | 14 维分类器 (完全移植) |
| 模型目录 | 硬编码 41+ 模型 | 动态拉取 (启动时) |
| 压缩 | 7 层上下文压缩 | (后续添加) |
| 图片生成 | 5 个图片模型 | (后续添加) |
| 钱包管理 | BIP-39 / EVM / Solana | 无 (不需要) |
| 内存占用 | ~100MB (Node.js) | ~20MB |
