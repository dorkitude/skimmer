# Corpus sources and attributions

Skimmer eval corpus, assembled 2026-10-15. Documents marked ◆ below were reused from
the [dorkitude/system-one-techniques](https://github.com/dorkitude/system-one-techniques)
eval corpus, which shares those extracts. Every document is publicly available and
permitted for academic/noncommercial use. `raw/` holds the bounded extracts the
skimmer chunker consumes (`go run ./evals/cmd/chunkcorpus` regenerates the catalog
byte-for-byte). Fetch commands are documented below so the raw inputs can be re-obtained.

| Document | Source | License | Fetch |
|---|---|---|---|
| ◆ `gutenberg-alice` | Lewis Carroll, *Alice's Adventures in Wonderland* (1865), ch. I-II. Project Gutenberg ebook #11 | Public domain (US); Project Gutenberg License | `curl -L https://www.gutenberg.org/cache/epub/11/pg11.txt` |
| ◆ `gutenberg-timemachine` | H. G. Wells, *The Time Machine* (1895), ch. I-II. Project Gutenberg ebook #35 | Public domain (US); Project Gutenberg License | `curl -L https://www.gutenberg.org/cache/epub/35/pg35.txt` |
| ◆ `wiki-ada-lovelace` | Wikipedia contributors, "Ada Lovelace" | CC BY-SA 4.0 | Wikipedia API `prop=extracts&explaintext` |
| ◆ `wiki-kalman-filter` | Wikipedia contributors, "Kalman filter" | CC BY-SA 4.0 | Wikipedia API `prop=extracts&explaintext` |
| ◆ `wiki-garbage-patch` | Wikipedia contributors, "Great Pacific garbage patch" | CC BY-SA 4.0 | Wikipedia API `prop=extracts&explaintext` |
| ◆ `arxiv-2610.18579` | Yubo Li, Ramayya Krishnan, Rema Padman, "Reading Right, Acting Wrong: Diagnosing Decision Failures of System One Models in Agent Loops", arXiv:2610.18579 (2026), abstract + introduction | arXiv non-exclusive distribution license; cite the paper | `curl -L https://arxiv.org/pdf/2610.18579` |
| `arxiv-2608.16621` | Kyle Wild, "Cost Scales with Change, Not Corpus Size: Incrementally Maintaining an Evolving Semantic Substrate", arXiv:2608.16621 (2026) | arXiv non-exclusive distribution license; cite the paper | `curl -L https://arxiv.org/pdf/2608.16621` |
| `arxiv-2608.20845` | Kyle Wild, Yusuke Takahashi, Asako Uraki, "RAG Deserves an Index: Why Ingest-Time Compilation Beats Query-Time Interpretation", arXiv:2608.20845 (2026) | arXiv non-exclusive distribution license; cite the paper | `curl -L https://arxiv.org/pdf/2608.20845` |
| `arxiv-2609.29661` | Kyle Wild, Yusuke Takahashi, Asako Uraki, "Ingest-Time Fact Compilation for Cost-Efficient and Reliable Question Answering over Revised Corpora", arXiv:2609.29661 (2026) | arXiv non-exclusive distribution license; cite the paper | `curl -L https://arxiv.org/pdf/2609.29661` |
| ◆ `nist-sp-800-63b` | NIST Special Publication 800-63B, *Digital Identity Guidelines: Authentication and Lifecycle Management* (2017), front matter + sections | US government work, public domain | `curl -L https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-63b.pdf` |
| ◆ `cdc-sleep` | US Centers for Disease Control and Prevention, "About Sleep" web page | US government work, public domain | `curl -L https://www.cdc.gov/sleep/about/index.html` |
| ◆ `rfc9110` | IETF, RFC 9110, *HTTP Semantics* (June 2022), sections 1-3 | RFC 5378 unlimited distribution (IETF Trust) | `curl -L https://www.rfc-editor.org/rfc/rfc9110.txt` |

Wikipedia text: "Ada Lovelace", "Kalman filter", "Great Pacific garbage
patch" — Wikipedia contributors, CC BY-SA 4.0
(https://creativecommons.org/licenses/by-sa/4.0/), accessed 2026-10-15 via
the MediaWiki API. If this repository is ever published, this file is the
required attribution for those extracts; Wikipedia text reused here should
remain under CC BY-SA-compatible terms.

arXiv:2610.18579 is redistributed under arXiv's non-exclusive license to
distribute the article for scholarly, noncommercial use; the authors
retain copyright.

Project Gutenberg extracts are from works in the US public domain; Project
Gutenberg (https://www.gutenberg.org) is the source and its trademark and
license terms apply to its ebook editions.
