// Package httphelper HTTP Error Response Helpers
package httphelper

import (
	"fmt"
	"net/http"

	"github.com/skycoin/skycoin/src/util/logging"
)

var (
	logger = logging.MustGetLogger("gui")
)

// HTTPError wraps http.Error
func HTTPError(w http.ResponseWriter, status int, httpMsg string) {
	msg := fmt.Sprintf("%d %s", status, httpMsg)
	logger.Errorf(msg)
	http.Error(w, msg, status)
}

// Error400 respond with a 400 error
func Error400(w http.ResponseWriter, msg string) {
	httpMsg := "Bad Request"
	if msg != "" {
		httpMsg = fmt.Sprintf("%s - %s", httpMsg, msg)
	}
	HTTPError(w, http.StatusBadRequest, httpMsg)
}

// Error401 response with a 401 error
func Error401(w http.ResponseWriter, auth, msg string) {
	w.Header().Set("WWW-Authenticate", auth)
	httpMsg := http.StatusText(http.StatusUnauthorized)
	if msg != "" {
		httpMsg = fmt.Sprintf("%s - %s", httpMsg, msg)
	}
	HTTPError(w, http.StatusUnauthorized, httpMsg)
}

// Error403 respond with a 403 error
func Error403(w http.ResponseWriter) {
	HTTPError(w, http.StatusForbidden, "Forbidden")
}

// Error403Msg respond with a 403 error and include a message
func Error403Msg(w http.ResponseWriter, msg string) {
	httpMsg := "Forbidden"
	if msg != "" {
		httpMsg = fmt.Sprintf("%s - %s", httpMsg, msg)
	}
	HTTPError(w, http.StatusForbidden, httpMsg)
}

// Error404 respond with a 404 error
func Error404(w http.ResponseWriter) {
	HTTPError(w, http.StatusNotFound, "Not Found")
}

// Error405 respond with a 405 error
func Error405(w http.ResponseWriter) {
	HTTPError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
}

// Error501 respond with a 501 error
func Error501(w http.ResponseWriter) {
	HTTPError(w, http.StatusNotImplemented, "Not Implemented")
}

// Error500 respond with a 500 error
func Error500(w http.ResponseWriter) {
	HTTPError(w, http.StatusInternalServerError, "Internal Server Error")
}

// Error500Msg respond with a 500 error and include a message
func Error500Msg(w http.ResponseWriter, msg string) {
	httpMsg := "Internal Server Error"
	if msg != "" {
		httpMsg = fmt.Sprintf("%s - %s", httpMsg, msg)
	}
	HTTPError(w, http.StatusInternalServerError, httpMsg)
}
