package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const braveEndpoint = "https://api.search.brave.com/res/v1/web/search"

type searchArguments struct {
	Query     string `json:"query"`
	Count     *int   `json:"count,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Freshness string `json:"freshness,omitempty"`
}

type searchResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
	Age           string   `json:"age,omitempty"`
	PageAge       string   `json:"page_age,omitempty"`
	Language      string   `json:"language,omitempty"`
}

type searchOutput struct {
	Query                string         `json:"query"`
	AlteredQuery         string         `json:"altered_query,omitempty"`
	MoreResultsAvailable bool           `json:"more_results_available"`
	Results              []searchResult `json:"results"`
}

type searchClient struct {
	config   config
	http     *http.Client
	endpoint string // fixed in production; tests use a local HTTP server
}

func newSearchClient(cfg config) *searchClient {
	return &searchClient{config: cfg, endpoint: braveEndpoint, http: &http.Client{
		Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
		// Never forward the subscription token to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func decodeArguments(raw json.RawMessage) (searchArguments, error) {
	var args searchArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return args, fmt.Errorf("invalid search arguments: %w", err)
	}
	if args.Query = strings.TrimSpace(args.Query); args.Query == "" || utf8.RuneCountInString(args.Query) > 600 || len(strings.Fields(args.Query)) > 75 {
		return args, errors.New("query must contain 1–600 characters and at most 75 words")
	}
	if args.Count == nil {
		count := 5
		args.Count = &count
	}
	if *args.Count < 1 || *args.Count > 20 {
		return args, errors.New("count must be between 1 and 20")
	}
	if args.Offset < 0 || args.Offset > 9 {
		return args, errors.New("offset must be a page number between 0 and 9")
	}
	if !validFreshness(args.Freshness) {
		return args, errors.New("freshness must be pd, pw, pm, py, or YYYY-MM-DDtoYYYY-MM-DD with start <= end")
	}
	return args, nil
}

func validFreshness(value string) bool {
	switch value {
	case "", "pd", "pw", "pm", "py":
		return true
	}
	start, end, ok := strings.Cut(value, "to")
	if !ok {
		return false
	}
	startDate, err := time.Parse("2006-01-02", start)
	endDate, endErr := time.Parse("2006-01-02", end)
	return err == nil && endErr == nil && !startDate.After(endDate)
}

func (c *searchClient) search(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	args, err := decodeArguments(raw)
	if err != nil {
		return nil, err
	}
	if c.config.APIKey == "" {
		return nil, errors.New("Brave API key is missing. Set api_key in the Brave plugin's config.toml or BRAVE_SEARCH_API_KEY, then restart Zoe")
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, errors.New("invalid Brave search endpoint")
	}
	query := endpoint.Query()
	query.Set("q", args.Query)
	query.Set("count", strconv.Itoa(*args.Count))
	query.Set("offset", strconv.Itoa(args.Offset))
	query.Set("result_filter", "web")
	query.Set("extra_snippets", "true")
	query.Set("text_decorations", "false")
	query.Set("safesearch", c.config.SafeSearch)
	if args.Freshness != "" {
		query.Set("freshness", args.Freshness)
	}
	if c.config.Country != "" {
		query.Set("country", c.config.Country)
	}
	if c.config.SearchLanguage != "" {
		query.Set("search_lang", c.config.SearchLanguage)
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("could not create Brave search request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.config.APIKey)
	response, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("Brave search timed out or was cancelled: %w", ctxError(err))
		}
		return nil, errors.New("Brave search request failed: check network connectivity")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case 401, 403:
			return nil, fmt.Errorf("Brave returned HTTP %d: check your API key and Search API subscription", response.StatusCode)
		case 429:
			return nil, errors.New("Brave returned HTTP 429: search rate limit or quota exceeded; retry later")
		default:
			return nil, fmt.Errorf("Brave returned HTTP %d", response.StatusCode)
		}
	}
	const maxBody = 8 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return nil, errors.New("could not read Brave search response")
	}
	if len(body) > maxBody {
		return nil, errors.New("Brave search response exceeded 8 MiB")
	}
	var data struct {
		Type  string `json:"type"`
		Query struct {
			Original             string `json:"original"`
			Altered              string `json:"altered"`
			MoreResultsAvailable bool   `json:"more_results_available"`
		} `json:"query"`
		Web struct {
			Results []searchResult `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &data); err != nil || data.Type != "search" {
		return nil, errors.New("Brave returned an invalid search response")
	}
	results := data.Web.Results
	if len(results) > *args.Count {
		results = results[:*args.Count]
	}
	if results == nil {
		results = []searchResult{}
	}
	output := searchOutput{Query: args.Query, AlteredQuery: data.Query.Altered, MoreResultsAvailable: data.Query.MoreResultsAvailable, Results: results}
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, errors.New("could not encode Brave search results")
	}
	// Leave ample space for the JSON-RPC envelope under Zoe's 1 MiB line limit.
	if len(encoded) > 512*1024 {
		return nil, errors.New("search results are too large; request a smaller count")
	}
	return encoded, nil
}

func ctxError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return context.DeadlineExceeded
}
