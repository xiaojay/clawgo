# Python 版 ClawGo 模型选择器说明

本文说明仓库里的 Python 版模型选择器实现：[python/clawgo_model_selector.py](/Users/jay/clawgo/python/clawgo_model_selector.py)。

它是对 Go 版两段核心逻辑的直接移植：

- 请求复杂度分类：对应 [router.go](/Users/jay/clawgo/clawgo/router.go)
- profile 选模与成本估算：对应 [selector.go](/Users/jay/clawgo/clawgo/selector.go)

这份实现适合做三类事情：

1. 在 Python 侧复用 ClawGo 当前的路由规则。
2. 离线分析 prompt 会被分到哪个 tier。
3. 对不同 profile 的选模结果和成本做快速实验。

## 整体流程

Python 版保留了和 Go 相同的两阶段流程：

1. 先根据 prompt 打分，输出 `SIMPLE`、`MEDIUM`、`COMPLEX`、`REASONING`。
2. 再根据 profile 的 tier 配置，选出主模型并计算成本。

对应入口函数是：

- `Router.classify(...)`
- `select_model(...)`
- `route_prompt(...)`

其中 `route_prompt(...)` 最接近 Go 里 `proxy.routeRequest` 的行为：它会先估算 token，再分类，再按 profile 给出最终模型。

## 已移植的能力

### 1. 复杂度分类

`Router` 保留了 Go 版的关键词、正则、权重和阈值：

- 15 个维度
- 相同的加权求和逻辑
- 相同的 tier 边界
- 相同的 sigmoid 置信度
- 相同的“2 个以上 reasoning 关键词直接判定为 `REASONING`”规则

返回结果 `ScoringResult` 包含：

- `score`
- `tier`
- `confidence`
- `signals`
- `agentic_score`
- `dimensions`

### 2. profile 选模

`default_tier_configs(profile)` 保留了 Go 版内置 profile：

- `auto`
- `eco`
- `premium`

`select_model(...)` 会根据 tier 选择主模型，并计算：

- `cost_estimate`
- `baseline_cost`
- `savings`

基准成本仍然按 `anthropic/claude-opus-4` 计算；如果 catalog 里没有这条模型价格，就退回到 Go 版同样的默认值：

- 输入价格：`5.0 / 1M tokens`
- 输出价格：`25.0 / 1M tokens`

### 3. fallback 与过滤

Python 版也保留了这些辅助函数：

- `get_fallback_chain(...)`
- `calculate_model_cost(...)`
- `filter_by_tool_calling(...)`
- `filter_by_vision(...)`

## 快速开始

### 命令行方式

直接运行：

```bash
python3 python/clawgo_model_selector.py "prove this theorem step by step"
```

带 profile：

```bash
python3 python/clawgo_model_selector.py \
  "read file, edit the code, fix the bug, then deploy and verify" \
  --profile auto
```

带 system prompt 和输出 token 预算：

```bash
python3 python/clawgo_model_selector.py \
  "implement a function that uses async await with import and class" \
  --system-prompt "You are a coding assistant." \
  --max-output-tokens 8192
```

输出是 JSON，包含：

- 分类结果 `scoring`
- 选模结果 `decision`
- 最终采用的 `estimated_tokens`

### 作为 Python 模块导入

```python
from clawgo_model_selector import ModelCatalog, route_prompt

catalog = ModelCatalog()
result = route_prompt(
    prompt="implement a function that uses async await with import and class",
    profile="auto",
    catalog=catalog,
    max_output_tokens=4096,
)

print(result.scoring.tier)
print(result.decision.model)
print(result.decision.cost_estimate)
```

如果只想做分类：

```python
from clawgo_model_selector import Router

router = Router()
result = router.classify(
    prompt="prove this theorem step by step using mathematical proof",
    system_prompt="",
    estimated_tokens=100,
)

print(result.tier)
print(result.confidence)
print(result.signals)
```

如果只想做选模：

```python
from clawgo_model_selector import (
    ModelCatalog,
    Tier,
    default_tier_configs,
    select_model,
)

catalog = ModelCatalog()
decision = select_model(
    tier=Tier.MEDIUM,
    confidence=0.9,
    method="rules",
    reasoning="score=0.18 | code detected",
    tier_configs=default_tier_configs("auto"),
    catalog=catalog,
    estimated_input_tokens=1000,
    max_output_tokens=500,
    profile="auto",
    agentic_score=0.0,
)
```

## ModelCatalog 的使用方式

`ModelCatalog` 默认是空的。空 catalog 也能工作，但成本会退化成：

- 当前模型价格视为 `0`
- baseline 仍有默认价格

如果你已经有 OpenRouter `/models` 的 JSON，可以这样初始化：

```python
import json

from clawgo_model_selector import ModelCatalog

with open("openrouter-models.json", "r", encoding="utf-8") as f:
    payload = json.load(f)

catalog = ModelCatalog.from_openrouter_response(payload)
```

目前 `from_openrouter_response(...)` 会自动填充：

- `id`
- `name`
- `pricing`
- `context_length`
- `top_provider`
- `supports_vision`

其中 `supports_vision` 的判定方式与 Go 版一致，基于 `architecture.modality == "text+image->text"`。

## 与 Go 版对齐的行为

下面这些行为已经按 Go 版实现对齐：

- token 估算公式：`len(system_prompt + " " + prompt) // 4`
- 置信度不足时，不返回明确 tier；在 `route_prompt(...)` 中回退到 `MEDIUM`
- `premium` profile 下 `savings` 固定不计算
- fallback 链顺序为 `[primary, ...fallback]`

## 当前边界

这份 Python 版是“算法移植”，不是完整的 ClawGo 代理实现。当前没有覆盖：

- HTTP 代理层
- 会话 pinning
- 请求去重
- 429/5xx fallback 重试流程
- 配置文件加载和自定义 profile 合并

还有一个需要注意的点：

- `filter_by_tool_calling(...)` 依赖 `ModelInfo.supports_tools`
- 但 `from_openrouter_response(...)` 当前不会自动推断 `supports_tools`

这和当前 Go 版的状态是一致的：Go 版 `FetchModels` 里也只自动标记了 vision，没有自动标记 tool calling。

## 测试

Python 测试文件在 [python/test_clawgo_model_selector.py](/Users/jay/clawgo/python/test_clawgo_model_selector.py)，覆盖了以下核心行为：

- 简单请求分类
- 推理请求分类
- 代码类请求分类
- agentic 任务识别
- 置信度函数
- 基础选模
- fallback 链
- premium savings
- 歧义场景默认回落到 `MEDIUM`

本地运行命令：

```bash
python3 -m unittest discover -s python -p 'test_*.py'
```

## 适合后续扩展的方向

如果后面要继续把 Python 版做完整，优先级建议是：

1. 增加配置文件加载，补齐自定义 profile。
2. 增加 OpenRouter `/models` 在线拉取。
3. 把 `route_prompt(...)` 扩成与 Go `routeRequest` 更接近的请求对象接口。
4. 如果需要做真实代理，再补 HTTP 转发、fallback 和 session pinning。
