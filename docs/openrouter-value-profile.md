# OpenRouter 的 OpenClaw 性价比 Profile 说明

本文档基于 2026 年 3 月 12 日 OpenRouter 官方页面信息整理。

本文说明 [config.example.yaml](/Users/jay/clawgo/config.example.yaml) 中新增的 `value` profile。它同样面向 OpenClaw 这类 coding agent 场景，但比 `balanced` 更强调性价比。

## 官方来源

- OpenClaw 集成文档: https://openrouter.ai/docs/guides/guides/coding-agents/openclaw-integration
- OpenRouter 模型总览: https://openrouter.ai/models
- Google Gemini 3.1 Flash Lite Preview: https://openrouter.ai/google/gemini-3.1-flash-lite-preview
- Google Gemini 3 Flash Preview: https://openrouter.ai/google/gemini-3-flash-preview
- Qwen3 Coder Next: https://openrouter.ai/qwen/qwen3-coder-next
- Qwen3 Coder 480B A35B Exacto: https://openrouter.ai/qwen/qwen3-coder:exacto
- OpenAI GPT-5 Mini: https://openrouter.ai/openai/gpt-5-mini
- Exacto 变体说明: https://openrouter.ai/docs/guides/routing/model-variants/exacto

## 数据摘要

下表汇总了设计这个 profile 时主要参考的因素。

| 模型 | 上下文窗口 | 价格输入 / 输出 | 说明 |
| --- | ---: | ---: | --- |
| `google/gemini-3.1-flash-lite-preview` | 1.05M | $0.25 / $1.50 每 1M tokens | OpenRouter 将其定位为高效率模型，质量明显优于 Gemini 2.5 Flash Lite，并接近 Gemini 2.5 Flash。 |
| `qwen/qwen3-coder-next` | 262,144 | $0.12 / $0.75 每 1M tokens | OpenRouter 明确写到它针对 coding agents、本地开发工作流、长链路工具使用和失败恢复进行了优化。 |
| `qwen/qwen3-coder:exacto` | 262,144 | $0.22 / $1.80 每 1M tokens | 模型本身针对 agentic coding；`Exacto` 变体会优先选择工具调用质量更高的 provider。 |
| `google/gemini-3-flash-preview` | 1.05M | $0.50 / $3.00 每 1M tokens | OpenRouter 将其定位为高速度、高价值的 thinking model，适合 agent workflows、多轮聊天和 coding assistance。 |
| `openai/gpt-5-mini` | 400K | $0.25 / $2.00 每 1M tokens | 轻量级 GPT-5，适合较轻量但仍有推理要求的任务。 |

## 推荐 Profile

```yaml
profiles:
  value:
    simple:
      - google/gemini-3.1-flash-lite-preview
      - qwen/qwen3-coder-next
      - google/gemini-2.5-flash-lite
    medium:
      - qwen/qwen3-coder-next
      - google/gemini-3.1-flash-lite-preview
      - qwen/qwen3-coder:exacto
    complex:
      - qwen/qwen3-coder:exacto
      - google/gemini-3-flash-preview
      - qwen/qwen3-coder-next
    reasoning:
      - google/gemini-3-flash-preview
      - qwen/qwen3-coder:exacto
      - openai/gpt-5-mini
```

## 为什么这组更有性价比

这组 profile 的目标不是“绝对最便宜”，而是“在 OpenClaw coding agent 场景下，尽量把每一档的成本压低，同时不明显牺牲工具调用和编码可靠性”。

设计思路是：

1. `simple` 用 `gemini-3.1-flash-lite-preview` 起步，用 1M 上下文和较低价格覆盖大量轻量任务。
2. `medium` 用 `qwen3-coder-next` 扛住主要 coding 工作，因为它的定价很低，同时 OpenRouter 对它的 agentic coding 和失败恢复描述很强。
3. `complex` 用 `qwen3-coder:exacto`，把成本控制在明显低于闭源高端模型的水平，同时借助 `Exacto` 强化工具调用 provider 质量。
4. `reasoning` 再升到 `gemini-3-flash-preview`，用相对可控的价格换更强的 thinking / agent 行为。

## 为什么适合 OpenClaw

对于 OpenClaw 来说，单看便宜模型不够，还要看：

1. 是否有足够的上下文窗口。
2. 是否明确支持 coding / tool use / agentic workflows。
3. 在 ClawGo 当前实现下，是否能减少“因为模型本身不适合工具流”而导致的失败。

有两个关键实现约束需要注意：

- ClawGo 当前只在 `429` 和 `5xx` 时才会 fallback，不会在上下文超限等 `4xx` 上自动切换。见 [proxy.go](/Users/jay/clawgo/clawgo/proxy.go#L471)。
- 因为 `value` 里最低的上下文窗口是 262,144，所以 OpenClaw 侧应该按 `262144` 来声明 `contextWindow`，不要按 Gemini 的 1M 去写。

## Exacto 在这里的作用

`qwen/qwen3-coder:exacto` 的价值不在于模型本体变了，而在于 OpenRouter 会对 provider 排序做“质量优先”的调整。

根据 OpenRouter 的 Exacto 文档：

- `:exacto` 会优先使用工具调用质量信号更强的 provider。
- 它不走默认的价格优先排序。
- 它更适合质量敏感的 agent / tool-calling 工作负载。

因此，把 `qwen/qwen3-coder:exacto` 放在 `complex` 层，比直接放普通 `qwen/qwen3-coder` 更适合 OpenClaw。

## 取舍与替代方案

下面这些结论是基于上面官方信息做出的工程判断，不是 OpenRouter 的原文结论：

- 推断: `qwen3-coder-next` 是当前这组里最值得做中层主力的模型，因为它在价格、coding agent 定位、以及 262k 上下文之间形成了很好的平衡。
- 推断: `gemini-3.1-flash-lite-preview` 更适合放在 `simple`，而不是直接顶 `medium/complex`，因为它虽然便宜且长上下文，但官方对 coding agent 的强调不如 Qwen3 Coder 系列直接。
- 推断: `gemini-3-flash-preview` 适合作为 `reasoning` 顶层，是因为它在 agent workflows 和 thinking 能力上的官方定位，比 `gpt-5-mini` 更像一个“仍然便宜但能兜底更重任务”的选择。

如果你更偏向不同取舍，可以这样改：

- 如果你更在意超长上下文，把 `google/gemini-3-flash-preview` 提前到 `complex` 主模型。
- 如果你更在意极致成本，可以把 `google/gemini-2.5-flash-lite` 提前到 `simple` 或 `medium`。
- 如果你更看重稳定的闭源 instruction-following，可以把 `openai/gpt-5-mini` 提前到 `complex` 或 `reasoning` fallback。

## 推荐的 OpenClaw 配置项

如果你是在 OpenClaw 里把 ClawGo 暴露成一个 OpenAI 兼容 provider，建议把这组配置成单独一个模型入口：

```yaml
- id: value
  name: "ClawGo Value Router"
  reasoning: true
  input: [text]
  contextWindow: 262144
  maxTokens: 16384
  cost:
    input: 0.25
    output: 1.8
    cacheRead: 0
    cacheWrite: 0
```

这里的 `contextWindow: 262144` 是保守且正确的写法，因为 `value` profile 里混用了 1M、400K、262K 的模型，而 OpenClaw 对外应按所有层级都能稳定承诺的最小共同上限来声明。
