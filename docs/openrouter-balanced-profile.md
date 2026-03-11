# OpenRouter 的 OpenClaw 平衡型 Profile 说明

本文档基于 2026 年 3 月 12 日 OpenRouter 官方页面信息整理。

本文说明 [config.example.yaml](/Users/jay/clawgo/config.example.yaml) 中新增的 `balanced` profile。它面向 OpenClaw 这类 coding agent 场景，而不是单纯追求最低成本的聊天场景。

## 官方来源

- OpenClaw 集成文档: https://openrouter.ai/docs/guides/guides/coding-agents/openclaw-integration
- OpenRouter 模型总览: https://openrouter.ai/models
- Google Gemini 2.5 Flash: https://openrouter.ai/google/gemini-2.5-flash
- Google Gemini 2.5 Flash Lite: https://openrouter.ai/google/gemini-2.5-flash-lite
- OpenAI GPT-5 Mini: https://openrouter.ai/openai/gpt-5-mini
- OpenAI GPT-5.1: https://openrouter.ai/openai/gpt-5.1
- OpenAI GPT-5.2: https://openrouter.ai/openai/gpt-5.2
- Anthropic Claude Sonnet 4.6: https://openrouter.ai/anthropic/claude-sonnet-4.6
- OpenRouter 工具调用模型集合: https://openrouter.ai/collections/tool-calling-models

## 数据摘要

下表汇总了设计这个 profile 时主要参考的因素。

| 模型 | 上下文窗口 | 价格输入 / 输出 | 说明 |
| --- | ---: | ---: | --- |
| `google/gemini-2.5-flash` | 1.05M | $0.30 / $2.50 每 1M tokens | 成本低、上下文大，OpenRouter 页面将其定位为适合 reasoning、coding、math、science 的通用工作模型。 |
| `google/gemini-2.5-flash-lite` | 1.05M | $0.10 / $0.40 每 1M tokens | 这组模型里最低成本的长上下文选项，更适合做 fallback 或极致省钱场景。 |
| `openai/gpt-5-mini` | 400K | $0.25 / $2.00 每 1M tokens | GPT-5 系列的轻量模型，适合中等复杂度和较轻量的 agent 推理。 |
| `openai/gpt-5.1` | 400K | $1.25 / $10.00 每 1M tokens | 更偏通用高质量，指令遵循和工具调用稳定性比轻量模型更强。 |
| `openai/gpt-5.2` | 400K | $1.75 / $14.00 每 1M tokens | OpenRouter 页面将其定位为比 GPT-5.1 更强的 agentic 和长上下文模型。 |
| `anthropic/claude-sonnet-4.6` | 1.0M | $3.00 / $15.00 每 1M tokens | OpenRouter 页面明确强调 coding、agents、codebase navigation、长期项目工作。 |

## 推荐 Profile

```yaml
profiles:
  balanced:
    simple:
      - google/gemini-2.5-flash
      - openai/gpt-5-mini
      - google/gemini-2.5-flash-lite
    medium:
      - openai/gpt-5-mini
      - google/gemini-2.5-flash
      - google/gemini-2.5-pro
    complex:
      - anthropic/claude-sonnet-4.6
      - openai/gpt-5.1
      - google/gemini-2.5-pro
    reasoning:
      - openai/gpt-5.2
      - anthropic/claude-sonnet-4.6
      - openai/gpt-5.1
```

## 为什么更适合 OpenClaw

OpenRouter 的 OpenClaw 文档主要是在说明如何让 OpenClaw 直接连接 OpenRouter 模型。对于 ClawGo 的路由 profile，还要额外考虑以下运行特性：

1. OpenClaw 的 coding session 往往会带来更长的 prompt、更大的仓库上下文，以及多轮工具调用。
2. ClawGo 当前只会在 `429` 和 `5xx` 时触发 fallback，不会在上下文超限这类 prompt 形状错误时自动切换。见 [proxy.go](/Users/jay/clawgo/clawgo/proxy.go#L471)。
3. 因此，给 OpenClaw 用的 profile 不能只看单价，还要优先保证较大的 context window 和较强的 coding / tool-calling 定位。

基于这些约束，这个 profile 的设计是：

- `simple` 首选 `gemini-2.5-flash`，因为它在低成本下提供了很大的上下文窗口，且官方定位对 coding 比较友好。
- `medium` 首选 `gpt-5-mini`，因为它比更便宜的轻量模型更适合 agent 式任务，同时价格仍然比较克制。
- `complex` 首选 `claude-sonnet-4.6`，因为 OpenRouter 官方描述里对 coding agent、迭代开发、长项目工作的强调非常明确。
- `reasoning` 首选 `gpt-5.2`，因为 OpenRouter 页面将其定位为比 GPT-5.1 更强的 agentic / 长上下文模型。

## 取舍与替代方案

下面这些结论是基于上面官方信息做出的工程判断，不是 OpenRouter 的原文结论：

- 推断: 对 OpenClaw 来说，`claude-sonnet-4.6` 比 `gpt-5.1` 更适合作为 `complex` 默认主模型，因为它的 1M 上下文窗口在大仓库任务里更稳。
- 推断: 对这个 profile 来说，`gpt-5.2` 比 `o3` 更适合作为 `reasoning` 顶层，因为它保留了 400K 上下文窗口，同时仍然强调 agent 能力。
- 推断: 在 OpenClaw 的多步工具调用场景里，`gemini-2.5-flash-lite` 更适合放在 fallback，而不是直接做主力层级模型，因为省下的成本未必抵得上 agent 表现的不确定性。

如果你的负载特征不同，也可以考虑这些替代方案：

- 如果你更关心成本，可以把 `google/gemini-2.5-flash-lite` 提升为 `simple` 和 `medium` 的主模型。
- 如果你更偏爱 Anthropic 的 coding 风格，可以让 `anthropic/claude-sonnet-4.6` 同时作为 `complex` 和 `reasoning` 的主模型。
- 如果你的 OpenClaw 任务通常更短、更交互，而不是长仓库上下文，可以改成更偏 `gpt-5-mini` / `gpt-5.1` 的 400K 配置，以进一步降低成本。

## 推荐的 OpenClaw 配置项

如果你是在 OpenClaw 里把 ClawGo 暴露成一个 OpenAI 兼容 provider，建议默认使用下面这条：

```yaml
- id: balanced
  name: "ClawGo Balanced Router"
  reasoning: true
  input: [text]
  contextWindow: 400000
  maxTokens: 16384
  cost:
    input: 1.0
    output: 5.0
    cacheRead: 0
    cacheWrite: 0
```

这里把 `contextWindow` 写成 `400000` 是一个保守值。因为这组路由里混用了 1M 和 400K 上下文窗口的模型，对 OpenClaw 暴露统一能力时，应该按所有层级里更稳妥的共同下限来写，避免把能力宣称得过高。
