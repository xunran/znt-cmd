package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"znt/internal/app/auth"
	"znt/internal/app/config"
	"znt/internal/contracts"
)

type maxBodyBytesContextKey struct{}

func decodeMapPayload(w http.ResponseWriter, r *http.Request, message string) (map[string]any, bool) {
	payload := map[string]any{}
	if !decodeJSONPayload(w, r, &payload, message) {
		return nil, false
	}
	return payload, true
}

type authedHandler func(http.ResponseWriter, *http.Request, auth.CallerIdentity)

func requireAuth(authenticator auth.Authenticator, next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := authenticator.Authenticate(r)
		if !ok {
			writeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "unauthorized", nil), http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(auth.WithCaller(r.Context(), caller)), caller)
	}
}

func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err *contracts.RuntimeError, status int) {
	writeJSON(w, err.ToAPIResponse(), status)
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) {
		writeError(w, runtimeErr, statusForRuntimeError(runtimeErr, http.StatusBadRequest))
		return
	}
	writeError(w, contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil), http.StatusInternalServerError)
}

func statusForRuntimeError(err *contracts.RuntimeError, fallback int) int {
	if err != nil && err.Code == contracts.CodeAdmissionRejected {
		return http.StatusTooManyRequests
	}
	return fallback
}

func withMaxBodyBytes(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), maxBodyBytesContextKey{}, maxBytes)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func maxBodyBytesFromRequest(r *http.Request) int64 {
	if maxBytes, ok := r.Context().Value(maxBodyBytesContextKey{}).(int64); ok {
		return maxBytes
	}
	return config.DefaultHTTPMaxBodyBytes
}

func decodeJSONPayload(w http.ResponseWriter, r *http.Request, dst any, message string) bool {
	body := r.Body
	if maxBytes := maxBodyBytesFromRequest(r); maxBytes > 0 {
		body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		status := http.StatusBadRequest
		errMessage := message
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
			errMessage = "request body too large"
		}
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, errMessage, map[string]any{"error": err.Error()}), status)
		return false
	}
	return true
}
