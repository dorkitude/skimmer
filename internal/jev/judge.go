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
// needs its own yes/no question, and its text sits once in the request state
// (twice when a prompt also pastes it into the question with {{.Text}}).
// Sending them all at once is what Jev rejects with max_tokens_exceeded, so
// items are packed into requests that each stay under a token budget, and
// those run in parallel.
const (
	// BatchTokenBudget is the estimated-token ceiling for one request.
	// Measured failures against jev-1.13 began between 54 and 81 chunks
	// of ~2,000 characters sent twice each, so this leaves generous headroom.
	BatchTokenBudget = 24000

	// MaxItemsPerBatch caps a batch regardless of the token estimate, so
	// many tiny items still split into parallelizable requests.
	MaxItemsPerBatch = 24

	// envelopeTokens is the JSON scaffolding around one question (key,
	// type, quoting) or one state entry (id, text key, quoting).
	envelopeTokens = 12
)

// ItemKey returns the question key (and state id) of the i-th item.
func ItemKey(i int) string { return "item_" + strconv.Itoa(i) }

// questionKey is the request key of question q about item i: the item's id
// for the main question, the id and the question's name for the others.
func questionKey(i int, name string) string {
	if name == "" {
		return ItemKey(i)
	}
	return ItemKey(i) + "." + name
}

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

// Ask is a yes/no question put to every item.
type Ask struct {
	// Name keys the question's verdicts in Verdicts.Extra.
	Name string
	// Instructions renders the question for item i (its id is ItemKey(i)).
	Instructions func(i int) (string, error)
	// Criteria, when set, says what yes and no mean.
	Criteria *NoulCriteria
}

// Judgement asks one yes/no question per item, plus any Extra questions,
// all in the same requests so the items are sent once.
type Judgement struct {
	// Goal is what the caller is trying to accomplish.
	Goal string
	// Context is optional extra state, e.g. the source a chunk came from.
	Context string
	// Texts are the items to judge.
	Texts []string
	// Instructions renders the main question for item i (its id is ItemKey(i)).
	Instructions func(i int) (string, error)
	// Criteria, when set, says what yes and no mean for the main question.
	Criteria *NoulCriteria
	// Extra are more questions asked of every item, e.g. an injection check.
	Extra []Ask
}

// asks lists every question, the main one first (with an empty name).
func (j Judgement) asks() []Ask {
	return append([]Ask{{Instructions: j.Instructions, Criteria: j.Criteria}}, j.Extra...)
}

// Verdicts holds P(yes) per item, aligned with Judgement.Texts. A nil entry
// is a question Jev did not answer.
type Verdicts struct {
	P     []*float64
	Extra map[string][]*float64
	Usage Usage
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

// costModel estimates a request's tokens: fixed per request, plus per item
// the questions' own text and the item's text times the number of copies.
type costModel struct {
	fixed      int // goal and context, once per request
	perItem    int // rendered questions (minus any pasted text) and envelopes
	textCopies int // 1 for the state, plus one per question pasting the text
}

func (m costModel) item(text string) int {
	return m.textCopies*estimateTokens(text) + m.perItem
}

// newCostModel renders every question for the first item to measure the
// prompt itself. A question that pastes the item's text ({{.Text}}) costs
// that text again, for every item.
func newCostModel(j Judgement) (costModel, error) {
	m := costModel{
		fixed:      estimateTokens(j.Goal) + estimateTokens(j.Context) + 3*envelopeTokens,
		perItem:    envelopeTokens, // the item's state entry
		textCopies: 1,
	}
	text := j.Texts[0]
	for _, a := range j.asks() {
		probe, err := a.Instructions(0)
		if err != nil {
			return m, err
		}
		q := estimateTokens(probe) + envelopeTokens
		if a.Criteria != nil {
			q += estimateTokens(a.Criteria.True) + estimateTokens(a.Criteria.False) + envelopeTokens
		}
		if strings.TrimSpace(text) != "" && strings.Contains(probe, text) {
			q -= estimateTokens(text)
			m.textCopies++
		}
		m.perItem += max(q, envelopeTokens)
	}
	return m, nil
}

// planBatches packs items into batches under the token budget; a batch
// always holds ≥1 item.
func planBatches(texts []string, m costModel) []batch {
	var out []batch
	lo, used := 0, m.fixed
	for i, text := range texts {
		cost := m.item(text)
		if i > lo && (used+cost > BatchTokenBudget || i-lo >= MaxItemsPerBatch) {
			out = append(out, batch{lo, i})
			lo, used = i, m.fixed
		}
		used += cost
	}
	if lo < len(texts) {
		out = append(out, batch{lo, len(texts)})
	}
	return out
}

// Judge returns P(yes) for every item, aligned with j.Texts; entries Jev did
// not answer are nil. See JudgeAll.
func (c *Client) Judge(ctx context.Context, j Judgement) ([]*float64, Usage, error) {
	v, err := c.JudgeAll(ctx, j)
	return v.P, v.Usage, err
}

// JudgeAll asks every question of every item. Batches run in parallel
// (bounded by the client's concurrency). A batch rejected for size is halved
// and retried; any other failure costs only its own items. If some batches
// succeeded the error is a *PartialError; a plain error means every batch
// failed.
func (c *Client) JudgeAll(ctx context.Context, j Judgement) (Verdicts, error) {
	v := Verdicts{P: make([]*float64, len(j.Texts)), Extra: map[string][]*float64{}}
	for _, a := range j.Extra {
		v.Extra[a.Name] = make([]*float64, len(j.Texts))
	}
	if len(j.Texts) == 0 {
		return v, nil
	}
	m, err := newCostModel(j)
	if err != nil {
		return v, err
	}

	batches := planBatches(j.Texts, m)
	var (
		mu       sync.Mutex
		unjudged []int
		failed   int
		lastErr  error
		wg       sync.WaitGroup
	)
	for _, b := range batches {
		wg.Add(1)
		go func(b batch) {
			defer wg.Done()
			u, miss, err := c.judgeRange(ctx, j, b, &v)
			mu.Lock()
			defer mu.Unlock()
			v.Usage.Add(u)
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
		return v, nil
	case failed == len(batches):
		return v, fmt.Errorf("jev: %w", lastErr)
	}
	sort.Ints(unjudged)
	return v, &PartialError{Unjudged: unjudged, Batches: len(batches), Failed: failed, Err: lastErr}
}

// request builds the request for items[b.lo:b.hi].
func (j Judgement) request(b batch) (*SystemOneRequest, error) {
	state := judgeState{Goal: j.Goal, Context: j.Context, Items: make([]JudgeItem, 0, b.hi-b.lo)}
	asks := j.asks()
	questions := make(map[string]Question, (b.hi-b.lo)*len(asks))
	for i := b.lo; i < b.hi; i++ {
		state.Items = append(state.Items, JudgeItem{ID: ItemKey(i), Text: j.Texts[i]})
		for _, a := range asks {
			instructions, err := a.Instructions(i)
			if err != nil {
				return nil, err
			}
			questions[questionKey(i, a.Name)] = NoulQuestion(instructions, a.Criteria)
		}
	}
	return &SystemOneRequest{State: state, Questions: questions}, nil
}

// judgeRange sends items[b.lo:b.hi] as one request and writes verdicts into
// v. A request rejected for size is split in half, down to one item.
func (c *Client) judgeRange(ctx context.Context, j Judgement, b batch, v *Verdicts) (Usage, []int, error) {
	req, err := j.request(b)
	if err != nil {
		return Usage{}, indices(b), err
	}
	resp, err := c.SystemOne(ctx, req)
	if err != nil {
		// The budget is an estimate; halving is the backstop that makes its
		// accuracy a performance concern rather than a bug.
		if tooLarge(err) && b.hi-b.lo > 1 {
			mid := b.lo + (b.hi-b.lo)/2
			u1, miss1, err1 := c.judgeRange(ctx, j, batch{b.lo, mid}, v)
			u2, miss2, err2 := c.judgeRange(ctx, j, batch{mid, b.hi}, v)
			u1.Add(u2)
			if err1 == nil {
				err1 = err2
			}
			return u1, append(miss1, miss2...), err1
		}
		return Usage{}, indices(b), err
	}

	answer := func(key string) *float64 {
		if raw, ok := resp.Answers[key]; ok {
			if p, err := raw.AsNoul(); err == nil {
				return &p
			}
		}
		return nil
	}
	for i := b.lo; i < b.hi; i++ {
		v.P[i] = answer(ItemKey(i))
		for _, a := range j.Extra {
			v.Extra[a.Name][i] = answer(questionKey(i, a.Name))
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
