// Package platformclient is a small HTTP client for the Devsy platform gateway.
package platformclient

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
)

const (
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 1 << 20
	orgPageSize      = 100
	maxOrgPages      = 20
)

// APIError is an error response from the gateway. It never carries request
// data, so it is safe to show to the user.
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

// New returns a client for the gateway at baseURL. A nil httpClient selects one
// with a timeout that does not follow redirects, so credentials are never
// replayed to another host.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: httpClient}
}

// NormalizeURL validates a user-supplied gateway URL and returns it without a
// trailing slash.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid gateway URL %q: %w", raw, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("invalid gateway URL %q: expected http(s)://host[:port]", raw)
	}
	if parsed.User != nil {
		return "", errors.New("gateway URL must not contain credentials")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
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
	if session.Token == "" {
		return Session{}, errors.New("gateway returned no session token")
	}
	return session, nil
}

// Logout revokes a session token.
func (c *Client) Logout(ctx context.Context, token string) error {
	return c.do(ctx, request{method: http.MethodPost, path: "/v1/auth/logout", bearer: token}, nil)
}

// ListOrgs lists every org the session's user belongs to, following pages.
func (c *Client) ListOrgs(ctx context.Context, token string) ([]Org, error) {
	var orgs []Org
	cursor := ""
	for range maxOrgPages {
		page, err := c.listOrgPage(ctx, token, cursor)
		if err != nil {
			return nil, err
		}
		orgs = append(orgs, page.Orgs...)
		if page.NextCursor == "" {
			return orgs, nil
		}
		cursor = page.NextCursor
	}
	return nil, errors.New("gateway returned too many organization pages")
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading gateway response: %w", err)
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
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
	}
	return apiErr
}

func retryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
