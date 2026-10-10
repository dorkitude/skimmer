package jev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Batching. Every item (a document chunk, or one entry of a result list)
// needs its own yes/no question, and its text appears twice in a request:
// once in the state and once in its rendered question. Sending them all at
// once is what Jev rejects with max_tokens_exceeded, so items are packed into
// requests that each stay under a token budget, and those run in parallel.
const (
	// BatchTokenBudget is the estimated-token ceiling for one request.
	// Measured failures against jev-latest began between 54 and 81 chunks
	// of ~2,000 characters, so this leaves generous headroom.
	BatchTokenBudget = 24000

	// MaxItemsPerBatch caps a batch regardless of the token estimate, so
	// many tiny items still split into parallelizable requests.
	MaxItemsPerBatch = 24

	// envelopeTokens is the per-question JSON scaffolding around the
	// instructions (key, type, quoting) plus the item's state entry.
	envelopeTokens = 24
)

// ItemKey returns the question key (and state id) of the i-th item.
func ItemKey(i int) string { return "item_" + strconv.Itoa(i) }

// JudgeItem is one entry of the state sent to Jev.
type JudgeItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type judgeState struct {
	Goal    string      `json:"goal"`
	Context string      `json:"context,omitempty"`
	Items   []JudgeItem `json:"items"`
}

// Judgement asks one yes/no question per item.
type Judgement struct {
	// Goal is what the caller is trying to accomplish.
	Goal string
	// Context is optional extra state, e.g. the source a chunk came from.
	Context string
	// Texts are the items to judge.
	Texts []string
	// Instructions renders the question for item i (its id is ItemKey(i)).
	Instructions func(i int) (string, error)
}

// PartialError reports that some batches failed while others succeeded. The
// verdicts returned alongside it are still usable; Unjudged lists the item
// indices Jev never ruled on, which callers should keep rather than drop so a
// Jev hiccup never silently deletes content.
type PartialError struct {
	Unjudged []int
	Batches  int
	Failed   int
	Err      error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("jev: %d of %d batch(es) failed, %d item(s) unjudged: %v",
		e.Failed, e.Batches, len(e.Unjudged), e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// estimateTokens approximates the token cost of s. Bytes over runes is
// deliberate: multi-byte text over-estimates, which errs toward smaller batches.
func estimateTokens(s string) int { return (len(s) + 3) / 4 }

// tooLarge reports whether err is Jev refusing a request for being too big,
// the one failure that splitting the batch can fix.
func tooLarge(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status != http.StatusBadRequest && apiErr.Status != http.StatusRequestEntityTooLarge {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return strings.Contains(body, "max_tokens") || strings.Contains(body, "too large") || strings.Contains(body, "context length")
}

type batch struct{ lo, hi int } // items[lo:hi]

// planBatches packs items into batches under the token budget. overhead is
// the fixed cost of one rendered question; a batch always holds ≥1 item.
func planBatches(texts []string, overhead int) []batch {
	var out []batch
	lo, used := 0, 0
	for i, text := range texts {
		cost := 2*estimateTokens(text) + overhead
		if i > lo && (used+cost > BatchTokenBudget || i-lo >= MaxItemsPerBatch) {
			out = append(out, batch{lo, i})
			lo, used = i, 0
		}
		used += cost
	}
	if lo < len(texts) {
		out = append(out, batch{lo, len(texts)})
	}
	return out
}

// Judge returns P(yes) for every item, aligned with j.Texts; entries Jev did
// not answer are nil. Batches run in parallel (bounded by the client's
// concurrency). A batch rejected for size is halved and retried; any other
// failure costs only its own items. If some batches succeeded the error is a
// *PartialError; a plain error means every batch failed.
func (c *Client) Judge(ctx context.Context, j Judgement) ([]*float64, Usage, error) {
	out := make([]*float64, len(j.Texts))
	if len(j.Texts) == 0 {
		return out, Usage{}, nil
	}
	// Render the first question with its own text removed to measure the
	// prompt boilerplate, so the plan budgets for the real prompt.
	probe, err := j.Instructions(0)
	if err != nil {
		return nil, Usage{}, err
	}
	overhead := estimateTokens(probe) - estimateTokens(j.Texts[0]) + envelopeTokens
	if overhead < envelopeTokens {
		overhead = envelopeTokens
	}
	overhead += estimateTokens(j.Goal) / 4 // goal is shared state, amortized

	batches := planBatches(j.Texts, overhead)
	var (
		mu       sync.Mutex
		usage    Usage
		unjudged []int
		failed   int
		lastErr  error
		wg       sync.WaitGroup
	)
	for _, b := range batches {
		wg.Add(1)
		go func(b batch) {
			defer wg.Done()
			u, miss, err := c.judgeRange(ctx, j, b, out)
			mu.Lock()
			defer mu.Unlock()
			usage.Add(u)
			if err != nil {
				failed++
				lastErr = err
				unjudged = append(unjudged, miss...)
			}
		}(b)
	}
	wg.Wait()

	switch {
	case failed == 0:
		return out, usage, nil
	case failed == len(batches):
		return out, usage, fmt.Errorf("jev: %w", lastErr)
	}
	sort.Ints(unjudged)
	return out, usage, &PartialError{Unjudged: unjudged, Batches: len(batches), Failed: failed, Err: lastErr}
}

// judgeRange sends items[b.lo:b.hi] as one request and writes verdicts into
// out. A request rejected for size is split in half, down to one item.
func (c *Client) judgeRange(ctx context.Context, j Judgement, b batch, out []*float64) (Usage, []int, error) {
	state := judgeState{Goal: j.Goal, Context: j.Context, Items: make([]JudgeItem, 0, b.hi-b.lo)}
	questions := make(map[string]Question, b.hi-b.lo)
	for i := b.lo; i < b.hi; i++ {
		id := ItemKey(i)
		state.Items = append(state.Items, JudgeItem{ID: id, Text: j.Texts[i]})
		instructions, err := j.Instructions(i)
		if err != nil {
			return Usage{}, indices(b), err
		}
		questions[id] = NoulQuestion(instructions)
	}

	resp, err := c.SystemOne(ctx, &SystemOneRequest{State: state, Questions: questions})
	if err != nil {
		// The budget is an estimate; halving is the backstop that makes its
		// accuracy a performance concern rather than a bug.
		if tooLarge(err) && b.hi-b.lo > 1 {
			mid := b.lo + (b.hi-b.lo)/2
			u1, miss1, err1 := c.judgeRange(ctx, j, batch{b.lo, mid}, out)
			u2, miss2, err2 := c.judgeRange(ctx, j, batch{mid, b.hi}, out)
			u1.Add(u2)
			if err1 == nil {
				err1 = err2
			}
			return u1, append(miss1, miss2...), err1
		}
		return Usage{}, indices(b), err
	}

	for i := b.lo; i < b.hi; i++ {
		if raw, ok := resp.Answers[ItemKey(i)]; ok {
			if p, err := raw.AsNoul(); err == nil {
				out[i] = &p
			}
		}
	}
	return resp.Usage, nil, nil
}

func indices(b batch) []int {
	idx := make([]int, 0, b.hi-b.lo)
	for i := b.lo; i < b.hi; i++ {
		idx = append(idx, i)
	}
	return idx
}
