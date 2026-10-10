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


def human_label(packet, label="strong_match", split="holdout"):
    state = {field: packet[field] for field in ("candidate_evidence", "posting_evidence")}
    return {"id": packet["id"], "label": label, "split": split, "annotator": "independent reviewer",
            "provenance": "human", "reason": "The selected API and queue work directly overlaps.",
            "packet_sha256": rs.digest(json.dumps(state, sort_keys=True))}


def test_packet_evaluation_scores_only_independent_non_null_holdout():
    posting, packet, profile = packet_case()
    predictions = rs.infer_packets(FitAgent(), [packet], [posting], profile)
    label = human_label(packet)
    report = rs.evaluate_packets([posting], [packet], profile, predictions, [label])
    assert report["accuracy_classified"] == 1
    assert report["coverage"] == 1
    label["label"] = "partial_match"
    assert rs.evaluate_packets([posting], [packet], profile, predictions, [label])["false_strong_matches"] == 1
    label["split"] = "train"
    assert rs.evaluate_packets([posting], [packet], profile, predictions, [label])["holdout_labeled"] == 0
    label.update(label=None, split="holdout", annotator=None, reason=None, provenance=None)
    report = rs.evaluate_packets([posting], [packet], profile, predictions, [label])
    assert report["accuracy_classified"] is None
    assert report["coverage"] is None


@pytest.mark.parametrize("field", ["state_sha256", "profile_sha256", "packet_sha256", "rubric_sha256"])
def test_packet_evaluation_rejects_changed_prediction_hashes(field):
    posting, packet, profile = packet_case()
    predictions = rs.infer_packets(FitAgent(), [packet], [posting], profile)
    predictions[0][field] = "changed"
    with pytest.raises(ValueError, match=field):
        rs.evaluate_packets([posting], [packet], profile, predictions, [human_label(packet)])


def test_packet_evaluation_rejects_changed_human_packet_and_selection():
    posting, packet, profile = packet_case()
    predictions = rs.infer_packets(FitAgent(), [packet], [posting], profile)
    label = human_label(packet)
    label["packet_sha256"] = "changed"
    with pytest.raises(ValueError, match="packet"):
        rs.evaluate_packets([posting], [packet], profile, predictions, [label])
    predictions[0]["selected_evidence"] = {}
    with pytest.raises(ValueError, match="selected evidence"):
        rs.evaluate_packets([posting], [packet], profile, predictions, [])


@pytest.mark.parametrize("field,value", [("label", "apply"), ("confidence", True), ("confidence", float("nan")),
                                         ("confidence", None), ("advisory_only", False), ("scope", "complete_fit"),
                                         ("abstained", True)])
def test_packet_evaluation_validates_all_predictions_even_without_labels(field, value):
    posting, packet, profile = packet_case()
    predictions = rs.infer_packets(FitAgent(), [packet], [posting], profile)
    predictions[0][field] = value
    with pytest.raises(ValueError):
        rs.evaluate_packets([posting], [packet], profile, predictions, [])


@pytest.mark.parametrize("field,value", [("provenance", "model"), ("annotator", ""), ("reason", None),
                                         ("label", "apply"), ("split", "test")])
def test_packet_evaluation_rejects_invalid_human_labels(field, value):
    posting, packet, profile = packet_case()
    label = human_label(packet)
    label[field] = value
    with pytest.raises(ValueError):
        rs.evaluate_packets([posting], [packet], profile, [], [label])


def test_packet_evaluation_separates_abstention_missing_and_null_labels():
    posting, packet, profile = packet_case()
    class NoPrediction(FitAgent):
        cfg = {"max_len": 200, "head_max_len": 192}
        def predict(self, *args):
            raise AssertionError("Over-budget packet must abstain")
    predictions = rs.infer_packets(NoPrediction(), [packet], [posting], profile)
    label = human_label(packet)
    report = rs.evaluate_packets([posting], [packet], profile, predictions, [label])
    assert report["abstained"] == 1 and report["missing_predictions"] == 0
    assert report["accuracy_classified"] is None and report["coverage"] == 0
    report = rs.evaluate_packets([posting], [packet], profile, [], [label])
    assert report["abstained"] == 0 and report["missing_predictions"] == 1
    label.update(label=None, provenance="model")
    with pytest.raises(ValueError, match="provenance"):
        rs.evaluate_packets([posting], [packet], profile, [], [label])


def test_packet_evaluation_rejects_duplicate_and_unknown_ids():
    posting, packet, profile = packet_case()
    predictions = rs.infer_packets(FitAgent(), [packet], [posting], profile)
    label = human_label(packet)
    for bad_predictions, bad_labels in ((predictions * 2, [label]), (predictions, [label] * 2),
                                        ([dict(predictions[0], id="unknown")], []), ([], [dict(label, id="unknown")])):
        with pytest.raises(ValueError, match="Duplicate or unknown"):
            rs.evaluate_packets([posting], [packet], profile, bad_predictions, bad_labels)


def test_packet_evaluation_cli_cannot_overwrite_labels(tmp_path, monkeypatch):
    import sys
    target = tmp_path / "labels.json"
    target.write_text("[]", encoding="utf-8")
    monkeypatch.setattr(sys, "argv", ["rank_shadow.py", "evaluate-packets", "--labels", str(target), "--out", str(target)])
    with pytest.raises(SystemExit):
        rs.main()
    assert target.read_text(encoding="utf-8") == "[]"


def test_packet_evaluation_cli_reads_frozen_inputs(tmp_path, monkeypatch):
    import sys
    posting, packet, profile = packet_case()
    values = {"cases": [posting], "packets": [packet], "predictions": {"predictions": rs.infer_packets(FitAgent(), [packet], [posting], profile)},
              "labels": [human_label(packet)]}
    argv = ["rank_shadow.py", "evaluate-packets"]
    for name, value in values.items():
        path = tmp_path / (name + ".json")
        path.write_text(json.dumps(value), encoding="utf-8")
        argv.extend(["--" + name, str(path)])
    profile_path = tmp_path / "profile.txt"
    profile_path.write_text(profile, encoding="utf-8")
    output = tmp_path / "report.json"
    argv.extend(["--profile", str(profile_path), "--out", str(output)])
    monkeypatch.setattr(sys, "argv", argv)
    rs.main()
    assert json.loads(output.read_text(encoding="utf-8"))["accuracy_classified"] == 1
