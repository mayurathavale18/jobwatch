import copy
import json
import urllib.error

import pytest

from jev_shadow import run, parse_response, request, NoRedirect, MODEL
from rank_shadow import digest, evaluate_packets


def fixture():
    profile = "Implemented Go services"
    state = {"title": "Backend", "jd": "Build Go services"}
    cases = [{"id": "one", "state": state}]
    packets = [{"id": "one", "selection_version": 1, "state_sha256": digest(json.dumps(state, sort_keys=True)),
                "profile_sha256": digest(profile), "candidate_evidence": [profile], "posting_evidence": [state["jd"]]}]
    return packets, cases, profile


def answer():
    return {"model": MODEL, "answers": {"responsibility_fit": {"type": "choice", "choice": "partial_match",
        "confidence": .6, "probabilities": {"strong_match": .1, "partial_match": .8, "insufficient_evidence": .1}}},
        "usage": {"input_tokens": 120, "output_tokens": 5}}


def test_success_preserves_frozen_evidence_and_reuses_journal_without_billing(tmp_path):
    packets, cases, profile = fixture()
    out = tmp_path / "predictions.json"
    calls = []
    def requester(body, key):
        calls.append(body)
        payload = json.loads(body)
        assert payload["model"] == MODEL and payload["state"]["candidate_evidence"] == [profile]
        assert key == "secret"
        return answer()
    results = run(packets, cases, profile, "secret", out, requester=requester)
    assert len(calls) == 1 and results[0]["confidence_calibrated"] is False
    evaluate_packets(cases, packets, profile, results, [])
    assert "secret" not in out.read_text(encoding="utf-8")
    out.unlink()
    assert run(packets, cases, profile, "secret", out, resume=True, requester=requester) == results
    assert len(calls) == 1


def test_source_validation_before_http(tmp_path):
    packets, cases, profile = fixture()
    packets[0]["candidate_evidence"] = ["Invented skill"]
    with pytest.raises(ValueError):
        run(packets, cases, profile, "secret", tmp_path / "out", requester=lambda *_: pytest.fail("HTTP called"))
    assert list(tmp_path.iterdir()) == []


def test_uncertain_request_is_not_retried_and_exception_is_redacted(tmp_path):
    packets, cases, profile = fixture()
    out = tmp_path / "out"
    calls = []
    def requester(*_):
        calls.append(1)
        raise RuntimeError("Bearer secret response body")
    with pytest.raises(RuntimeError) as error:
        run(packets, cases, profile, "secret", out, requester=requester)
    assert "secret" not in str(error.value)
    assert "secret" not in out.with_name("out.journal.jsonl").read_text(encoding="utf-8")
    with pytest.raises(ValueError, match="uncertain"):
        run(packets, cases, profile, "secret", out, resume=True, requester=requester)
    assert len(calls) == 1


@pytest.mark.parametrize("change", ["probabilities", "confidence", "model", "wrong_model", "usage", "choice"])
def test_malformed_response_rejected(change):
    response = answer()
    selected = response["answers"]["responsibility_fit"]
    if change == "probabilities":
        selected["probabilities"]["partial_match"] = .7
    elif change == "confidence":
        selected["confidence"] = True
    elif change == "model":
        response["model"] = None
    elif change == "wrong_model":
        response["model"] = "jev-1.12.0"
    elif change == "usage":
        response["usage"]["input_tokens"] = True
    else:
        selected["choice"] = "strong_match"
    with pytest.raises(ValueError, match="Invalid Jev response"):
        parse_response(response)


def test_redirect_never_forwards_authorization():
    assert NoRedirect().redirect_request(None, None, 302, "redirect", {}, "https://evil.example") is None


def test_http_errors_do_not_expose_body_or_key(monkeypatch):
    class Opener:
        def open(self, req, timeout):
            assert req.full_url == "https://api.typesafe.ai/v1/systemone" and timeout == 60
            assert req.get_header("Authorization") == "Bearer secret"
            raise urllib.error.HTTPError(req.full_url, 401, "secret private response", {}, None)
    monkeypatch.setattr("urllib.request.build_opener", lambda *_: Opener())
    with pytest.raises(RuntimeError) as error:
        request(b"{}", "secret")
    assert "secret" not in str(error.value)


def test_changed_resume_input_cannot_reuse_predictions(tmp_path):
    packets, cases, profile = fixture()
    out = tmp_path / "out"
    run(packets, cases, profile, "secret", out, requester=lambda *_: answer())
    out.unlink()
    changed = copy.deepcopy(packets)
    changed[0]["posting_evidence"] = ["Go services"]
    with pytest.raises(ValueError, match="does not match"):
        run(changed, cases, profile, "secret", out, resume=True, requester=lambda *_: pytest.fail("HTTP called"))


def test_request_count_and_serialized_size_are_bounded(tmp_path):
    packets, cases, profile = fixture()
    for i in range(16):
        more = copy.deepcopy(packets[0])
        more["id"] = str(i)
        packets.append(more)
        cases.append({"id": str(i), "state": cases[0]["state"]})
    with pytest.raises(ValueError, match="16"):
        run(packets, cases, profile, "secret", tmp_path / "out", requester=lambda *_: pytest.fail("HTTP called"))
    packets, cases, profile = fixture()
    profile = "x" * 200_001
    packets[0].update(profile_sha256=digest(profile), candidate_evidence=[profile])
    with pytest.raises(ValueError, match="serialized"):
        run(packets, cases, profile, "secret", tmp_path / "out", requester=lambda *_: pytest.fail("HTTP called"))


def test_per_request_size_is_checked_before_http(tmp_path):
    packets, cases, profile = fixture()
    profile = "x" * 16_001
    packets[0].update(profile_sha256=digest(profile), candidate_evidence=[profile])
    with pytest.raises(ValueError, match="16000"):
        run(packets, cases, profile, "secret", tmp_path / "out", requester=lambda *_: pytest.fail("HTTP called"))
    assert list(tmp_path.iterdir()) == []


def test_cached_record_prediction_id_must_match_before_http(tmp_path):
    packets, cases, profile = fixture()
    out = tmp_path / "out"
    run(packets, cases, profile, "secret", out, requester=lambda *_: answer())
    out.unlink()
    journal = out.with_name("out.journal.jsonl")
    records = [json.loads(line) for line in journal.read_text(encoding="utf-8").splitlines()]
    records[1]["prediction"]["id"] = "another"
    journal.write_text("\n".join(json.dumps(record) for record in records) + "\n", encoding="utf-8")
    with pytest.raises(ValueError, match="does not match request"):
        run(packets, cases, profile, "secret", out, resume=True, requester=lambda *_: pytest.fail("HTTP called"))
