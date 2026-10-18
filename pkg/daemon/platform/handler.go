package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// statusError is an error that carries the HTTP status code to respond with.
type statusError struct {
	code int
	err  error
}

func (e statusError) Error() string {
	return e.err.Error()
}

func (e statusError) Unwrap() error {
	return e.err
}

func badRequest(err error) error {
	return statusError{code: http.StatusBadRequest, err: err}
}

func forbidden(err error) error {
	return statusError{code: http.StatusForbidden, err: err}
}

func missingQueryParam(name string) error {
	return badRequest(fmt.Errorf("missing required query parameter %q", name))
}

// handlerFunc is an HTTP handler that reports failures by returning them.
// A statusError supplies the response code; any other error is a 500.
// The error text is written as the response body.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
	if err == nil {
		return
	}

	code := http.StatusInternalServerError
	if se, ok := errors.AsType[statusError](err); ok {
		code = se.code
	}
	http.Error(w, err.Error(), code)
}

// writeJSON writes obj as the JSON response body. Marshal failures are
// returned before anything is written so the caller can report them.
func writeJSON(w http.ResponseWriter, obj any) error {
	out, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
	return nil
}
