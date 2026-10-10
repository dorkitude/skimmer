package fetch

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Reddit fronts www.reddit.com with a JavaScript challenge: instead of the
// page, it returns HTTP 200 with a hidden form and a script that fills in
// solution = token+token and resubmits the form as a GET. The response to
// that GET is the real page, and it sets session cookies that skip the
// challenge on later requests. A cookie jar on the client keeps them.

var (
	redditChallengeForm   = regexp.MustCompile(`(?s)<form[^>]*\baction="([^"]*)"[^>]*>(.*?)</form>`)
	redditChallengeInput  = regexp.MustCompile(`<input[^>]*\bname="([^"]+)"(?:[^>]*\bvalue="([^"]*)")?`)
	redditChallengeSolver = regexp.MustCompile(`\(async e=>e\+e\)\("([^"]+)"\)`)
)

// errRedditChallenge is returned when a page is a Reddit challenge we cannot
// solve, e.g. because the script changed shape.
var errRedditChallenge = errors.New("unrecognized Reddit challenge page")

// isRedditChallenge reports whether body is the challenge interstitial.
func isRedditChallenge(body []byte) bool {
	return strings.Contains(string(body), `name="js_challenge"`)
}

// solveRedditChallenge returns the URL whose GET yields the real page:
// the form's action with the form's hidden fields, the computed solution,
// and the original request's query parameters.
func solveRedditChallenge(pageURL *url.URL, body []byte) (string, error) {
	html := string(body)
	form := redditChallengeForm.FindStringSubmatch(html)
	solver := redditChallengeSolver.FindStringSubmatch(html)
	if form == nil || solver == nil {
		return "", errRedditChallenge
	}
	action, err := pageURL.Parse(form[1])
	if err != nil {
		return "", fmt.Errorf("%w: bad form action: %v", errRedditChallenge, err)
	}
	q := url.Values{}
	for _, in := range redditChallengeInput.FindAllStringSubmatch(form[2], -1) {
		q.Set(in[1], in[2])
	}
	if !q.Has("solution") {
		return "", errRedditChallenge
	}
	q.Set("solution", solver[1]+solver[1])
	for k, vs := range pageURL.Query() {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	action.RawQuery = q.Encode()
	return action.String(), nil
}
