import unittest

from clawgo_model_selector import (
    ModelCatalog,
    Router,
    Tier,
    calculate_model_cost,
    calibrate_confidence,
    default_tier_configs,
    get_fallback_chain,
    route_prompt,
    select_model,
)


class RouterTests(unittest.TestCase):
    def test_classify_simple(self) -> None:
        router = Router()
        result = router.classify("hello, how are you?", "", 10)
        self.assertEqual(result.tier, Tier.SIMPLE)

    def test_classify_reasoning(self) -> None:
        router = Router()
        result = router.classify("prove this theorem step by step using mathematical proof", "", 100)
        self.assertEqual(result.tier, Tier.REASONING)

    def test_classify_code(self) -> None:
        router = Router()
        result = router.classify("implement a function that uses async await with import and class", "", 200)
        self.assertIsNotNone(result.tier)
        self.assertIn(result.tier, {Tier.MEDIUM, Tier.COMPLEX, Tier.REASONING})

    def test_classify_agentic(self) -> None:
        router = Router()
        result = router.classify("read file, edit the code, fix the bug, then deploy and verify", "", 100)
        self.assertGreaterEqual(result.agentic_score, 0.5)

    def test_sigmoid_confidence(self) -> None:
        self.assertGreater(calibrate_confidence(0.5, 12), 0.9)
        self.assertAlmostEqual(calibrate_confidence(0, 12), 0.5, delta=0.01)


class SelectorTests(unittest.TestCase):
    def test_select_model(self) -> None:
        catalog = ModelCatalog()
        tier_configs = default_tier_configs("auto")
        decision = select_model(
            Tier.SIMPLE,
            0.9,
            "rules",
            "test",
            tier_configs,
            catalog,
            1000,
            500,
            "auto",
            0.0,
        )
        self.assertTrue(decision.model)
        self.assertEqual(decision.tier, Tier.SIMPLE)
        self.assertEqual(decision.confidence, 0.9)

    def test_get_fallback_chain(self) -> None:
        tier_configs = default_tier_configs("auto")
        chain = get_fallback_chain(Tier.MEDIUM, tier_configs)
        self.assertGreaterEqual(len(chain), 2)
        self.assertEqual(chain[0], tier_configs[Tier.MEDIUM].primary)

    def test_calculate_model_cost(self) -> None:
        catalog = ModelCatalog()
        cost = calculate_model_cost("test-model", catalog, 1000, 500, "auto")
        self.assertEqual(cost.cost_estimate, 0.0)

    def test_select_model_savings(self) -> None:
        catalog = ModelCatalog()
        tier_configs = default_tier_configs("premium")
        decision = select_model(
            Tier.COMPLEX,
            0.95,
            "rules",
            "test",
            tier_configs,
            catalog,
            1000,
            500,
            "premium",
            0.0,
        )
        self.assertEqual(decision.savings, 0.0)

    def test_route_prompt_defaults_ambiguous_to_medium(self) -> None:
        result = route_prompt("build a thing", profile="auto", estimated_tokens=100)
        self.assertEqual(result.decision.tier, Tier.MEDIUM)


if __name__ == "__main__":
    unittest.main()
