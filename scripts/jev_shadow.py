"""Bounded hosted Jev comparison on frozen packets; never changes jobs or resumes."""
import argparse
import json
import math
import os
import time
import urllib.error
import urllib.request
from pathlib import Path

from rank_shadow import FIT_QUESTION, digest, validate_packets, evaluate_packets

ENDPOINT = "https://api.typesafe.ai/v1/systemone"
MODEL = "jev-1.13.0"
MAX_REQUESTS = 16
MAX_BYTES = 200_000
MAX_REQUEST_BYTES = 16_000


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(body, key):
    req = urllib.request.Request(ENDPOINT, data=body, method="POST", headers={
        "Authorization": "Bearer " + key, "Content-Type": "application/json"})
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=60) as response:
            raw = response.read(1_000_001)
        if len(raw) > 1_000_000:
            raise ValueError("Oversized response")
        return json.loads(raw)
    except Exception:
        # Do not surface provider bodies, URLs, or credential-bearing exception text.
        raise RuntimeError("Jev request failed; outcome may be billed. No automatic retry.") from None


def unit(value):
    return not isinstance(value, bool) and isinstance(value, (int, float)) and math.isfinite(value) and 0 <= value <= 1


def parse_response(response):
    try:
        model = response["model"]
        answer = response["answers"]["responsibility_fit"]
        usage = response["usage"]
        probabilities = answer["probabilities"]
        labels = set(FIT_QUESTION["responsibility_fit"]["criteria"])
        valid = (model == MODEL and answer["type"] == "choice"
                 and answer["choice"] in labels and unit(answer["confidence"])
                 and isinstance(probabilities, dict) and set(probabilities) == labels
                 and all(unit(value) for value in probabilities.values())
                 and math.isclose(sum(probabilities.values()), 1, abs_tol=1e-5)
                 and probabilities[answer["choice"]] == max(probabilities.values())
                 and isinstance(usage, dict) and isinstance(usage["input_tokens"], int)
                 and not isinstance(usage["input_tokens"], bool) and usage["input_tokens"] >= 0)
        if "output_tokens" in usage:
            valid = valid and isinstance(usage["output_tokens"], int) and not isinstance(usage["output_tokens"], bool) and usage["output_tokens"] >= 0
        if not valid:
            raise ValueError()
    except (TypeError, KeyError, ValueError, AttributeError):
        raise ValueError("Invalid Jev response; no automatic retry.") from None
    return model, answer, usage


def run(packets, cases, profile, key, out, resume=False, requester=request):
    validate_packets(packets, cases, profile)
    if len(packets) > MAX_REQUESTS:
        raise ValueError("At most 16 packets per hosted run")
    if not isinstance(key, str) or not key.strip() or "\n" in key or "\r" in key:
        raise ValueError("JEV_API_KEY is missing or invalid")
    if out.exists():
        raise ValueError("Output must be a new file")
    journal = out.with_name(out.name + ".journal.jsonl")
    states = [{field: packet[field] for field in ("candidate_evidence", "posting_evidence")} for packet in packets]
    bodies = [json.dumps({"model": MODEL, "state": state, "questions": FIT_QUESTION}, ensure_ascii=False).encode("utf-8") for state in states]
    if sum(map(len, bodies)) > MAX_BYTES:
        raise ValueError("Hosted run exceeds 200000 serialized request bytes")
    if any(len(body) > MAX_REQUEST_BYTES for body in bodies):
        raise ValueError("A hosted request exceeds 16000 serialized bytes")
    run_hash = digest(json.dumps({"packets": packets, "model": MODEL, "rubric": FIT_QUESTION}, sort_keys=True))
    completed, pending = {}, set()
    if resume:
        if not journal.is_file():
            raise ValueError("Resume journal is missing")
        for line in journal.read_text(encoding="utf-8").splitlines():
            record = json.loads(line)
            if record.get("run_sha256") != run_hash or record.get("id") not in {str(p["id"]) for p in packets}:
                raise ValueError("Resume journal does not match frozen run")
            if record.get("status") == "pending" and record["id"] not in pending and record["id"] not in completed:
                pending.add(record["id"])
            elif record.get("status") == "done" and record["id"] in pending:
                prediction = record["prediction"]
                if prediction.get("id") != record["id"] or prediction.get("requested_model") != MODEL:
                    raise ValueError("Cached Jev prediction does not match request")
                parse_response({"model": prediction.get("response_model"), "usage": prediction.get("usage"),
                    "answers": {"responsibility_fit": {"type": "choice", "choice": prediction.get("label"),
                        "confidence": prediction.get("confidence"), "probabilities": prediction.get("probabilities")}}})
                completed[record["id"]] = prediction
                pending.remove(record["id"])
            else:
                raise ValueError("Invalid resume journal sequence")
        if pending:
            raise ValueError("A request outcome is uncertain; resume cannot retry it automatically")
        evaluate_packets(cases, packets, profile, list(completed.values()), [])
    elif journal.exists():
        raise ValueError("Journal exists; use --resume to reuse completed requests")
    results = []
    with journal.open("a" if resume else "x", encoding="utf-8") as stream:
        def checkpoint(record):
            stream.write(json.dumps(dict(record, run_sha256=run_hash), ensure_ascii=False) + "\n")
            stream.flush()
            os.fsync(stream.fileno())
        for packet, state, body in zip(packets, states, bodies):
            key_id = str(packet["id"])
            if key_id in completed:
                results.append(completed[key_id])
                continue
            checkpoint({"id": key_id, "status": "pending"})
            started = time.perf_counter()
            try:
                response = requester(body, key)
            except Exception:
                raise RuntimeError("Jev request failed; outcome may be billed. No automatic retry.") from None
            model, answer, usage = parse_response(response)
            label = answer["choice"]
            prediction = {"id": key_id, "state_sha256": packet["state_sha256"],
                "profile_sha256": packet["profile_sha256"], "packet_sha256": digest(json.dumps(state, sort_keys=True)),
                "rubric_sha256": digest(json.dumps(FIT_QUESTION, sort_keys=True)), "selected_evidence": state,
                "selection_version": 1, "advisory_only": True, "scope": "selected_responsibilities_only",
                "label": label, "confidence": answer["confidence"], "probabilities": answer["probabilities"],
                "confidence_calibrated": False, "abstained": label == "insufficient_evidence",
                "reason": "insufficient_evidence" if label == "insufficient_evidence" else None,
                "requested_model": MODEL, "response_model": model, "usage": usage,
                "request_bytes": len(body), "latency_ms": round((time.perf_counter() - started) * 1000, 3)}
            checkpoint({"id": key_id, "status": "done", "prediction": prediction})
            results.append(prediction)
    with out.open("x", encoding="utf-8") as stream:
        json.dump(results, stream, indent=2, ensure_ascii=False)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("packets", "cases", "profile", "out"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--resume", action="store_true")
    args = parser.parse_args()
    protected = {p.resolve() for p in (args.packets, args.cases, args.profile)}
    if args.out.resolve() in protected or args.out.with_name(args.out.name + ".journal.jsonl").resolve() in protected:
        parser.error("Cannot overwrite inputs")
    key = os.environ.get("JEV_API_KEY")
    if not key:
        env_file = Path(__file__).resolve().parent.parent / ".env"
        if env_file.is_file():
            for line in env_file.read_text(encoding="utf-8").splitlines():
                name, separator, value = line.strip().removeprefix("export ").partition("=")
                if separator and name.strip() == "JEV_API_KEY":
                    key = value.strip().strip('"').strip("'")
                    break
    run(json.loads(args.packets.read_text(encoding="utf-8")), json.loads(args.cases.read_text(encoding="utf-8")),
        args.profile.read_text(encoding="utf-8"), key, args.out, args.resume)


if __name__ == "__main__":
    main()
