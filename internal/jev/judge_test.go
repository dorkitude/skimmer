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
		answers := map[string]Answer{}
		for _, it := range req.State.Items {
			if it.ID == failID {
				w.WriteHeader(500)
				return
			}
			p := 0.1
			if strings.Contains(it.Text, "keep") {
				p = 0.9
			}
			answers[it.ID] = Answer{Type: TypeNoul, Noul: &p}
		}
		json.NewEncoder(w).Encode(SystemOneResponse{Answers: answers, Usage: Usage{InputTokens: 10}})
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
	_, err := NewClient("").SystemOne(context.Background(), &SystemOneRequest{Questions: map[string]Question{"q": NoulQuestion("x")}})
	if err == nil || !strings.Contains(err.Error(), "skimmer auth") {
		t.Errorf("want missing-key error, got %v", err)
	}
}

func errorsAs(err error, target **PartialError) bool {
	pe, ok := err.(*PartialError)
	if ok {
		*target = pe
	}
	return ok
}
