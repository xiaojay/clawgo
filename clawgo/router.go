package clawgo

import (
	"math"
	"regexp"
	"strings"

	"github.com/anthropics/clawgo/clawgo/schema"
)

// Router implements a 14-dimension weighted classifier for prompt complexity.
type Router struct {
	codeKeywords           []string
	reasoningKeywords      []string
	simpleKeywords         []string
	technicalKeywords      []string
	creativeKeywords       []string
	imperativeVerbs        []string
	constraintIndicators   []string
	outputFormatKeywords   []string
	referenceKeywords      []string
	negationKeywords       []string
	domainSpecificKeywords []string
	agenticTaskKeywords    []string
	multiStepPatterns      []*regexp.Regexp
}

// NewRouter creates a Router with all multilingual keyword lists.
func NewRouter() *Router {
	return &Router{
		codeKeywords: []string{
			"function", "class", "import", "def", "select", "async", "await", "const", "let", "var", "return", "```",
			"函数", "类", "导入", "定义", "查询", "异步", "等待", "常量", "变量", "返回",
			"関数", "クラス", "インポート", "非同期", "定数", "変数",
			"функция", "класс", "импорт", "определ", "запрос", "асинхронный", "ожидать", "константа", "переменная", "вернуть",
			"funktion", "klasse", "importieren", "definieren", "abfrage", "asynchron", "erwarten", "konstante", "variable", "zurückgeben",
			"función", "clase", "importar", "definir", "consulta", "asíncrono", "esperar", "constante", "variable", "retornar",
			"função", "classe", "importar", "definir", "consulta", "assíncrono", "aguardar", "constante", "variável", "retornar",
			"함수", "클래스", "가져오기", "정의", "쿼리", "비동기", "대기", "상수", "변수", "반환",
			"دالة", "فئة", "استيراد", "تعريف", "استعلام", "غير متزامن", "انتظار", "ثابت", "متغير", "إرجاع",
		},
		reasoningKeywords: []string{
			"prove", "theorem", "derive", "step by step", "chain of thought", "formally", "mathematical", "proof", "logically",
			"证明", "定理", "推导", "逐步", "思维链", "形式化", "数学", "逻辑",
			"証明", "定理", "導出", "ステップバイステップ", "論理的",
			"доказать", "докажи", "доказательств", "теорема", "вывести", "шаг за шагом", "пошагово", "поэтапно", "цепочка рассуждений", "рассуждени", "формально", "математически", "логически",
			"beweisen", "beweis", "theorem", "ableiten", "schritt für schritt", "gedankenkette", "formal", "mathematisch", "logisch",
			"demostrar", "teorema", "derivar", "paso a paso", "cadena de pensamiento", "formalmente", "matemático", "prueba", "lógicamente",
			"provar", "teorema", "derivar", "passo a passo", "cadeia de pensamento", "formalmente", "matemático", "prova", "logicamente",
			"증명", "정리", "도출", "단계별", "사고의 연쇄", "형식적", "수학적", "논리적",
			"إثبات", "نظرية", "اشتقاق", "خطوة بخطوة", "سلسلة التفكير", "رسمياً", "رياضي", "برهان", "منطقياً",
		},
		simpleKeywords: []string{
			"what is", "define", "translate", "hello", "yes or no", "capital of", "how old", "who is", "when was",
			"什么是", "定义", "翻译", "你好", "是否", "首都", "多大", "谁是", "何时",
			"とは", "定義", "翻訳", "こんにちは", "はいかいいえ", "首都", "誰",
			"что такое", "определение", "перевести", "переведи", "привет", "да или нет", "столица", "сколько лет", "кто такой", "когда", "объясни",
			"was ist", "definiere", "übersetze", "hallo", "ja oder nein", "hauptstadt", "wie alt", "wer ist", "wann", "erkläre",
			"qué es", "definir", "traducir", "hola", "sí o no", "capital de", "cuántos años", "quién es", "cuándo",
			"o que é", "definir", "traduzir", "olá", "sim ou não", "capital de", "quantos anos", "quem é", "quando",
			"무엇", "정의", "번역", "안녕하세요", "예 또는 아니오", "수도", "누구", "언제",
			"ما هو", "تعريف", "ترجم", "مرحبا", "نعم أو لا", "عاصمة", "من هو", "متى",
		},
		technicalKeywords: []string{
			"algorithm", "optimize", "architecture", "distributed", "kubernetes", "microservice", "database", "infrastructure",
			"算法", "优化", "架构", "分布式", "微服务", "数据库", "基础设施",
			"アルゴリズム", "最適化", "アーキテクチャ", "分散", "マイクロサービス", "データベース",
			"алгоритм", "оптимизировать", "оптимизаци", "оптимизируй", "архитектура", "распределённый", "микросервис", "база данных", "инфраструктура",
			"algorithmus", "optimieren", "architektur", "verteilt", "kubernetes", "mikroservice", "datenbank", "infrastruktur",
			"algoritmo", "optimizar", "arquitectura", "distribuido", "microservicio", "base de datos", "infraestructura",
			"algoritmo", "otimizar", "arquitetura", "distribuído", "microsserviço", "banco de dados", "infraestrutura",
			"알고리즘", "최적화", "아키텍처", "분산", "마이크로서비스", "데이터베이스", "인프라",
			"خوارزمية", "تحسين", "بنية", "موزع", "خدمة مصغرة", "قاعدة بيانات", "بنية تحتية",
		},
		creativeKeywords: []string{
			"story", "poem", "compose", "brainstorm", "creative", "imagine", "write a",
			"故事", "诗", "创作", "头脑风暴", "创意", "想象", "写一个",
			"物語", "詩", "作曲", "ブレインストーム", "創造的", "想像",
			"история", "рассказ", "стихотворение", "сочинить", "сочини", "мозговой штурм", "творческий", "представить", "придумай", "напиши",
			"geschichte", "gedicht", "komponieren", "brainstorming", "kreativ", "vorstellen", "schreibe", "erzählung",
			"historia", "poema", "componer", "lluvia de ideas", "creativo", "imaginar", "escribe",
			"história", "poema", "compor", "criativo", "imaginar", "escreva",
			"이야기", "시", "작곡", "브레인스토밍", "창의적", "상상", "작성",
			"قصة", "قصيدة", "تأليف", "عصف ذهني", "إبداعي", "تخيل", "اكتب",
		},
		imperativeVerbs: []string{
			"build", "create", "implement", "design", "develop", "construct", "generate", "deploy", "configure", "set up",
			"构建", "创建", "实现", "设计", "开发", "生成", "部署", "配置", "设置",
			"構築", "作成", "実装", "設計", "開発", "生成", "デプロイ", "設定",
			"построить", "построй", "создать", "создай", "реализовать", "реализуй", "спроектировать", "разработать", "разработай", "сконструировать", "сгенерировать", "сгенерируй", "развернуть", "разверни", "настроить", "настрой",
			"erstellen", "bauen", "implementieren", "entwerfen", "entwickeln", "konstruieren", "generieren", "bereitstellen", "konfigurieren", "einrichten",
			"construir", "crear", "implementar", "diseñar", "desarrollar", "generar", "desplegar", "configurar",
			"construir", "criar", "implementar", "projetar", "desenvolver", "gerar", "implantar", "configurar",
			"구축", "생성", "구현", "설계", "개발", "배포", "설정",
			"بناء", "إنشاء", "تنفيذ", "تصميم", "تطوير", "توليد", "نشر", "إعداد",
		},
		constraintIndicators: []string{
			"under", "at most", "at least", "within", "no more than", "o(", "maximum", "minimum", "limit", "budget",
			"不超过", "至少", "最多", "在内", "最大", "最小", "限制", "预算",
			"以下", "最大", "最小", "制限", "予算",
			"не более", "не менее", "как минимум", "в пределах", "максимум", "минимум", "ограничение", "бюджет",
			"höchstens", "mindestens", "innerhalb", "nicht mehr als", "maximal", "minimal", "grenze", "budget",
			"como máximo", "al menos", "dentro de", "no más de", "máximo", "mínimo", "límite", "presupuesto",
			"no máximo", "pelo menos", "dentro de", "não mais que", "máximo", "mínimo", "limite", "orçamento",
			"이하", "이상", "최대", "최소", "제한", "예산",
			"على الأكثر", "على الأقل", "ضمن", "لا يزيد عن", "أقصى", "أدنى", "حد", "ميزانية",
		},
		outputFormatKeywords: []string{
			"json", "yaml", "xml", "table", "csv", "markdown", "schema", "format as", "structured",
			"表格", "格式化为", "结构化",
			"テーブル", "フォーマット", "構造化",
			"таблица", "форматировать как", "структурированный",
			"tabelle", "formatieren als", "strukturiert",
			"tabla", "formatear como", "estructurado",
			"tabela", "formatar como", "estruturado",
			"테이블", "형식", "구조화",
			"جدول", "تنسيق", "منظم",
		},
		referenceKeywords: []string{
			"above", "below", "previous", "following", "the docs", "the api", "the code", "earlier", "attached",
			"上面", "下面", "之前", "接下来", "文档", "代码", "附件",
			"上記", "下記", "前の", "次の", "ドキュメント", "コード",
			"выше", "ниже", "предыдущий", "следующий", "документация", "код", "ранее", "вложение",
			"oben", "unten", "vorherige", "folgende", "dokumentation", "der code", "früher", "anhang",
			"arriba", "abajo", "anterior", "siguiente", "documentación", "el código", "adjunto",
			"acima", "abaixo", "anterior", "seguinte", "documentação", "o código", "anexo",
			"위", "아래", "이전", "다음", "문서", "코드", "첨부",
			"أعلاه", "أدناه", "السابق", "التالي", "الوثائق", "الكود", "مرفق",
		},
		negationKeywords: []string{
			"don't", "do not", "avoid", "never", "without", "except", "exclude", "no longer",
			"不要", "避免", "从不", "没有", "除了", "排除",
			"しないで", "避ける", "決して", "なしで", "除く",
			"не делай", "не надо", "нельзя", "избегать", "никогда", "без", "кроме", "исключить", "больше не",
			"nicht", "vermeide", "niemals", "ohne", "außer", "ausschließen", "nicht mehr",
			"no hagas", "evitar", "nunca", "sin", "excepto", "excluir",
			"não faça", "evitar", "nunca", "sem", "exceto", "excluir",
			"하지 마", "피하다", "절대", "없이", "제외",
			"لا تفعل", "تجنب", "أبداً", "بدون", "باستثناء", "استبعاد",
		},
		domainSpecificKeywords: []string{
			"quantum", "fpga", "vlsi", "risc-v", "asic", "photonics", "genomics", "proteomics", "topological", "homomorphic", "zero-knowledge", "lattice-based",
			"量子", "光子学", "基因组学", "蛋白质组学", "拓扑", "同态", "零知识", "格密码",
			"量子", "フォトニクス", "ゲノミクス", "トポロジカル",
			"квантовый", "фотоника", "геномика", "протеомика", "топологический", "гомоморфный", "с нулевым разглашением", "на основе решёток",
			"quanten", "photonik", "genomik", "proteomik", "topologisch", "homomorph", "zero-knowledge", "gitterbasiert",
			"cuántico", "fotónica", "genómica", "proteómica", "topológico", "homomórfico",
			"quântico", "fotônica", "genômica", "proteômica", "topológico", "homomórfico",
			"양자", "포토닉스", "유전체학", "위상", "동형",
			"كمي", "ضوئيات", "جينوميات", "طوبولوجي", "تماثلي",
		},
		agenticTaskKeywords: []string{
			"read file", "read the file", "look at", "check the", "open the", "edit", "modify", "update the", "change the", "write to", "create file", "execute", "deploy", "install", "npm", "pip", "compile", "after that", "and also", "once done", "step 1", "step 2", "fix", "debug", "until it works", "keep trying", "iterate", "make sure", "verify", "confirm",
			"读取文件", "查看", "打开", "编辑", "修改", "更新", "创建", "执行", "部署", "安装", "第一步", "第二步", "修复", "调试", "直到", "确认", "验证",
			"leer archivo", "editar", "modificar", "actualizar", "ejecutar", "desplegar", "instalar", "paso 1", "paso 2", "arreglar", "depurar", "verificar",
			"ler arquivo", "editar", "modificar", "atualizar", "executar", "implantar", "instalar", "passo 1", "passo 2", "corrigir", "depurar", "verificar",
			"파일 읽기", "편집", "수정", "업데이트", "실행", "배포", "설치", "단계 1", "단계 2", "디버그", "확인",
			"قراءة ملف", "تحرير", "تعديل", "تحديث", "تنفيذ", "نشر", "تثبيت", "الخطوة 1", "الخطوة 2", "إصلاح", "تصحيح", "تحقق",
		},
		multiStepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`first.*then`),
			regexp.MustCompile(`step \d`),
			regexp.MustCompile(`\d\.\s`),
		},
	}
}

// calibrateConfidence computes sigmoid confidence from distance and steepness.
func calibrateConfidence(distance, steepness float64) float64 {
	return 1.0 / (1.0 + math.Exp(-steepness*distance))
}

// countKeywordMatches counts how many keywords from the list appear in the lowered text.
func countKeywordMatches(text string, keywords []string) int {
	count := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			count++
		}
	}
	return count
}

// Classify scores a prompt across 14 dimensions and returns a ScoringResult.
func (r *Router) Classify(prompt, systemPrompt string, estimatedTokens int64) schema.ScoringResult {
	lower := strings.ToLower(prompt)
	dimensions := make([]schema.DimensionScore, 0, 15)

	// 1. tokenCount (weight 0.08)
	var tokenScore float64
	if estimatedTokens < 50 {
		tokenScore = -1.0
	} else if estimatedTokens > 500 {
		tokenScore = 1.0
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "tokenCount", Score: tokenScore})

	// 2. codePresence (weight 0.15)
	codeCount := countKeywordMatches(lower, r.codeKeywords)
	var codeScore float64
	var codeSignal string
	if codeCount >= 2 {
		codeScore = 1.0
		codeSignal = "code detected"
	} else if codeCount == 1 {
		codeScore = 0.5
		codeSignal = "code detected"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "codePresence", Score: codeScore, Signal: codeSignal})

	// 3. reasoningMarkers (weight 0.18)
	reasoningCount := countKeywordMatches(lower, r.reasoningKeywords)
	var reasoningScore float64
	var reasoningSignal string
	if reasoningCount >= 2 {
		reasoningScore = 1.0
		reasoningSignal = "reasoning required"
	} else if reasoningCount == 1 {
		reasoningScore = 0.7
		reasoningSignal = "reasoning required"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "reasoningMarkers", Score: reasoningScore, Signal: reasoningSignal})

	// 4. technicalTerms (weight 0.10)
	techCount := countKeywordMatches(lower, r.technicalKeywords)
	var techScore float64
	var techSignal string
	if techCount >= 4 {
		techScore = 1.0
		techSignal = "technical content"
	} else if techCount >= 2 {
		techScore = 0.5
		techSignal = "technical content"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "technicalTerms", Score: techScore, Signal: techSignal})

	// 5. creativeMarkers (weight 0.05)
	creativeCount := countKeywordMatches(lower, r.creativeKeywords)
	var creativeScore float64
	var creativeSignal string
	if creativeCount >= 2 {
		creativeScore = 0.7
		creativeSignal = "creative task"
	} else if creativeCount == 1 {
		creativeScore = 0.5
		creativeSignal = "creative task"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "creativeMarkers", Score: creativeScore, Signal: creativeSignal})

	// 6. simpleIndicators (weight 0.02)
	simpleCount := countKeywordMatches(lower, r.simpleKeywords)
	var simpleScore float64
	var simpleSignal string
	if simpleCount >= 1 {
		simpleScore = -1.0
		simpleSignal = "simple query"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "simpleIndicators", Score: simpleScore, Signal: simpleSignal})

	// 7. multiStepPatterns (weight 0.12)
	var multiStepScore float64
	var multiStepSignal string
	for _, pat := range r.multiStepPatterns {
		if pat.MatchString(lower) {
			multiStepScore = 0.5
			multiStepSignal = "multi-step"
			break
		}
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "multiStepPatterns", Score: multiStepScore, Signal: multiStepSignal})

	// 8. questionComplexity (weight 0.05)
	questionCount := strings.Count(lower, "?")
	var questionScore float64
	if questionCount > 3 {
		questionScore = 0.5
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "questionComplexity", Score: questionScore})

	// 9. imperativeVerbs (weight 0.03)
	impCount := countKeywordMatches(lower, r.imperativeVerbs)
	var impScore float64
	var impSignal string
	if impCount >= 2 {
		impScore = 0.5
		impSignal = "imperative"
	} else if impCount == 1 {
		impScore = 0.3
		impSignal = "imperative"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "imperativeVerbs", Score: impScore, Signal: impSignal})

	// 10. constraintCount (weight 0.04)
	constraintCount := countKeywordMatches(lower, r.constraintIndicators)
	var constraintScore float64
	var constraintSignal string
	if constraintCount >= 3 {
		constraintScore = 0.7
		constraintSignal = "constraints"
	} else if constraintCount >= 1 {
		constraintScore = 0.3
		constraintSignal = "constraints"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "constraintCount", Score: constraintScore, Signal: constraintSignal})

	// 11. outputFormat (weight 0.03)
	formatCount := countKeywordMatches(lower, r.outputFormatKeywords)
	var formatScore float64
	var formatSignal string
	if formatCount >= 2 {
		formatScore = 0.7
		formatSignal = "format specified"
	} else if formatCount == 1 {
		formatScore = 0.4
		formatSignal = "format specified"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "outputFormat", Score: formatScore, Signal: formatSignal})

	// 12. referenceComplexity (weight 0.02)
	refCount := countKeywordMatches(lower, r.referenceKeywords)
	var refScore float64
	var refSignal string
	if refCount >= 2 {
		refScore = 0.5
		refSignal = "references"
	} else if refCount == 1 {
		refScore = 0.3
		refSignal = "references"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "referenceComplexity", Score: refScore, Signal: refSignal})

	// 13. negationComplexity (weight 0.01)
	negCount := countKeywordMatches(lower, r.negationKeywords)
	var negScore float64
	if negCount >= 3 {
		negScore = 0.5
	} else if negCount >= 2 {
		negScore = 0.3
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "negationComplexity", Score: negScore})

	// 14. domainSpecificity (weight 0.02)
	domainCount := countKeywordMatches(lower, r.domainSpecificKeywords)
	var domainScore float64
	var domainSignal string
	if domainCount >= 2 {
		domainScore = 0.8
		domainSignal = "domain specific"
	} else if domainCount == 1 {
		domainScore = 0.5
		domainSignal = "domain specific"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "domainSpecificity", Score: domainScore, Signal: domainSignal})

	// agenticTask (weight 0.04)
	agenticCount := countKeywordMatches(lower, r.agenticTaskKeywords)
	var agenticDimScore float64
	var agenticScore float64
	var agenticSignal string
	if agenticCount >= 4 {
		agenticDimScore = 1.0
		agenticScore = 1.0
		agenticSignal = "agentic task"
	} else if agenticCount == 3 {
		agenticDimScore = 0.6
		agenticScore = 0.6
		agenticSignal = "agentic task"
	} else if agenticCount >= 1 {
		agenticDimScore = 0.2
		agenticScore = 0.2
		agenticSignal = "agentic task"
	}
	dimensions = append(dimensions, schema.DimensionScore{Name: "agenticTask", Score: agenticDimScore, Signal: agenticSignal})

	// Weights corresponding to each dimension (order must match dimensions slice)
	weights := []float64{
		0.08, // tokenCount
		0.15, // codePresence
		0.18, // reasoningMarkers
		0.10, // technicalTerms
		0.05, // creativeMarkers
		0.02, // simpleIndicators
		0.12, // multiStepPatterns
		0.05, // questionComplexity
		0.03, // imperativeVerbs
		0.04, // constraintCount
		0.03, // outputFormat
		0.02, // referenceComplexity
		0.01, // negationComplexity
		0.02, // domainSpecificity
		0.04, // agenticTask
	}

	// Compute weighted sum
	weightedSum := 0.0
	for i, dim := range dimensions {
		weightedSum += dim.Score * weights[i]
	}

	// Tier boundaries
	const (
		simpleMedium     = 0.0
		mediumComplex    = 0.3
		complexReasoning = 0.5
	)

	// Override: 2+ reasoning keywords → force REASONING
	var tier schema.Tier
	if reasoningCount >= 2 {
		tier = schema.TierReasoning
	} else if weightedSum <= simpleMedium {
		tier = schema.TierSimple
	} else if weightedSum <= mediumComplex {
		tier = schema.TierMedium
	} else if weightedSum <= complexReasoning {
		tier = schema.TierComplex
	} else {
		tier = schema.TierReasoning
	}

	// Compute distance from nearest boundary for confidence
	var distance float64
	switch tier {
	case schema.TierSimple:
		distance = simpleMedium - weightedSum
	case schema.TierMedium:
		d1 := weightedSum - simpleMedium
		d2 := mediumComplex - weightedSum
		distance = math.Min(d1, d2)
	case schema.TierComplex:
		d1 := weightedSum - mediumComplex
		d2 := complexReasoning - weightedSum
		distance = math.Min(d1, d2)
	case schema.TierReasoning:
		if reasoningCount >= 2 {
			distance = 1.0 // high confidence for override
		} else {
			distance = weightedSum - complexReasoning
		}
	}

	confidence := calibrateConfidence(distance, 12)

	// Confidence threshold: <0.7 → tier=nil (ambiguous)
	var tierPtr *schema.Tier
	if confidence >= 0.7 {
		tierPtr = &tier
	}

	// Collect signals
	var signals []string
	for _, dim := range dimensions {
		if dim.Signal != "" {
			signals = append(signals, dim.Signal)
		}
	}

	return schema.ScoringResult{
		Score:        weightedSum,
		Tier:         tierPtr,
		Confidence:   confidence,
		Signals:      signals,
		AgenticScore: agenticScore,
		Dimensions:   dimensions,
	}
}
