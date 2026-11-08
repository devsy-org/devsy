package platformclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Workspace is one workspace as the gateway reports it. Phase is read from the
// cluster on every request, so it is a snapshot.
type Workspace struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

// AuthError is a 401 on a call made with a stored API key. The key is missing,
// revoked, or unknown, and signing in again issues a new one.
type AuthError struct {
	Cause *APIError
}

func (e *AuthError) Error() string {
	return "authentication failed: the gateway did not accept the stored API key; " +
		"run 'devsy login' to sign in again"
}

func (e *AuthError) Unwrap() error { return e.Cause }

// PermissionError is a 403 on a call made with a stored API key. The gateway
// only answers 403 on these routes when the key lacks the scope the route
// needs, and its own message ("insufficient scope") does not say which one.
type PermissionError struct {
	// Scope is the scope the call needed: "read" or "write".
	Scope string
	Cause *APIError
}

func (e *PermissionError) Error() string {
	msg := "permission denied: the stored API key lacks " + e.Scope + " scope"
	if e.Cause != nil && e.Cause.Message != "" {
		msg += " (gateway: " + e.Cause.Message + ")"
	}
	return msg
}

func (e *PermissionError) Unwrap() error { return e.Cause }

var errNoKey = errors.New("not logged in: run 'devsy login' to sign in")

// ListWorkspaces lists the workspaces of the key's org.
func (c *Client) ListWorkspaces(ctx context.Context, apiKey string) ([]Workspace, error) {
	var out []Workspace
	err := c.doWithKey(ctx, request{
		method: http.MethodGet,
		path:   "/v1/workspaces",
		bearer: apiKey,
	}, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateWorkspace asks the gateway to create a workspace from a template. The
// gateway answers 202 once the request is recorded; the workspace is not ready
// yet, and callers that need it ready poll ListWorkspaces.
func (c *Client) CreateWorkspace(
	ctx context.Context,
	apiKey, name, template string,
) (Workspace, error) {
	var out Workspace
	err := c.doWithKey(ctx, request{
		method: http.MethodPost,
		path:   "/v1/workspaces",
		bearer: apiKey,
		body:   map[string]string{"name": name, "template": template},
	}, &out)
	if err != nil {
		return Workspace{}, err
	}
	return out, nil
}

// SleepWorkspace asks the gateway to put a workspace to sleep. The gateway
// answers 202 once the request is recorded; the phase changes later.
func (c *Client) SleepWorkspace(ctx context.Context, apiKey, name string) (Workspace, error) {
	return c.lifecycle(ctx, request{
		method: http.MethodPost,
		path:   workspacePath(name) + "/sleep",
		bearer: apiKey,
	})
}

// WakeWorkspace asks the gateway to wake a sleeping workspace. The gateway
// answers 202 once the request is recorded; the phase changes later.
func (c *Client) WakeWorkspace(ctx context.Context, apiKey, name string) (Workspace, error) {
	return c.lifecycle(ctx, request{
		method: http.MethodPost,
		path:   workspacePath(name) + "/wake",
		bearer: apiKey,
	})
}

// DeleteWorkspace asks the gateway to delete a workspace. The gateway answers
// 202 once the request is recorded; the workspace disappears later.
func (c *Client) DeleteWorkspace(ctx context.Context, apiKey, name string) (Workspace, error) {
	return c.lifecycle(ctx, request{
		method: http.MethodDelete,
		path:   workspacePath(name),
		bearer: apiKey,
	})
}

func workspacePath(name string) string {
	return "/v1/workspaces/" + url.PathEscape(name)
}

// lifecycle sends a request that the gateway only accepts: anything but a 202
// is an error.
func (c *Client) lifecycle(ctx context.Context, r request) (Workspace, error) {
	r.wantStatus = http.StatusAccepted
	var out Workspace
	if err := c.doWithKey(ctx, r, &out); err != nil {
		return Workspace{}, err
	}
	return out, nil
}

// doWithKey is do for calls authenticated by an API key. It names a rejected
// key, which plain do cannot: login uses 401 for a wrong password.
func (c *Client) doWithKey(ctx context.Context, r request, out any) error {
	if r.bearer == "" {
		return errNoKey
	}
	err := c.do(ctx, r, out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return &AuthError{Cause: apiErr}
	case http.StatusForbidden:
		return &PermissionError{Scope: scopeFor(r.method), Cause: apiErr}
	default:
		return err
	}
}

// scopeFor returns the scope the gateway requires for an HTTP method: reads
// need read, everything that changes a workspace needs write.
func scopeFor(method string) string {
	if method == http.MethodGet || method == http.MethodHead {
		return "read"
	}
	return "write"
}
