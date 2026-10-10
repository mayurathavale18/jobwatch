import copy
import json
import sys
from types import SimpleNamespace

import pytest

from classifier_shadow import infer, packets, main, load_engine, ROLE_PROMPT, SUPPORT_TEMPLATE
from evidence_ledger import build_ledger, digest


def fixture():
    state = {"title": "Backend Engineer", "jd": "Implement Go authentication"}
    cases = [{"id": "one", "state": state, "state_sha256": digest(json.dumps(state, sort_keys=True))}]
    profile = "# production\nImplemented Go authentication middleware\n\nGo [FILL: confirm]\n"
    selections = [{"id": "one", "clauses": [{"evidence": state["jd"], "capabilities": ["Go authentication"]}]}]
    return build_ledger(cases, profile, selections, "facts.md"), cases, profile


class FakeEngine:
    budget = 20
    labels = ["contradiction", "entailment", "neutral"]
    calls = 0

    def prepare(self, item):
        return item, item.get("test_tokens", 10)

    def predict(self, item):
        self.calls += 1
        return {"contradiction": .1, "entailment": .2, "neutral": .7}


def test_support_pairs_preserve_sources_and_neutral_does_not_reject():
    ledger, cases, profile = fixture()
    items = packets(ledger, cases, profile, "support")
    assert items[0]["claim"] == "Go authentication"
    assert items[0]["evidence"] in profile
    assert len({item["id"] for item in items}) == len(items)
    results = infer(FakeEngine(), items, "support")
    assert results[0]["label"] == "neutral" and results[0]["abstained"]
    assert results[0]["advisory_only"] and not results[0]["confidence_calibrated"]
    assert results[1]["reason"] == "unresolved_candidate_fact"


def test_classification_uses_exact_title_and_clause():
    ledger, cases, profile = fixture()
    items = packets(ledger, cases, profile, "classify")
    assert len(items) == 1
    assert items[0]["title"] == cases[0]["state"]["title"]
    assert items[0]["posting_evidence"] == cases[0]["state"]["jd"]
    engine = FakeEngine()
    engine.labels = ["backend", "sales"]
    engine.predict = lambda _: {"backend": .8, "sales": .2}
    assert infer(engine, items, "classify")[0]["scope"] == "role_family"


def test_over_budget_never_calls_predict_or_truncates():
    ledger, cases, profile = fixture()
    items = packets(ledger, cases, profile, "support")[:1]
    items[0]["test_tokens"] = 21
    engine = FakeEngine()
    result = infer(engine, items, "support")[0]
    assert engine.calls == 0 and result["label"] is None
    assert result["input_tokens"] == 21 and result["reason"] == "context_budget_exceeded"


@pytest.mark.parametrize("scores", [{"entailment": 1}, {"contradiction": .1, "entailment": float("nan"), "neutral": .7},
                                    {"contradiction": .1, "entailment": True, "neutral": .7}])
def test_rejects_invalid_scores(scores):
    ledger, cases, profile = fixture()
    engine = FakeEngine()
    engine.predict = lambda _: scores
    with pytest.raises(ValueError, match="scores"):
        infer(engine, packets(ledger, cases, profile, "support")[:1], "support")


def test_tampered_ledger_is_rejected_before_inference():
    ledger, cases, profile = fixture()
    altered = copy.deepcopy(ledger)
    altered["records"][0]["clauses"][0]["evidence"] = "Invented expert consensus"
    with pytest.raises(ValueError):
        packets(altered, cases, profile, "support")


def test_existing_output_is_not_overwritten(tmp_path, monkeypatch):
    source = tmp_path / "source"
    source.write_text("keep", encoding="utf-8")
    monkeypatch.setattr(sys, "argv", ["shadow", "--mode", "support", "--ledger", str(source),
                                     "--cases", str(source), "--facts", str(source),
                                     "--checkpoint", str(tmp_path), "--out", str(source)])
    with pytest.raises(SystemExit):
        main()
    assert source.read_text(encoding="utf-8") == "keep"


@pytest.mark.parametrize("mode", ["classify", "support"])
def test_loader_counts_complete_model_framing_without_truncation(tmp_path, monkeypatch, mode):
    calls = []
    class Tokenizer:
        model_max_length = 50
        def __call__(self, *args, **kwargs):
            calls.append((args, kwargs))
            assert kwargs["truncation"] is False
            return {"input_ids": SimpleNamespace(shape=(1, 51))}
    config = SimpleNamespace(id2label={0: "neutral", 1: "contradiction", 2: "entailment"},
                             max_position_embeddings=50, architecture_type="uni-encoder",
                             encoder_config=SimpleNamespace(max_position_embeddings=50))
    class Model:
        def __init__(self):
            self.config = config
        def to(self, *_):
            return self
        def eval(self):
            return self
        def __call__(self, **_):
            raise AssertionError("An oversized packet must never reach the model")
    factory = SimpleNamespace(from_pretrained=lambda *a, **k: Model())
    class Pipeline:
        def __init__(self, *_args, **_kwargs):
            pass
        def prepare_input(self, text, labels, prompt):
            return "LABELS:" + ",".join(labels) + " PROMPT:" + prompt + " TEXT:" + text
    monkeypatch.setitem(sys.modules, "torch", SimpleNamespace(set_num_threads=lambda _: None))
    monkeypatch.setitem(sys.modules, "transformers", SimpleNamespace(
        AutoTokenizer=SimpleNamespace(from_pretrained=lambda *a, **k: Tokenizer()),
        AutoModelForSequenceClassification=factory))
    monkeypatch.setitem(sys.modules, "gliclass", SimpleNamespace(GLiClassModel=factory))
    monkeypatch.setitem(sys.modules, "gliclass.pipeline", SimpleNamespace(UniEncoderZeroShotClassificationPipeline=Pipeline))
    engine = load_engine(tmp_path, mode)
    ledger, cases, profile = fixture()
    result = infer(engine, packets(ledger, cases, profile, mode)[:1], mode)[0]
    assert result["input_tokens"] == 51 and result["reason"] == "context_budget_exceeded"
    if mode == "classify":
        assert ROLE_PROMPT in calls[0][0][0] and "LABELS:" in calls[0][0][0]
    else:
        assert calls[0][0][1] == SUPPORT_TEMPLATE.format(claim="Go authentication")
        assert engine.labels == ["neutral", "contradiction", "entailment"]


def test_missing_retrieval_is_explicit_abstention():
    ledger, cases, profile = fixture()
    selections = [{"id": "one", "clauses": [{"evidence": cases[0]["state"]["jd"], "capabilities": ["Implement"]}]}]
    ledger = build_ledger(cases, profile, selections, "facts")
    engine = FakeEngine()
    results = infer(engine, packets(ledger, cases, profile, "support"), "support")
    assert len(results) == 1 and results[0]["reason"] == "no_retrieved_evidence"
    assert engine.calls == 0


def test_support_keeps_adjacent_qualification_and_abstains():
    ledger, cases, _ = fixture()
    profile = "# production\nImplemented Go authentication middleware\n[FILL: confirm what was implemented]\n"
    selections = [{"id": "one", "clauses": [{"evidence": cases[0]["state"]["jd"], "capabilities": ["Go authentication"]}]}]
    ledger = build_ledger(cases, profile, selections, "facts")
    items = packets(ledger, cases, profile, "support")
    assert items[0]["evidence"] == profile.split("\n", 1)[1].rstrip("\n")
    assert items[0]["evidence_excerpt"] == "Implemented Go authentication middleware"
    assert items[0]["context_start_line"] == 2 and items[0]["context_end_line"] == 3
    engine = FakeEngine()
    result = infer(engine, items, "support")[0]
    assert result["reason"] == "unresolved_candidate_fact" and engine.calls == 0


@pytest.mark.parametrize("heading", ["familiar", "planned"])
def test_known_learning_level_is_not_demonstrated_support(heading):
    _, cases, _ = fixture()
    profile = f"# {heading}\nGo authentication middleware\n"
    selections = [{"id": "one", "clauses": [{"evidence": cases[0]["state"]["jd"]}]}]
    ledger = build_ledger(cases, profile, selections, "facts")
    engine = FakeEngine()
    result = infer(engine, packets(ledger, cases, profile, "support"), "support")[0]
    assert result["reason"] == "knowledge_level_not_demonstrated" and engine.calls == 0


def test_partial_context_never_reaches_model():
    _, cases, _ = fixture()
    profile = "# production\nGo authentication\n" + "detail\n" * 12
    selections = [{"id": "one", "clauses": [{"evidence": cases[0]["state"]["jd"]}]}]
    ledger = build_ledger(cases, profile, selections, "facts")
    engine = FakeEngine()
    result = infer(engine, packets(ledger, cases, profile, "support"), "support")[0]
    assert result["reason"] == "incomplete_candidate_context" and engine.calls == 0


@pytest.mark.parametrize("parent", ["planned", "familiar", "production [FILL: confirm]"])
def test_nested_fact_cannot_bypass_parent_qualification(parent):
    state = {"title": "Backend", "jd": "Kafka authentication"}
    cases = [{"id": "one", "state": state, "state_sha256": digest(json.dumps(state, sort_keys=True))}]
    profile = f"- {parent}: Kafka gateway\n  - Kafka authentication middleware\n"
    ledger = build_ledger(cases, profile, [{"id": "one", "clauses": [{"evidence": state["jd"]}]}], "facts")
    items = packets(ledger, cases, profile, "support")
    assert f"- {parent}: Kafka gateway" in items[0]["evidence"]
    engine = FakeEngine()
    result = infer(engine, items, "support")[0]
    assert result["abstained"] and engine.calls == 0


def test_contextual_restriction_overrides_production_inference_not_provenance():
    state = {"title": "Backend", "jd": "Kafka authentication"}
    cases = [{"id": "one", "state": state, "state_sha256": digest(json.dumps(state, sort_keys=True))}]
    profile = "# production\n- Kafka authentication\n  - Not production; familiar only\n"
    ledger = build_ledger(cases, profile, [{"id": "one", "clauses": [{"evidence": state["jd"]}]}], "facts")
    engine = FakeEngine()
    engine.predict = lambda _: pytest.fail("Qualified fact must not reach the model")
    result = infer(engine, packets(ledger, cases, profile, "support"), "support")[0]
    assert result["source_level"] == "production" and result["context_requires_review"]
    assert result["reason"] == "context_qualification_requires_review"
