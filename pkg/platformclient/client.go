// Package platformclient is a small HTTP client for the Devsy platform gateway.
package platformclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 1 << 20
	orgPageSize      = 100
	maxOrgPages      = 20
	maxRetryAfter    = 5 * time.Minute
	maxErrorText     = 200
)

var (
	errTokenRequired   = errors.New("session token is required")
	errResponseTooBig  = errors.New("gateway response is too large")
	errUnexpectedPages = errors.New("gateway returned too many organization pages")
)

// APIError is an error response from the gateway. It never carries request
// data. Code and Message are shortened and stripped of control characters, so
// they are safe to print to a terminal.
type APIError struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is the wait the gateway asked for, or zero.
	RetryAfter time.Duration
}

// IsUnauthorized reports whether err is a 401 from the gateway.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gateway returned status %d", e.Status)
	}
	return e.Message
}

// Client talks to one gateway.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for the gateway at baseURL, which callers should obtain
// from NormalizeURL. A nil httpClient selects one with a timeout. The client
// never follows redirects, so credentials are not replayed to another host. A
// supplied httpClient is copied, and the caller's copy is left unchanged. Pass a
// context with a deadline when the supplied client has no timeout.
func New(baseURL string, httpClient *http.Client) *Client {
	var clone http.Client
	if httpClient == nil {
		clone.Timeout = defaultTimeout
	} else {
		clone = *httpClient
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &clone}
}

// NormalizeURL validates a user-supplied gateway URL and returns it without a
// trailing slash. Plain http is accepted only for loopback hosts, because the
// login sends a password. Errors never repeat the input, which may hold a
// pasted password.
func NormalizeURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", errors.New("invalid gateway URL: expected https://host[:port]")
	}
	if parsed.User != nil {
		return "", errors.New("gateway URL must not contain credentials")
	}
	if err := checkScheme(parsed); err != nil {
		return "", err
	}
	parsed.RawQuery, parsed.ForceQuery = "", false
	parsed.Fragment, parsed.RawFragment = "", ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func checkScheme(parsed *url.URL) error {
	switch {
	case parsed.Scheme == "https":
		return nil
	case parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()):
		return nil
	case parsed.Scheme == "http":
		return errors.New("gateway URL must use https unless the host is loopback")
	default:
		return errors.New("invalid gateway URL: expected https://host[:port]")
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Session is the result of a successful password login.
type Session struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	UserID    string    `json:"user_id"`
}

// Org is an organization the user belongs to.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Login exchanges an email and password for a session token.
func (c *Client) Login(ctx context.Context, email, password string) (Session, error) {
	var session Session
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/v1/auth/login",
		body:   map[string]string{"email": email, "password": password},
	}, &session)
	if err != nil {
		return Session{}, err
	}
	if session.Token == "" || session.ExpiresAt.IsZero() {
		return Session{}, errors.New("gateway returned an incomplete session")
	}
	return session, nil
}

// Logout revokes a session token.
func (c *Client) Logout(ctx context.Context, token string) error {
	if token == "" {
		return errTokenRequired
	}
	return c.do(ctx, request{method: http.MethodPost, path: "/v1/auth/logout", bearer: token}, nil)
}

// ListOrgs lists every org the session's user belongs to, following pages.
func (c *Client) ListOrgs(ctx context.Context, token string) ([]Org, error) {
	if token == "" {
		return nil, errTokenRequired
	}
	var orgs []Org
	cursor := ""
	for range maxOrgPages {
		page, err := c.listOrgPage(ctx, token, cursor)
		if err != nil {
			return nil, err
		}
		if len(page.Orgs) > orgPageSize || page.NextCursor == cursor && cursor != "" {
			return nil, errUnexpectedPages
		}
		orgs = append(orgs, page.Orgs...)
		if page.NextCursor == "" {
			return orgs, nil
		}
		cursor = page.NextCursor
	}
	return nil, errUnexpectedPages
}

type orgPage struct {
	Orgs       []Org  `json:"orgs"`
	NextCursor string `json:"next_cursor"`
}

func (c *Client) listOrgPage(ctx context.Context, token, cursor string) (orgPage, error) {
	query := url.Values{"limit": {strconv.Itoa(orgPageSize)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page orgPage
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/v1/orgs?" + query.Encode(),
		bearer: token,
	}, &page)
	return page, err
}

type request struct {
	method string
	path   string
	bearer string
	body   any
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// url.Error embeds only the method and URL, never the body or headers.
		return fmt.Errorf("calling gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading gateway response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return errResponseTooBig
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := parseAPIError(resp.StatusCode, data)
		apiErr.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		return apiErr
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding gateway response: %w", err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, r request) (*http.Request, error) {
	var reader io.Reader
	if r.body != nil {
		encoded, err := json.Marshal(r.body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.BaseURL+r.path, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	return req, nil
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func parseAPIError(status int, data []byte) *APIError {
	apiErr := &APIError{Status: status}
	var envelope struct {
		Error errorBody `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil {
		apiErr.Code = printable(envelope.Error.Code)
		apiErr.Message = printable(envelope.Error.Message)
	}
	return apiErr
}

func retryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 {
		return 0
	}
	if seconds > int(maxRetryAfter/time.Second) {
		return maxRetryAfter
	}
	return time.Duration(seconds) * time.Second
}

// printable replaces control characters, collapses whitespace and shortens
// text from the gateway.
func printable(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxErrorText {
		return string(runes[:maxErrorText])
	}
	return text
}
