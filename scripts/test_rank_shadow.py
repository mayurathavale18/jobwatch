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


def packet_case():
    profile = "Built Python APIs and durable queues in production."
    posting = case()
    posting["state"]["jd"] = "Build Python APIs and durable queues."
    packet = {"id": posting["id"], "selection_version": 1, "state_sha256": rs.digest(json.dumps(posting["state"], sort_keys=True)),
              "profile_sha256": rs.digest(profile), "candidate_evidence": [profile],
              "posting_evidence": [posting["state"]["jd"]]}
    return posting, packet, profile


class FitAgent(Agent):
    def predict(self, state, questions):
        assert questions == rs.FIT_QUESTION
        assert set(state) == {"candidate_evidence", "posting_evidence"}
        return {"answers": {"responsibility_fit": {"choice": "strong_match", "answer_confidence": 0.8}}}


def test_packets_are_advisory_and_require_exact_frozen_evidence():
    posting, packet, profile = packet_case()
    result = rs.infer_packets(FitAgent(), [packet], [posting], profile)[0]
    assert result["label"] == "strong_match"
    assert result["advisory_only"] is True
    assert result["scope"] == "selected_responsibilities_only"
    packet["candidate_evidence"] = ["Invented ten years of experience"]
    with pytest.raises(ValueError, match="exact source"):
        rs.infer_packets(FitAgent(), [packet], [posting], profile)


def test_packets_reject_changed_profile_posting_and_duplicate_ids():
    posting, packet, profile = packet_case()
    with pytest.raises(ValueError, match="profile"):
        rs.validate_packets([packet], [posting], profile + "changed")
    with pytest.raises(ValueError, match="Duplicate"):
        rs.validate_packets([packet, packet], [posting], profile)
    posting["state"]["jd"] += "changed"
    with pytest.raises(ValueError, match="changed"):
        rs.validate_packets([packet], [posting], profile)


def test_packet_budget_never_truncates_or_executes_model():
    posting, packet, profile = packet_case()
    class NoPrediction(FitAgent):
        cfg = {"max_len": 200, "head_max_len": 192}
        def predict(self, *args):
            raise AssertionError("Over-budget packet must abstain")
    result = rs.infer_packets(NoPrediction(), [packet], [posting], profile)[0]
    assert result["abstained"] is True
    assert result["reason"] == "context_budget_exceeded"


@pytest.mark.parametrize("confidence", [True, float("nan"), -1, 1.1, "0.8"])
def test_packet_output_validation(confidence):
    posting, packet, profile = packet_case()
    class BadAgent(FitAgent):
        def predict(self, *args):
            return {"answers": {"responsibility_fit": {"choice": "strong_match", "answer_confidence": confidence}}}
    with pytest.raises(ValueError, match="Invalid"):
        rs.infer_packets(BadAgent(), [packet], [posting], profile)
