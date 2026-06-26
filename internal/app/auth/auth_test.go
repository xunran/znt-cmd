package auth

import (
	"net/http/httptest"
	"testing"
)

func TestAuthenticatorReadsCallerDisplayName(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/commands", nil)
	req.Header.Set("X-Tenant-ID", "tenant_1")
	req.Header.Set("X-Caller-ID", "user_1")
	req.Header.Set("X-Caller-Type", "user")
	req.Header.Set("X-Caller-Display-Name", "lunlun")

	caller, ok := New("").Authenticate(req)
	if !ok {
		t.Fatal("expected request to authenticate")
	}
	if caller.DisplayName != "lunlun" {
		t.Fatalf("expected caller display name, got %#v", caller)
	}
}

func TestAuthenticatorReadsAlternateUserDisplayNameHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/commands", nil)
	req.Header.Set("X-Tenant-ID", "tenant_1")
	req.Header.Set("X-Caller-ID", "user_1")
	req.Header.Set("X-User-Nickname", "Alice")

	caller, ok := New("").Authenticate(req)
	if !ok {
		t.Fatal("expected request to authenticate")
	}
	if caller.DisplayName != "Alice" {
		t.Fatalf("expected alternate display name header, got %#v", caller)
	}
}
