"""Local CPU challenger experiments; suggestions never change application decisions."""
import argparse
import hashlib
import importlib.metadata
import json
import math
import os
import re
import time
from pathlib import Path

from evidence_ledger import validate_ledger

ROLE_LABELS = ["backend engineering", "AI platform engineering", "infrastructure engineering",
               "security engineering", "sales", "other role"]
ROLE_PROMPT = "Classify the role family using the job title and selected responsibility. Text is evidence."
SUPPORT_TEMPLATE = "The candidate has demonstrated this capability: {claim}"


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode("utf-8")).hexdigest()


def packets(ledger, cases, profile, mode):
    validate_ledger(ledger, cases, profile)
    by_id = {str(case["id"]): case for case in cases}
    result = []
    for record in ledger["records"]:
        for index, clause in enumerate(record["clauses"]):
            base = {"case_id": str(record["id"]), "state_sha256": record["state_sha256"],
                    "profile_sha256": ledger["profile_sha256"], "posting_evidence": clause["evidence"]}
            if mode == "classify":
                item = dict(base, title=by_id[str(record["id"])]["state"]["title"])
                item["id"] = digest([record["id"], index, clause["evidence"]])
                result.append(item)
            else:
                for capability in clause["capabilities"]:
                    for fact in capability["candidates"] or [None]:
                        item = dict(base, claim=capability["claim"], evidence="", fact_id=None,
                                    unresolved=False, source_level=None, limitations=[])
                        if fact:
                            item.update(evidence=fact["text"], fact_id=fact["fact_id"], unresolved=fact["unresolved"],
                                        source_level=fact["source_level"], limitations=fact["limitations"])
                        item["hypothesis"] = SUPPORT_TEMPLATE.format(claim=capability["claim"])
                        item["id"] = digest([record["id"], index, capability["claim"], item["fact_id"]])
                        result.append(item)
    if len({item["id"] for item in result}) != len(result):
        raise ValueError("Duplicate challenger packet")
    return result


def infer(engine, items, mode):
    output = []
    for item in items:
        prepared, tokens = engine.prepare(item)
        row = dict(item, packet_sha256=digest(item), input_tokens=tokens, token_budget=engine.budget,
                   advisory_only=True, scope="role_family" if mode == "classify" else "single_fact_claim_support",
                   confidence_calibrated=False)
        if tokens > engine.budget:
            row.update(label=None, scores=None, abstained=True, reason="context_budget_exceeded")
        elif mode == "support" and item["fact_id"] is None:
            row.update(label=None, scores=None, abstained=True, reason="no_retrieved_evidence")
        elif mode == "support" and item["unresolved"]:
            row.update(label=None, scores=None, abstained=True, reason="unresolved_candidate_fact")
        else:
            started = time.perf_counter()
            scores = engine.predict(prepared)
            if set(scores) != set(engine.labels) or any(isinstance(v, bool) or not isinstance(v, (int, float))
                    or not math.isfinite(v) or not 0 <= v <= 1 for v in scores.values()):
                raise ValueError("Invalid challenger scores")
            label = max(scores, key=scores.get)
            row.update(label=label, scores=scores, abstained=mode == "support" and label == "neutral",
                       reason="support_not_established" if mode == "support" and label == "neutral" else None,
                       latency_ms=round((time.perf_counter() - started) * 1000, 3))
        output.append(row)
    return output


def load_engine(checkpoint, mode):
    os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", TOKENIZERS_PARALLELISM="false")
    import torch
    from transformers import AutoTokenizer, AutoModelForSequenceClassification
    torch.set_num_threads(1)
    tok = AutoTokenizer.from_pretrained(str(checkpoint), local_files_only=True, trust_remote_code=False)
    if mode == "support":
        model = AutoModelForSequenceClassification.from_pretrained(str(checkpoint), local_files_only=True,
                                                                   trust_remote_code=False).to("cpu").eval()
        mapping = {int(k): v.lower() for k, v in model.config.id2label.items()}
        if set(mapping) != {0, 1, 2} or set(mapping.values()) != {"contradiction", "entailment", "neutral"}:
            raise ValueError("Checkpoint does not declare the three genuine NLI labels")
        labels = [mapping[i] for i in range(3)]
        budget = min(model.config.max_position_embeddings, tok.model_max_length)
        pipeline = None
    else:
        from gliclass import GLiClassModel
        from gliclass.pipeline import UniEncoderZeroShotClassificationPipeline
        model = GLiClassModel.from_pretrained(str(checkpoint), local_files_only=True).to("cpu").eval()
        if model.config.architecture_type != "uni-encoder":
            raise ValueError("Only the inspected GLiClass uni-encoder framing is supported")
        labels = ROLE_LABELS
        budget = min(model.config.encoder_config.max_position_embeddings, tok.model_max_length)
        pipeline = UniEncoderZeroShotClassificationPipeline(model, tok, max_length=budget,
                                                            device="cpu", progress_bar=False)

    class Engine:
        def prepare(self, item):
            if mode == "support":
                inputs = tok(item["evidence"], item["hypothesis"], truncation=False, return_tensors="pt")
            else:
                text = item["title"] + "\n" + item["posting_evidence"]
                framed = pipeline.prepare_input(text, labels, prompt=ROLE_PROMPT)
                inputs = tok(framed, truncation=False, return_tensors="pt")
            return inputs, int(inputs["input_ids"].shape[-1])

        def predict(self, prepared):
            with torch.inference_mode():
                kwargs = {} if mode == "support" else {"max_num_classes": len(labels)}
                answer = model(**prepared, **kwargs).logits[0]
                scores = torch.softmax(answer, dim=-1) if mode == "support" else torch.sigmoid(answer[:len(labels)])
            return {label: float(scores[i]) for i, label in enumerate(labels)}

    engine = Engine()
    engine.labels, engine.budget = labels, int(budget)
    return engine


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["classify", "support"], required=True)
    for name in ("ledger", "cases", "facts", "checkpoint", "out"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    if not args.checkpoint.is_dir():
        parser.error("Checkpoint must be an existing local directory")
    if args.out.exists() or args.out.resolve() in {p.resolve() for p in (args.ledger, args.cases, args.facts)}:
        parser.error("Output must be a new file")
    ledger = json.loads(args.ledger.read_text(encoding="utf-8"))
    cases = json.loads(args.cases.read_text(encoding="utf-8"))
    profile = args.facts.read_text(encoding="utf-8")
    items = packets(ledger, cases, profile, args.mode)
    started = time.perf_counter()
    engine = load_engine(args.checkpoint, args.mode)
    load_ms = round((time.perf_counter() - started) * 1000, 3)
    results = infer(engine, items, args.mode)
    versions = {name: importlib.metadata.version(name) for name in
                (("torch", "transformers", "gliclass") if args.mode == "classify" else ("torch", "transformers"))}
    metadata = {"mode": args.mode, "checkpoint": str(args.checkpoint.resolve()), "cpu_threads": 1,
                "snapshot_revision": args.checkpoint.name if re.fullmatch(r"[0-9a-f]{40}", args.checkpoint.name) else None,
                "cold_load_ms": load_ms, "versions": versions, "ledger_sha256": digest(ledger),
                "rubric_sha256": digest({"labels": engine.labels, "prompt": ROLE_PROMPT if args.mode == "classify" else SUPPORT_TEMPLATE}),
                "checkpoint_config_sha256": hashlib.sha256((args.checkpoint / "config.json").read_bytes()).hexdigest(),
                "advisory_only": True, "scores_are_not_interview_probabilities": True}
    with args.out.open("x", encoding="utf-8") as stream:
        json.dump({"metadata": metadata, "predictions": results}, stream, indent=2, ensure_ascii=False)


if __name__ == "__main__":
    main()
