import json
import pytest
import rank_shadow as rs


class Tokenizer:
    def encode(self, text, add_special_tokens=False):
        return list(text.split())


class Agent:
    cfg = {"max_len":512,"head_max_len":192}
    tok = Tokenizer()
    def predict(self,state,questions):
        return {"answers":{"priority":{"choice":"prioritize","answer_confidence":0.8}}}


def case(label="prioritize", split="holdout"):
    return {"id":"1","state":{"title":"Engineer","jd":"Go Kubernetes SQL experience. "*30},"label":label,"split":split}


def test_inference_abstains_instead_of_truncating():
    normal = case()
    predictions = rs.infer_cases(Agent(),[normal],"Backend engineer in India")
    assert predictions[0]["label"] == "prioritize"
    long = case()
    long["state"]["jd"] = "word "*1000
    prediction = rs.infer_cases(Agent(),[long],"Backend engineer")[0]
    assert prediction["abstained"] is True
    assert prediction["reason"] == "context_budget_exceeded"


def test_evaluation_uses_only_holdout_and_checks_evidence():
    normal = case()
    predictions = rs.infer_cases(Agent(),[normal],"Backend engineer")
    report = rs.evaluate([normal],predictions,"Backend engineer")
    assert report["accuracy_classified"] == 1
    assert report["false_rejections"] == 0
    assert rs.evaluate([case(split="train")],predictions,"Backend engineer")["classified"] == 0
    normal["state"]["jd"] += "changed"
    with pytest.raises(ValueError,match="evidence"):
        rs.evaluate([normal],predictions,"Backend engineer")


def test_evaluation_separates_abstentions_and_missing_predictions():
    normal = case()
    predictions = rs.infer_cases(Agent(),[normal],"Profile")
    predictions[0].update(abstained=True,label="review",confidence=None)
    report = rs.evaluate([normal],predictions,"Profile")
    assert report["classified"] == 0
    assert report["abstained"] == 1
    assert report["accuracy_classified"] is None
    assert rs.evaluate([normal],[],"Profile")["matched"] == 0
    with pytest.raises(ValueError,match="Duplicate"):
        rs.evaluate([normal],predictions+predictions,"Profile")


def test_evaluation_rejects_changed_candidate_profile():
    normal = case()
    predictions = rs.infer_cases(Agent(),[normal],"Old profile")
    with pytest.raises(ValueError,match="profile"):
        rs.evaluate([normal],predictions,"Changed profile")


def test_context_boundary_reserves_special_tokens():
    class FixedTokenizer:
        def encode(self,text,add_special_tokens=False):
            return list(range(317))
    agent = Agent()
    agent.tok = FixedTokenizer()
    prediction = rs.infer_cases(agent,[case()],"Profile")[0]
    assert prediction["state_budget"] == 316
    assert prediction["abstained"] is True


def test_evaluation_rejects_changed_rubric():
    normal = case()
    prediction = rs.infer_cases(Agent(),[normal],"Profile")[0]
    prediction["rubric_sha256"] = "different"
    with pytest.raises(ValueError,match="rubric"):
        rs.evaluate([normal],[prediction],"Profile")
