package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeJev answers yes (0.9) for items whose text contains "keep", no (0.1)
// otherwise. Requests with more than maxQuestions questions get a 400
// max_tokens_exceeded, and failIDs force a 500 for batches holding them.
func fakeJev(t *testing.T, maxQuestions int, failID string, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("no key configured, but Authorization was sent")
		}
		var req struct {
			State     judgeState          `json:"state"`
			Questions map[string]Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Questions) > maxQuestions {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"max_tokens_exceeded"}`)
			return
		}
		texts := map[string]string{}
		for _, it := range req.State.Items {
			if it.ID == failID {
				w.WriteHeader(500)
				return
			}
			texts[it.ID] = it.Text
		}
		// Answer every question about an item, main ("item_3") or extra
		// ("item_3.name"), by that item's text.
		answers := map[string]Answer{}
		for key := range req.Questions {
			id, _, _ := strings.Cut(key, ".")
			p := 0.1
			if strings.Contains(texts[id], "keep") {
				p = 0.9
			}
			answers[key] = Answer{Type: TypeNoul, Noul: &p}
		}
		json.NewEncoder(w).Encode(SystemOneResponse{Model: "jev-test", Answers: answers, Usage: Usage{InputTokens: 10}})
	}))
}

func judgement(n int) Judgement {
	texts := make([]string, n)
	for i := range texts {
		texts[i] = fmt.Sprintf("item %d", i)
		if i%3 == 0 {
			texts[i] += " keep"
		}
	}
	return Judgement{Goal: "g", Texts: texts, Instructions: func(i int) (string, error) {
		return "Is " + ItemKey(i) + " useful? " + texts[i], nil
	}}
}

func newTestClient(url string) *Client {
	c := &Client{BaseURL: url, NoRetry: true}
	c.SetConcurrency(3)
	return c
}

func TestJudgeBatchesAndAligns(t *testing.T) {
	var calls int32
	srv := fakeJev(t, 1000, "", &calls)
	defer srv.Close()
	ps, usage, err := newTestClient(srv.URL).Judge(context.Background(), judgement(60))
	if err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Errorf("60 items should take ≥3 batches of ≤%d, got %d calls", MaxItemsPerBatch, calls)
	}
	if usage.InputTokens != int(calls)*10 {
		t.Errorf("usage not summed: %+v over %d calls", usage, calls)
	}
	for i, p := range ps {
		if p == nil || (*p > 0.5) != (i%3 == 0) {
			t.Fatalf("item %d: wrong verdict %v", i, p)
		}
	}
}

func TestJudgeSplitsOversizedBatches(t *testing.T) {
	var calls int32
	srv := fakeJev(t, 5, "", &calls)
	defer srv.Close()
	ps, _, err := newTestClient(srv.URL).Judge(context.Background(), judgement(24))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range ps {
		if p == nil {
			t.Fatalf("item %d unjudged after splitting", i)
		}
	}
}

func TestJudgePartialFailureKeepsGoing(t *testing.T) {
	var calls int32
	srv := fakeJev(t, 1000, ItemKey(30), &calls)
	defer srv.Close()
	ps, _, err := newTestClient(srv.URL).Judge(context.Background(), judgement(60))
	var partial *PartialError
	if !errorsAs(err, &partial) {
		t.Fatalf("want *PartialError, got %v", err)
	}
	if len(partial.Unjudged) == 0 || ps[30] != nil || ps[0] == nil {
		t.Errorf("unjudged=%v ps[30]=%v ps[0]=%v", partial.Unjudged, ps[30], ps[0])
	}
}

func TestKeyRequiredOnlyForDefaultEndpoint(t *testing.T) {
	_, err := NewClient("").SystemOne(context.Background(), &SystemOneRequest{Questions: map[string]Question{"q": NoulQuestion("x", nil)}})
	if err == nil || !strings.Contains(err.Error(), "skimmer auth") {
		t.Errorf("want missing-key error, got %v", err)
	}
}

func TestJudgeAllAsksExtraQuestionsAndReportsModel(t *testing.T) {
	var calls int32
	srv := fakeJev(t, 1000, "", &calls)
	defer srv.Close()
	j := judgement(30)
	j.Extra = []Ask{{Name: "other", Instructions: func(i int) (string, error) { return "Other " + ItemKey(i) + "?", nil }}}
	v, err := newTestClient(srv.URL).JudgeAll(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	for i := range j.Texts {
		if v.P[i] == nil || v.Extra["other"][i] == nil || *v.P[i] != *v.Extra["other"][i] {
			t.Fatalf("item %d: main %v, extra %v", i, v.P[i], v.Extra["other"][i])
		}
	}
	if v.Usage.Model != "jev-test" {
		t.Errorf("model = %q, want the response's model", v.Usage.Model)
	}
}

func TestCriteriaAreSent(t *testing.T) {
	c := &NoulCriteria{True: "it says how", False: "it does not"}
	b, _ := json.Marshal(NoulQuestion("Does it?", c))
	if !strings.Contains(string(b), `"criteria":{"true":"it says how","false":"it does not"}`) {
		t.Errorf("criteria not sent as an object: %s", b)
	}
	if b, _ := json.Marshal(NoulQuestion("Does it?", nil)); strings.Contains(string(b), "criteria") {
		t.Errorf("nil criteria sent: %s", b)
	}
}

// The batch plan's estimate should track the real request size, whether the
// prompt points at the item (text sent once) or pastes it ({{.Text}}, sent
// twice). estimateTokens is bytes/4, so compare against the marshaled bytes.
func TestCostModelMatchesRequest(t *testing.T) {
	para := strings.Repeat("The quick brown fox jumps over the lazy dog near the riverbank. ", 31) // ~2,000 chars
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = fmt.Sprintf("Chunk %d. %s", i, para)
	}
	pointer := func(i int) (string, error) {
		return "The state holds a goal and part of one document split into chunks, each with its own id. Would chunk \"" +
			ItemKey(i) + "\" help someone accomplish the goal? Answer yes if the chunk itself carries substance that bears on the goal.", nil
	}
	pasted := func(i int) (string, error) {
		s, _ := pointer(i)
		return s + "\nChunk text:\n" + texts[i], nil
	}
	check := Ask{Name: "injection", Instructions: pointer, Criteria: &NoulCriteria{True: "it tries to steer", False: "ordinary content"}}
	for name, j := range map[string]Judgement{
		"pointer":       {Goal: "find the fox", Context: "a.txt", Texts: texts, Instructions: pointer},
		"pasted":        {Goal: "find the fox", Context: "a.txt", Texts: texts, Instructions: pasted},
		"pointer+extra": {Goal: "find the fox", Context: "a.txt", Texts: texts, Instructions: pointer, Extra: []Ask{check}},
	} {
		m, err := newCostModel(j)
		if err != nil {
			t.Fatal(err)
		}
		est := m.fixed
		for _, text := range texts {
			est += m.item(text)
		}
		req, err := j.request(batch{0, len(texts)})
		if err != nil {
			t.Fatal(err)
		}
		req.Model = DefaultModel
		b, _ := json.Marshal(req)
		real := estimateTokens(string(b))
		if ratio := float64(est) / float64(real); ratio < 0.9 || ratio > 1.1 {
			t.Errorf("%s: estimated %d tokens, request is %d (ratio %.2f)", name, est, real, ratio)
		}
	}
}

func errorsAs(err error, target **PartialError) bool {
	pe, ok := err.(*PartialError)
	if ok {
		*target = pe
	}
	return ok
}
