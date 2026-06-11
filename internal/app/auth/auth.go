package auth

import (
	"context"
	"net/http"
	"strings"

	"znt/internal/contracts"
)

type Role string

const (
	RoleRuntimeCaller Role = "runtime_caller"
	RoleOptimizer     Role = "optimizer"
	RoleAdmin         Role = "admin"
)

type CallerIdentity struct {
	CallerID   string             `json:"caller_id"`
	CallerType string             `json:"caller_type"`
	TenantID   contracts.TenantID `json:"tenant_id"`
	Roles      []Role             `json:"roles"`
}

type contextKey struct{}

type Authenticator struct {
	ServiceToken string
}

func New(serviceToken string) Authenticator {
	return Authenticator{ServiceToken: serviceToken}
}

func (a Authenticator) Authenticate(r *http.Request) (CallerIdentity, bool) {
	tenantID := contracts.TenantID(strings.TrimSpace(r.Header.Get("X-Tenant-ID")))
	callerID := strings.TrimSpace(r.Header.Get("X-Caller-ID"))
	callerType := strings.TrimSpace(r.Header.Get("X-Caller-Type"))
	if callerType == "" {
		callerType = "service"
	}
	if callerID == "" {
		callerID = "anonymous"
	}
	if tenantID == "" {
		return CallerIdentity{}, false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if a.ServiceToken != "" && token != a.ServiceToken {
		return CallerIdentity{}, false
	}
	roles := []Role{RoleRuntimeCaller}
	if raw := strings.TrimSpace(r.Header.Get("X-Roles")); raw != "" {
		roles = parseRoles(raw)
	}
	return CallerIdentity{
		CallerID:   callerID,
		CallerType: callerType,
		TenantID:   tenantID,
		Roles:      roles,
	}, true
}

func WithCaller(ctx context.Context, caller CallerIdentity) context.Context {
	return context.WithValue(ctx, contextKey{}, caller)
}

func CallerFromContext(ctx context.Context) (CallerIdentity, bool) {
	caller, ok := ctx.Value(contextKey{}).(CallerIdentity)
	return caller, ok
}

func (c CallerIdentity) HasRole(role Role) bool {
	for _, current := range c.Roles {
		if current == role {
			return true
		}
	}
	return false
}

func parseRoles(raw string) []Role {
	parts := strings.Split(raw, ",")
	roles := make([]Role, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			roles = append(roles, Role(part))
		}
	}
	if len(roles) == 0 {
		return []Role{RoleRuntimeCaller}
	}
	return roles
}
