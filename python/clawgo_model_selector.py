from __future__ import annotations

import argparse
import json
import math
import re
from dataclasses import asdict, dataclass, field
from enum import StrEnum
from typing import Any


class Tier(StrEnum):
    SIMPLE = "SIMPLE"
    MEDIUM = "MEDIUM"
    COMPLEX = "COMPLEX"
    REASONING = "REASONING"


@dataclass(slots=True)
class DimensionScore:
    name: str
    score: float
    signal: str = ""


@dataclass(slots=True)
class ScoringResult:
    score: float
    tier: Tier | None
    confidence: float
    signals: list[str]
    agentic_score: float
    dimensions: list[DimensionScore]


@dataclass(slots=True)
class RoutingDecision:
    model: str
    tier: Tier
    confidence: float
    method: str
    reasoning: str
    cost_estimate: float
    baseline_cost: float
    savings: float
    agentic_score: float = 0.0


@dataclass(slots=True)
class CostResult:
    cost_estimate: float
    baseline_cost: float
    savings: float


@dataclass(slots=True)
class TierConfig:
    primary: str = ""
    fallback: list[str] = field(default_factory=list)


@dataclass(slots=True)
class ModelPricing:
    prompt: str = ""
    completion: str = ""


@dataclass(slots=True)
class TopProvider:
    context_length: int = 0
    max_output_tokens: int = 0
    is_moderated: bool = False


@dataclass(slots=True)
class ModelInfo:
    id: str
    name: str = ""
    pricing: ModelPricing = field(default_factory=ModelPricing)
    context_length: int = 0
    top_provider: TopProvider | None = None
    supports_tools: bool = False
    supports_vision: bool = False


@dataclass(slots=True)
class RouteResult:
    scoring: ScoringResult
    decision: RoutingDecision
    estimated_tokens: int


BASELINE_MODEL_ID = "anthropic/claude-opus-4"
BASELINE_INPUT_PRICE = 5.0
BASELINE_OUTPUT_PRICE = 25.0


def calibrate_confidence(distance: float, steepness: float) -> float:
    return 1.0 / (1.0 + math.exp(-steepness * distance))


def count_keyword_matches(text: str, keywords: list[str]) -> int:
    return sum(1 for keyword in keywords if keyword in text)


def parse_token_price(value: str) -> float:
    try:
        return float(value) * 1_000_000
    except (TypeError, ValueError):
        return 0.0


class ModelCatalog:
    def __init__(self, models: dict[str, ModelInfo] | None = None) -> None:
        self.models = models or {}

    @classmethod
    def from_openrouter_response(cls, payload: dict[str, Any]) -> "ModelCatalog":
        models: dict[str, ModelInfo] = {}
        for item in payload.get("data", []):
            architecture = item.get("architecture") or {}
            model = ModelInfo(
                id=item["id"],
                name=item.get("name", ""),
                pricing=ModelPricing(
                    prompt=(item.get("pricing") or {}).get("prompt", ""),
                    completion=(item.get("pricing") or {}).get("completion", ""),
                ),
                context_length=item.get("context_length", 0),
                top_provider=_parse_top_provider(item.get("top_provider")),
                supports_vision=architecture.get("modality") == "text+image->text",
            )
            models[model.id] = model
        return cls(models)

    def get_model(self, model_id: str) -> ModelInfo | None:
        return self.models.get(model_id)

    def get_pricing(self, model_id: str) -> tuple[float, float]:
        model = self.models.get(model_id)
        if model is None:
            return 0.0, 0.0
        return (
            parse_token_price(model.pricing.prompt),
            parse_token_price(model.pricing.completion),
        )

    def count(self) -> int:
        return len(self.models)

    def classify_by_price(self, input_price_per_m: float, output_price_per_m: float) -> Tier:
        average_price = (input_price_per_m + output_price_per_m) / 2
        if average_price < 1.0:
            return Tier.SIMPLE
        if average_price < 8.0:
            return Tier.MEDIUM
        if average_price < 20.0:
            return Tier.COMPLEX
        return Tier.REASONING


def _parse_top_provider(data: dict[str, Any] | None) -> TopProvider | None:
    if not data:
        return None
    return TopProvider(
        context_length=data.get("context_length", 0),
        max_output_tokens=data.get("max_output_tokens", 0),
        is_moderated=data.get("is_moderated", False),
    )


def default_tier_configs(profile: str) -> dict[Tier, TierConfig]:
    profile = profile.lower().strip()
    if profile == "eco":
        return {
            Tier.SIMPLE: TierConfig("google/gemini-2.5-flash-lite", ["deepseek/deepseek-chat"]),
            Tier.MEDIUM: TierConfig("google/gemini-2.5-flash-lite", ["deepseek/deepseek-chat"]),
            Tier.COMPLEX: TierConfig("google/gemini-2.5-flash", ["deepseek/deepseek-chat"]),
            Tier.REASONING: TierConfig("deepseek/deepseek-r1", ["google/gemini-2.5-flash"]),
        }
    if profile == "premium":
        return {
            Tier.SIMPLE: TierConfig("anthropic/claude-haiku", ["google/gemini-2.5-flash"]),
            Tier.MEDIUM: TierConfig(
                "anthropic/claude-sonnet-4",
                ["google/gemini-2.5-pro", "openai/gpt-4o"],
            ),
            Tier.COMPLEX: TierConfig(
                "anthropic/claude-opus-4",
                ["openai/gpt-4o", "anthropic/claude-sonnet-4"],
            ),
            Tier.REASONING: TierConfig(
                "openai/o3-pro",
                ["anthropic/claude-sonnet-4", "openai/o3"],
            ),
        }
    return {
        Tier.SIMPLE: TierConfig("google/gemini-2.5-flash-lite", ["deepseek/deepseek-chat"]),
        Tier.MEDIUM: TierConfig(
            "google/gemini-2.5-flash",
            ["deepseek/deepseek-chat", "google/gemini-2.5-flash-lite"],
        ),
        Tier.COMPLEX: TierConfig(
            "google/gemini-2.5-pro",
            ["google/gemini-2.5-flash", "deepseek/deepseek-chat"],
        ),
        Tier.REASONING: TierConfig(
            "anthropic/claude-sonnet-4",
            ["deepseek/deepseek-r1", "openai/o3"],
        ),
    }


def select_model(
    tier: Tier,
    confidence: float,
    method: str,
    reasoning: str,
    tier_configs: dict[Tier, TierConfig],
    catalog: ModelCatalog,
    estimated_input_tokens: int,
    max_output_tokens: int,
    profile: str,
    agentic_score: float,
) -> RoutingDecision:
    profile = profile.lower().strip()
    config = tier_configs[tier]
    model = config.primary

    input_price, output_price = catalog.get_pricing(model)
    input_cost = estimated_input_tokens / 1_000_000 * input_price
    output_cost = max_output_tokens / 1_000_000 * output_price
    cost_estimate = input_cost + output_cost

    baseline_input_price, baseline_output_price = catalog.get_pricing(BASELINE_MODEL_ID)
    if baseline_input_price == 0:
        baseline_input_price = BASELINE_INPUT_PRICE
    if baseline_output_price == 0:
        baseline_output_price = BASELINE_OUTPUT_PRICE
    baseline_cost = (
        estimated_input_tokens / 1_000_000 * baseline_input_price
        + max_output_tokens / 1_000_000 * baseline_output_price
    )

    savings = 0.0
    if profile != "premium" and baseline_cost > 0:
        savings = (baseline_cost - cost_estimate) / baseline_cost
        if savings < 0:
            savings = 0.0

    return RoutingDecision(
        model=model,
        tier=tier,
        confidence=confidence,
        method=method,
        reasoning=reasoning,
        cost_estimate=cost_estimate,
        baseline_cost=baseline_cost,
        savings=savings,
        agentic_score=agentic_score,
    )


def get_fallback_chain(tier: Tier, tier_configs: dict[Tier, TierConfig]) -> list[str]:
    config = tier_configs[tier]
    return [config.primary, *config.fallback]


def calculate_model_cost(
    model: str,
    catalog: ModelCatalog,
    estimated_input_tokens: int,
    max_output_tokens: int,
    profile: str,
) -> CostResult:
    profile = profile.lower().strip()
    input_price, output_price = catalog.get_pricing(model)
    cost_estimate = (
        estimated_input_tokens / 1_000_000 * input_price
        + max_output_tokens / 1_000_000 * output_price
    )

    baseline_input_price, baseline_output_price = catalog.get_pricing(BASELINE_MODEL_ID)
    if baseline_input_price == 0:
        baseline_input_price = BASELINE_INPUT_PRICE
    if baseline_output_price == 0:
        baseline_output_price = BASELINE_OUTPUT_PRICE
    baseline_cost = (
        estimated_input_tokens / 1_000_000 * baseline_input_price
        + max_output_tokens / 1_000_000 * baseline_output_price
    )

    savings = 0.0
    if profile != "premium" and baseline_cost > 0:
        savings = (baseline_cost - cost_estimate) / baseline_cost
        if savings < 0:
            savings = 0.0

    return CostResult(
        cost_estimate=cost_estimate,
        baseline_cost=baseline_cost,
        savings=savings,
    )


def filter_by_tool_calling(models: list[str], has_tools: bool, catalog: ModelCatalog) -> list[str]:
    if not has_tools:
        return models
    filtered = [model for model in models if (catalog.get_model(model) or ModelInfo(id=model)).supports_tools]
    return filtered or models


def filter_by_vision(models: list[str], has_vision: bool, catalog: ModelCatalog) -> list[str]:
    if not has_vision:
        return models
    filtered = [model for model in models if (catalog.get_model(model) or ModelInfo(id=model)).supports_vision]
    return filtered or models


class Router:
    def __init__(self) -> None:
        self.code_keywords = [
            "function", "class", "import", "def", "select", "async", "await", "const", "let", "var", "return", "```",
            "函数", "类", "导入", "定义", "查询", "异步", "等待", "常量", "变量", "返回",
            "関数", "クラス", "インポート", "非同期", "定数", "変数",
            "функция", "класс", "импорт", "определ", "запрос", "асинхронный", "ожидать", "константа", "переменная", "вернуть",
            "funktion", "klasse", "importieren", "definieren", "abfrage", "asynchron", "erwarten", "konstante", "variable", "zurückgeben",
            "función", "clase", "importar", "definir", "consulta", "asíncrono", "esperar", "constante", "variable", "retornar",
            "função", "classe", "importar", "definir", "consulta", "assíncrono", "aguardar", "constante", "variável", "retornar",
            "함수", "클래스", "가져오기", "정의", "쿼리", "비동기", "대기", "상수", "변수", "반환",
            "دالة", "فئة", "استيراد", "تعريف", "استعلام", "غير متزامن", "انتظار", "ثابت", "متغير", "إرجاع",
        ]
        self.reasoning_keywords = [
            "prove", "theorem", "derive", "step by step", "chain of thought", "formally", "mathematical", "proof", "logically",
            "证明", "定理", "推导", "逐步", "思维链", "形式化", "数学", "逻辑",
            "証明", "定理", "導出", "ステップバイステップ", "論理的",
            "доказать", "докажи", "доказательств", "теорема", "вывести", "шаг за шагом", "пошагово", "поэтапно", "цепочка рассуждений", "рассуждени", "формально", "математически", "логически",
            "beweisen", "beweis", "theorem", "ableiten", "schritt für schritt", "gedankenkette", "formal", "mathematisch", "logisch",
            "demostrar", "teorema", "derivar", "paso a paso", "cadena de pensamiento", "formalmente", "matemático", "prueba", "lógicamente",
            "provar", "teorema", "derivar", "passo a passo", "cadeia de pensamento", "formalmente", "matemático", "prova", "logicamente",
            "증명", "정리", "도출", "단계별", "사고의 연쇄", "형식적", "수학적", "논리적",
            "إثبات", "نظرية", "اشتقاق", "خطوة بخطوة", "سلسلة التفكير", "رسمياً", "رياضي", "برهان", "منطقياً",
        ]
        self.simple_keywords = [
            "what is", "define", "translate", "hello", "yes or no", "capital of", "how old", "who is", "when was",
            "什么是", "定义", "翻译", "你好", "是否", "首都", "多大", "谁是", "何时",
            "とは", "定義", "翻訳", "こんにちは", "はいかいいえ", "首都", "誰",
            "что такое", "определение", "перевести", "переведи", "привет", "да или нет", "столица", "сколько лет", "кто такой", "когда", "объясни",
            "was ist", "definiere", "übersetze", "hallo", "ja oder nein", "hauptstadt", "wie alt", "wer ist", "wann", "erkläre",
            "qué es", "definir", "traducir", "hola", "sí o no", "capital de", "cuántos años", "quién es", "cuándo",
            "o que é", "definir", "traduzir", "olá", "sim ou não", "capital de", "quantos anos", "quem é", "quando",
            "무엇", "정의", "번역", "안녕하세요", "예 또는 아니오", "수도", "누구", "언제",
            "ما هو", "تعريف", "ترجم", "مرحبا", "نعم أو لا", "عاصمة", "من هو", "متى",
        ]
        self.technical_keywords = [
            "algorithm", "optimize", "architecture", "distributed", "kubernetes", "microservice", "database", "infrastructure",
            "算法", "优化", "架构", "分布式", "微服务", "数据库", "基础设施",
            "アルゴリズム", "最適化", "アーキテクチャ", "分散", "マイクロサービス", "データベース",
            "алгоритм", "оптимизировать", "оптимизаци", "оптимизируй", "архитектура", "распределённый", "микросервис", "база данных", "инфраструктура",
            "algorithmus", "optimieren", "architektur", "verteilt", "kubernetes", "mikroservice", "datenbank", "infrastruktur",
            "algoritmo", "optimizar", "arquitectura", "distribuido", "microservicio", "base de datos", "infraestructura",
            "algoritmo", "otimizar", "arquitetura", "distribuído", "microsserviço", "banco de dados", "infraestrutura",
            "알고리즘", "최적화", "아키텍처", "분산", "마이크로서비스", "데이터베이스", "인프라",
            "خوارزمية", "تحسين", "بنية", "موزع", "خدمة مصغرة", "قاعدة بيانات", "بنية تحتية",
        ]
        self.creative_keywords = [
            "story", "poem", "compose", "brainstorm", "creative", "imagine", "write a",
            "故事", "诗", "创作", "头脑风暴", "创意", "想象", "写一个",
            "物語", "詩", "作曲", "ブレインストーム", "創造的", "想像",
            "история", "рассказ", "стихотворение", "сочинить", "сочини", "мозговой штурм", "творческий", "представить", "придумай", "напиши",
            "geschichte", "gedicht", "komponieren", "brainstorming", "kreativ", "vorstellen", "schreibe", "erzählung",
            "historia", "poema", "componer", "lluvia de ideas", "creativo", "imaginar", "escribe",
            "história", "poema", "compor", "criativo", "imaginar", "escreva",
            "이야기", "시", "작곡", "브레인스토밍", "창의적", "상상", "작성",
            "قصة", "قصيدة", "تأليف", "عصف ذهني", "إبداعي", "تخيل", "اكتب",
        ]
        self.imperative_verbs = [
            "build", "create", "implement", "design", "develop", "construct", "generate", "deploy", "configure", "set up",
            "构建", "创建", "实现", "设计", "开发", "生成", "部署", "配置", "设置",
            "構築", "作成", "実装", "設計", "開発", "生成", "デプロイ", "設定",
            "построить", "построй", "создать", "создай", "реализовать", "реализуй", "спроектировать", "разработать", "разработай", "сконструировать", "сгенерировать", "сгенерируй", "развернуть", "разверни", "настроить", "настрой",
            "erstellen", "bauen", "implementieren", "entwerfen", "entwickeln", "konstruieren", "generieren", "bereitstellen", "konfigurieren", "einrichten",
            "construir", "crear", "implementar", "diseñar", "desarrollar", "generar", "desplegar", "configurar",
            "construir", "criar", "implementar", "projetar", "desenvolver", "gerar", "implantar", "configurar",
            "구축", "생성", "구현", "설계", "개발", "배포", "설정",
            "بناء", "إنشاء", "تنفيذ", "تصميم", "تطوير", "توليد", "نشر", "إعداد",
        ]
        self.constraint_indicators = [
            "under", "at most", "at least", "within", "no more than", "o(", "maximum", "minimum", "limit", "budget",
            "不超过", "至少", "最多", "在内", "最大", "最小", "限制", "预算",
            "以下", "最大", "最小", "制限", "予算",
            "не более", "не менее", "как минимум", "в пределах", "максимум", "минимум", "ограничение", "бюджет",
            "höchstens", "mindestens", "innerhalb", "nicht mehr als", "maximal", "minimal", "grenze", "budget",
            "como máximo", "al menos", "dentro de", "no más de", "máximo", "mínimo", "límite", "presupuesto",
            "no máximo", "pelo menos", "dentro de", "não mais que", "máximo", "mínimo", "limite", "orçamento",
            "이하", "이상", "최대", "최소", "제한", "예산",
            "على الأكثر", "على الأقل", "ضمن", "لا يزيد عن", "أقصى", "أدنى", "حد", "ميزانية",
        ]
        self.output_format_keywords = [
            "json", "yaml", "xml", "table", "csv", "markdown", "schema", "format as", "structured",
            "表格", "格式化为", "结构化",
            "テーブル", "フォーマット", "構造化",
            "таблица", "форматировать как", "структурированный",
            "tabelle", "formatieren als", "strukturiert",
            "tabla", "formatear como", "estructurado",
            "tabela", "formatar como", "estruturado",
            "테이블", "형식", "구조화",
            "جدول", "تنسيق", "منظم",
        ]
        self.reference_keywords = [
            "above", "below", "previous", "following", "the docs", "the api", "the code", "earlier", "attached",
            "上面", "下面", "之前", "接下来", "文档", "代码", "附件",
            "上記", "下記", "前の", "次の", "ドキュメント", "コード",
            "выше", "ниже", "предыдущий", "следующий", "документация", "код", "ранее", "вложение",
            "oben", "unten", "vorherige", "folgende", "dokumentation", "der code", "früher", "anhang",
            "arriba", "abajo", "anterior", "siguiente", "documentación", "el código", "adjunto",
            "acima", "abaixo", "anterior", "seguinte", "documentação", "o código", "anexo",
            "위", "아래", "이전", "다음", "문서", "코드", "첨부",
            "أعلاه", "أدناه", "السابق", "التالي", "الوثائق", "الكود", "مرفق",
        ]
        self.negation_keywords = [
            "don't", "do not", "avoid", "never", "without", "except", "exclude", "no longer",
            "不要", "避免", "从不", "没有", "除了", "排除",
            "しないで", "避ける", "決して", "なしで", "除く",
            "не делай", "не надо", "нельзя", "избегать", "никогда", "без", "кроме", "исключить", "больше не",
            "nicht", "vermeide", "niemals", "ohne", "außer", "ausschließen", "nicht mehr",
            "no hagas", "evitar", "nunca", "sin", "excepto", "excluir",
            "não faça", "evitar", "nunca", "sem", "exceto", "excluir",
            "하지 마", "피하다", "절대", "없이", "제외",
            "لا تفعل", "تجنب", "أبداً", "بدون", "باستثناء", "استبعاد",
        ]
        self.domain_specific_keywords = [
            "quantum", "fpga", "vlsi", "risc-v", "asic", "photonics", "genomics", "proteomics", "topological", "homomorphic", "zero-knowledge", "lattice-based",
            "量子", "光子学", "基因组学", "蛋白质组学", "拓扑", "同态", "零知识", "格密码",
            "量子", "フォトニクス", "ゲノミクス", "トポロジカル",
            "квантовый", "фотоника", "геномика", "протеомика", "топологический", "гомоморфный", "с нулевым разглашением", "на основе решёток",
            "quanten", "photonik", "genomik", "proteomik", "topologisch", "homomorph", "zero-knowledge", "gitterbasiert",
            "cuántico", "fotónica", "genómica", "proteómica", "topológico", "homomórfico",
            "quântico", "fotônica", "genômica", "proteômica", "topológico", "homomórfico",
            "양자", "포토닉스", "유전체학", "위상", "동형",
            "كمي", "ضوئيات", "جينوميات", "طوبولوجي", "تماثلي",
        ]
        self.agentic_task_keywords = [
            "read file", "read the file", "look at", "check the", "open the", "edit", "modify", "update the", "change the", "write to", "create file", "execute", "deploy", "install", "npm", "pip", "compile", "after that", "and also", "once done", "step 1", "step 2", "fix", "debug", "until it works", "keep trying", "iterate", "make sure", "verify", "confirm",
            "读取文件", "查看", "打开", "编辑", "修改", "更新", "创建", "执行", "部署", "安装", "第一步", "第二步", "修复", "调试", "直到", "确认", "验证",
            "leer archivo", "editar", "modificar", "actualizar", "ejecutar", "desplegar", "instalar", "paso 1", "paso 2", "arreglar", "depurar", "verificar",
            "ler arquivo", "editar", "modificar", "atualizar", "executar", "implantar", "instalar", "passo 1", "passo 2", "corrigir", "depurar", "verificar",
            "파일 읽기", "편집", "수정", "업데이트", "실행", "배포", "설치", "단계 1", "단계 2", "디버그", "확인",
            "قراءة ملف", "تحرير", "تعديل", "تحديث", "تنفيذ", "نشر", "تثبيت", "الخطوة 1", "الخطوة 2", "إصلاح", "تصحيح", "تحقق",
        ]
        self.multi_step_patterns = [
            re.compile(r"first.*then"),
            re.compile(r"step \d"),
            re.compile(r"\d\.\s"),
        ]

    def classify(self, prompt: str, system_prompt: str = "", estimated_tokens: int = 0) -> ScoringResult:
        del system_prompt
        lower = prompt.lower()
        dimensions: list[DimensionScore] = []

        token_score = 0.0
        if estimated_tokens < 50:
            token_score = -1.0
        elif estimated_tokens > 500:
            token_score = 1.0
        dimensions.append(DimensionScore(name="tokenCount", score=token_score))

        code_count = count_keyword_matches(lower, self.code_keywords)
        code_score = 0.0
        code_signal = ""
        if code_count >= 2:
            code_score = 1.0
            code_signal = "code detected"
        elif code_count == 1:
            code_score = 0.5
            code_signal = "code detected"
        dimensions.append(DimensionScore(name="codePresence", score=code_score, signal=code_signal))

        reasoning_count = count_keyword_matches(lower, self.reasoning_keywords)
        reasoning_score = 0.0
        reasoning_signal = ""
        if reasoning_count >= 2:
            reasoning_score = 1.0
            reasoning_signal = "reasoning required"
        elif reasoning_count == 1:
            reasoning_score = 0.7
            reasoning_signal = "reasoning required"
        dimensions.append(DimensionScore(name="reasoningMarkers", score=reasoning_score, signal=reasoning_signal))

        technical_count = count_keyword_matches(lower, self.technical_keywords)
        technical_score = 0.0
        technical_signal = ""
        if technical_count >= 4:
            technical_score = 1.0
            technical_signal = "technical content"
        elif technical_count >= 2:
            technical_score = 0.5
            technical_signal = "technical content"
        dimensions.append(DimensionScore(name="technicalTerms", score=technical_score, signal=technical_signal))

        creative_count = count_keyword_matches(lower, self.creative_keywords)
        creative_score = 0.0
        creative_signal = ""
        if creative_count >= 2:
            creative_score = 0.7
            creative_signal = "creative task"
        elif creative_count == 1:
            creative_score = 0.5
            creative_signal = "creative task"
        dimensions.append(DimensionScore(name="creativeMarkers", score=creative_score, signal=creative_signal))

        simple_count = count_keyword_matches(lower, self.simple_keywords)
        simple_score = 0.0
        simple_signal = ""
        if simple_count >= 1:
            simple_score = -1.0
            simple_signal = "simple query"
        dimensions.append(DimensionScore(name="simpleIndicators", score=simple_score, signal=simple_signal))

        multi_step_score = 0.0
        multi_step_signal = ""
        for pattern in self.multi_step_patterns:
            if pattern.search(lower):
                multi_step_score = 0.5
                multi_step_signal = "multi-step"
                break
        dimensions.append(DimensionScore(name="multiStepPatterns", score=multi_step_score, signal=multi_step_signal))

        question_count = lower.count("?")
        question_score = 0.5 if question_count > 3 else 0.0
        dimensions.append(DimensionScore(name="questionComplexity", score=question_score))

        imperative_count = count_keyword_matches(lower, self.imperative_verbs)
        imperative_score = 0.0
        imperative_signal = ""
        if imperative_count >= 2:
            imperative_score = 0.5
            imperative_signal = "imperative"
        elif imperative_count == 1:
            imperative_score = 0.3
            imperative_signal = "imperative"
        dimensions.append(DimensionScore(name="imperativeVerbs", score=imperative_score, signal=imperative_signal))

        constraint_count = count_keyword_matches(lower, self.constraint_indicators)
        constraint_score = 0.0
        constraint_signal = ""
        if constraint_count >= 3:
            constraint_score = 0.7
            constraint_signal = "constraints"
        elif constraint_count >= 1:
            constraint_score = 0.3
            constraint_signal = "constraints"
        dimensions.append(DimensionScore(name="constraintCount", score=constraint_score, signal=constraint_signal))

        format_count = count_keyword_matches(lower, self.output_format_keywords)
        format_score = 0.0
        format_signal = ""
        if format_count >= 2:
            format_score = 0.7
            format_signal = "format specified"
        elif format_count == 1:
            format_score = 0.4
            format_signal = "format specified"
        dimensions.append(DimensionScore(name="outputFormat", score=format_score, signal=format_signal))

        reference_count = count_keyword_matches(lower, self.reference_keywords)
        reference_score = 0.0
        reference_signal = ""
        if reference_count >= 2:
            reference_score = 0.5
            reference_signal = "references"
        elif reference_count == 1:
            reference_score = 0.3
            reference_signal = "references"
        dimensions.append(DimensionScore(name="referenceComplexity", score=reference_score, signal=reference_signal))

        negation_count = count_keyword_matches(lower, self.negation_keywords)
        negation_score = 0.0
        if negation_count >= 3:
            negation_score = 0.5
        elif negation_count >= 2:
            negation_score = 0.3
        dimensions.append(DimensionScore(name="negationComplexity", score=negation_score))

        domain_count = count_keyword_matches(lower, self.domain_specific_keywords)
        domain_score = 0.0
        domain_signal = ""
        if domain_count >= 2:
            domain_score = 0.8
            domain_signal = "domain specific"
        elif domain_count == 1:
            domain_score = 0.5
            domain_signal = "domain specific"
        dimensions.append(DimensionScore(name="domainSpecificity", score=domain_score, signal=domain_signal))

        agentic_count = count_keyword_matches(lower, self.agentic_task_keywords)
        agentic_dimension_score = 0.0
        agentic_score = 0.0
        agentic_signal = ""
        if agentic_count >= 4:
            agentic_dimension_score = 1.0
            agentic_score = 1.0
            agentic_signal = "agentic task"
        elif agentic_count == 3:
            agentic_dimension_score = 0.6
            agentic_score = 0.6
            agentic_signal = "agentic task"
        elif agentic_count >= 1:
            agentic_dimension_score = 0.2
            agentic_score = 0.2
            agentic_signal = "agentic task"
        dimensions.append(DimensionScore(name="agenticTask", score=agentic_dimension_score, signal=agentic_signal))

        weights = [
            0.08,
            0.15,
            0.18,
            0.10,
            0.05,
            0.02,
            0.12,
            0.05,
            0.03,
            0.04,
            0.03,
            0.02,
            0.01,
            0.02,
            0.04,
        ]
        weighted_sum = sum(dimension.score * weight for dimension, weight in zip(dimensions, weights, strict=True))

        simple_medium = 0.0
        medium_complex = 0.3
        complex_reasoning = 0.5

        if reasoning_count >= 2:
            tier = Tier.REASONING
        elif weighted_sum <= simple_medium:
            tier = Tier.SIMPLE
        elif weighted_sum <= medium_complex:
            tier = Tier.MEDIUM
        elif weighted_sum <= complex_reasoning:
            tier = Tier.COMPLEX
        else:
            tier = Tier.REASONING

        if tier == Tier.SIMPLE:
            distance = simple_medium - weighted_sum
        elif tier == Tier.MEDIUM:
            distance = min(weighted_sum - simple_medium, medium_complex - weighted_sum)
        elif tier == Tier.COMPLEX:
            distance = min(weighted_sum - medium_complex, complex_reasoning - weighted_sum)
        elif reasoning_count >= 2:
            distance = 1.0
        else:
            distance = weighted_sum - complex_reasoning

        confidence = calibrate_confidence(distance, 12)
        resolved_tier = tier if confidence >= 0.7 else None
        signals = [dimension.signal for dimension in dimensions if dimension.signal]

        return ScoringResult(
            score=weighted_sum,
            tier=resolved_tier,
            confidence=confidence,
            signals=signals,
            agentic_score=agentic_score,
            dimensions=dimensions,
        )


def estimate_tokens(prompt: str, system_prompt: str = "") -> int:
    return len(f"{system_prompt} {prompt}") // 4


def route_prompt(
    prompt: str,
    system_prompt: str = "",
    profile: str = "auto",
    catalog: ModelCatalog | None = None,
    max_output_tokens: int = 4096,
    estimated_tokens: int | None = None,
    router: Router | None = None,
) -> RouteResult:
    profile = profile.lower().strip()
    resolved_router = router or Router()
    resolved_catalog = catalog or ModelCatalog()
    resolved_estimated_tokens = estimated_tokens
    if resolved_estimated_tokens is None:
        resolved_estimated_tokens = estimate_tokens(prompt, system_prompt)

    scoring = resolved_router.classify(prompt, system_prompt, resolved_estimated_tokens)
    tier = scoring.tier or Tier.MEDIUM
    decision = select_model(
        tier=tier,
        confidence=scoring.confidence,
        method="rules",
        reasoning=f"score={scoring.score:.2f} | {', '.join(scoring.signals)}",
        tier_configs=default_tier_configs(profile),
        catalog=resolved_catalog,
        estimated_input_tokens=resolved_estimated_tokens,
        max_output_tokens=max_output_tokens,
        profile=profile,
        agentic_score=scoring.agentic_score,
    )
    return RouteResult(scoring=scoring, decision=decision, estimated_tokens=resolved_estimated_tokens)


def _to_jsonable(value: Any) -> Any:
    if isinstance(value, list):
        return [_to_jsonable(item) for item in value]
    if isinstance(value, dict):
        return {key: _to_jsonable(item) for key, item in value.items()}
    if isinstance(value, StrEnum):
        return str(value)
    if hasattr(value, "__dataclass_fields__"):
        return _to_jsonable(asdict(value))
    return value


def main() -> None:
    parser = argparse.ArgumentParser(description="Python port of ClawGo's model routing and selection logic.")
    parser.add_argument("prompt", help="User prompt to classify and route.")
    parser.add_argument("--system-prompt", default="", help="Optional system prompt used for token estimation.")
    parser.add_argument("--profile", default="auto", help="Routing profile: auto, eco, premium.")
    parser.add_argument("--max-output-tokens", type=int, default=4096, help="max_tokens used for cost estimation.")
    parser.add_argument("--estimated-tokens", type=int, default=None, help="Override the prompt token estimate.")
    args = parser.parse_args()

    result = route_prompt(
        prompt=args.prompt,
        system_prompt=args.system_prompt,
        profile=args.profile,
        max_output_tokens=args.max_output_tokens,
        estimated_tokens=args.estimated_tokens,
    )
    print(json.dumps(_to_jsonable(result), indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
