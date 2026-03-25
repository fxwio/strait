package response

import (
	"encoding/json"
	"net/http"
)

const (
	ErrorTypeAuthentication = "authentication_error"
	ErrorTypePermission     = "permission_error"
	ErrorTypeInvalidRequest = "invalid_request_error"
	ErrorTypeRateLimit      = "rate_limit_error"
	ErrorTypeServer         = "server_error"
)

type OpenAIErrorEnvelope struct {
	Error OpenAIError `json:"error"`
}

type OpenAIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func Ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func WriteOpenAIError(
	w http.ResponseWriter,
	status int,
	message string,
	errType string,
	param *string,
	code *string,
) {
	if errType == "" {
		errType = defaultErrorType(status)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(OpenAIErrorEnvelope{
		Error: OpenAIError{
			Message: message,
			Type:    errType,
			Param:   param,
			Code:    code,
		},
	})
}

func WriteAuthenticationError(w http.ResponseWriter, status int, message string, code string) {
	WriteOpenAIError(w, status, message, ErrorTypeAuthentication, nil, Ptr(code))
}

func WritePermissionError(w http.ResponseWriter, status int, message string, param string, code string) {
	WriteOpenAIError(w, status, message, ErrorTypePermission, Ptr(param), Ptr(code))
}

func WriteRateLimitError(w http.ResponseWriter, message string, code string) {
	WriteOpenAIError(w, http.StatusTooManyRequests, message, ErrorTypeRateLimit, nil, Ptr(code))
}

func WriteServerError(w http.ResponseWriter, status int, message string, code string) {
	WriteOpenAIError(w, status, message, ErrorTypeServer, nil, Ptr(code))
}

func WriteInternalServerError(w http.ResponseWriter, message string, code string) {
	WriteServerError(w, http.StatusInternalServerError, message, code)
}

func WriteServiceUnavailable(w http.ResponseWriter, message string, code string) {
	WriteServerError(w, http.StatusServiceUnavailable, message, code)
}

func WriteGatewayTimeout(w http.ResponseWriter, message string, code string) {
	WriteServerError(w, http.StatusGatewayTimeout, message, code)
}

func defaultErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return ErrorTypeAuthentication
	case http.StatusForbidden:
		return ErrorTypePermission
	case http.StatusTooManyRequests:
		return ErrorTypeRateLimit
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return ErrorTypeInvalidRequest
	default:
		return ErrorTypeServer
	}
}
