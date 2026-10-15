#!/usr/bin/env python3
"""Deterministic passage slicer for the Skimmer eval corpus.

Reads raw/*.txt (bounded public-document extracts), unwraps hard line wraps,
drops short fragments, and keeps up to PASSAGES_PER_DOC evenly spaced
paragraph or sentence-group passages between LO and HI bytes. Output:
passages.json with one entry per document. Same input, same output, always.
"""
import json, os, re, sys

PASSAGES_PER_DOC, LO, HI, GROUP = 13, 120, 700, 450

def unwrap(p):
    buf = ""
    for l in (x.strip() for x in p.split("\n")):
        if not l:
            if buf: yield buf; buf = ""
            continue
        buf = (buf + " " + l).strip() if buf else l
    if buf: yield buf

def sentences(p):
    return [s for s in re.split(r"(?<=[.!?])\s+", p.strip()) if s]

def slice_doc(text):
    paras, kept = list(unwrap(text)), []
    for p in paras:
        if re.match(r"^[^A-Za-z0-9]", p):
            continue
        if LO <= len(p) <= HI:
            kept.append(p)
        elif len(p) > HI:
            buf = ""
            for s in sentences(p):
                if buf and len(buf) + 1 + len(s) > GROUP:
                    if len(buf) >= LO: kept.append(buf)
                    buf = s
                else:
                    buf = (buf + " " + s).strip()
            if len(buf) >= LO: kept.append(buf)
    if len(kept) <= PASSAGES_PER_DOC:
        return kept
    step = len(kept) / PASSAGES_PER_DOC
    return [kept[int(i * step)] for i in range(PASSAGES_PER_DOC)]

def main():
    base = os.path.dirname(os.path.abspath(__file__))
    out = {}
    for key in sorted(os.listdir(os.path.join(base, "raw"))):
        if not key.endswith(".txt"): continue
        doc = key[:-4]
        text = open(os.path.join(base, "raw", key), encoding="utf-8").read()
        out[doc] = {"passages": [
            {"id": f"{doc}-{i:02d}", "text": t} for i, t in enumerate(slice_doc(text))
        ]}
    with open(os.path.join(base, "passages.json"), "w", encoding="utf-8") as f:
        json.dump(out, f, indent=1, ensure_ascii=False)
        f.write("\n")
    total = sum(len(v["passages"]) for v in out.values())
    print(f"{len(out)} documents, {total} passages")
    if total == 0: sys.exit(1)

if __name__ == "__main__":
    main()
