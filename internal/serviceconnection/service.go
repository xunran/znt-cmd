package serviceconnection

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"znt/internal/contracts"
	"znt/pkg/idgen"
)

const (
	TypeCleanCore = "clean_core"
	TypeHTTPAPI   = "http_api"
	TypeDatabase  = "database"
	TypeWebhook   = "webhook"
	TypeOAuth     = "oauth"
	TypeCache     = "cache"
	TypeQueue     = "queue"
	TypeStorage   = "storage"

	AuthTypeNone          = "none"
	AuthTypeAPIKey        = "api_key"
	AuthTypeBearer        = "bearer"
	AuthTypeBasic         = "basic"
	AuthTypeOAuth2        = "oauth2"
	AuthTypeSignedRequest = "signed_request"
	AuthTypeMTLS          = "mtls"

	StatusDraft    = "draft"
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

type ServiceConnection struct {
	TenantID           contracts.TenantID `json:"tenant_id,omitempty"`
	ConnectionID       string             `json:"connection_id"`
	Name               string             `json:"name"`
	ConnectionType     string             `json:"connection_type"`
	Environment        string             `json:"environment,omitempty"`
	Status             string             `json:"status"`
	HealthStatus       string             `json:"health_status,omitempty"`
	Description        string             `json:"description,omitempty"`
	BaseURL            string             `json:"base_url,omitempty"`
	AuthType           string             `json:"auth_type,omitempty"`
	AuthRef            string             `json:"auth_ref,omitempty"`
	NetworkScope       string             `json:"network_scope,omitempty"`
	TimeoutMS          int                `json:"timeout_ms,omitempty"`
	RetryMax           int                `json:"retry_max,omitempty"`
	HealthCheckEnabled bool               `json:"health_check_enabled"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
	LastHealthAt       *time.Time         `json:"last_health_at,omitempty"`
	LastHealthError    string             `json:"last_health_error,omitempty"`
	Version            string             `json:"version,omitempty"`
}

type ServiceConnectionResource struct {
	TenantID     contracts.TenantID `json:"tenant_id,omitempty"`
	ConnectionID string             `json:"connection_id"`
	ResourceID   string             `json:"resource_id"`
	ResourceType string             `json:"resource_type"`
	Name         string             `json:"name"`
	Schema       map[string]any     `json:"schema,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
	DiscoveredAt time.Time          `json:"discovered_at"`
}

type ServiceConnectionHealthEvent struct {
	TenantID     contracts.TenantID `json:"tenant_id,omitempty"`
	ConnectionID string             `json:"connection_id"`
	HealthStatus string             `json:"health_status"`
	Error        string             `json:"error,omitempty"`
	LatencyMS    int                `json:"latency_ms,omitempty"`
	CheckedAt    time.Time          `json:"checked_at"`
}

type ServiceConnectionSecretRotationRequest struct {
	AuthRef   string `json:"auth_ref"`
	AuthType  string `json:"auth_type,omitempty"`
	Reason    string `json:"reason,omitempty"`
	RotatedBy string `json:"rotated_by,omitempty"`
}

type ServiceConnectionSecretRotation struct {
	TenantID            contracts.TenantID `json:"tenant_id,omitempty"`
	ConnectionID        string             `json:"connection_id"`
	RotationID          string             `json:"rotation_id"`
	AuthType            string             `json:"auth_type,omitempty"`
	PreviousAuthRefHash string             `json:"previous_auth_ref_hash,omitempty"`
	NewAuthRefHash      string             `json:"new_auth_ref_hash"`
	Reason              string             `json:"reason,omitempty"`
	RotatedBy           string             `json:"rotated_by,omitempty"`
	RotatedAt           time.Time          `json:"rotated_at"`
}

type ListFilter struct {
	ConnectionType string
	Status         string
	HealthStatus   string
	Environment    string
	Query          string
	PageSize       int
	Cursor         string
}

type Store interface {
	UpsertConnection(ctx context.Context, connection ServiceConnection) error
	UpsertConnectionAndReplaceResources(ctx context.Context, connection ServiceConnection, resources []ServiceConnectionResource) error
	GetConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, bool, error)
	ListConnections(ctx context.Context, tenantID contracts.TenantID, filter ListFilter) ([]ServiceConnection, error)
	DeleteConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) error
	UpsertResource(ctx context.Context, resource ServiceConnectionResource) error
	ReplaceResources(ctx context.Context, tenantID contracts.TenantID, connectionID string, resources []ServiceConnectionResource) error
	ListResources(ctx context.Context, tenantID contracts.TenantID, connectionID string) ([]ServiceConnectionResource, error)
	AppendHealthEvent(ctx context.Context, event ServiceConnectionHealthEvent) error
	ListHealthEvents(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]ServiceConnectionHealthEvent, error)
	AppendSecretRotation(ctx context.Context, rotation ServiceConnectionSecretRotation) error
	ListSecretRotations(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]ServiceConnectionSecretRotation, error)
}

type Service struct {
	mu        sync.RWMutex
	store     Store
	items     map[string]ServiceConnection
	res       map[string][]ServiceConnectionResource
	health    map[string][]ServiceConnectionHealthEvent
	rotations map[string][]ServiceConnectionSecretRotation
	client    *http.Client
	openDB    func(driverName string, dataSourceName string) (*sql.DB, error)
	now       func() time.Time
}

func NewServiceWithStore(store Store) *Service {
	return &Service{
		store:     store,
		items:     map[string]ServiceConnection{},
		res:       map[string][]ServiceConnectionResource{},
		health:    map[string][]ServiceConnectionHealthEvent{},
		rotations: map[string][]ServiceConnectionSecretRotation{},
		client:    &http.Client{Timeout: 10 * time.Second},
		openDB:    sql.Open,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Upsert(ctx context.Context, connection ServiceConnection) (ServiceConnection, error) {
	connection = normalizeConnection(connection)
	if connection.ConnectionID == "" {
		connection.ConnectionID = idgen.New("conn")
	}
	var err error
	var clearResources bool
	connection, clearResources, err = s.normalizeConnectionForUpsert(ctx, connection)
	if err != nil {
		return ServiceConnection{}, err
	}
	if err := validateConnection(connection); err != nil {
		return ServiceConnection{}, err
	}
	if s.store != nil {
		if clearResources {
			if err := s.store.UpsertConnectionAndReplaceResources(ctx, connection, nil); err != nil {
				return ServiceConnection{}, err
			}
		} else if err := s.store.UpsertConnection(ctx, connection); err != nil {
			return ServiceConnection{}, err
		}
	}
	s.mu.Lock()
	key := connectionKey(connection.TenantID, connection.ConnectionID)
	s.items[key] = connection
	if clearResources {
		delete(s.res, key)
	}
	s.mu.Unlock()
	return connection, nil
}

func (s *Service) normalizeConnectionForUpsert(ctx context.Context, connection ServiceConnection) (ServiceConnection, bool, error) {
	connection = normalizeConnection(connection)
	existing, ok, err := s.Get(ctx, connection.TenantID, connection.ConnectionID)
	if err != nil {
		return ServiceConnection{}, false, err
	}
	if ok {
		existing = normalizeConnection(existing)
		clearResources := !connectionResourceScopeMatches(existing, connection)
		if connectionHealthScopeMatches(existing, connection) {
			connection.HealthStatus = existing.HealthStatus
			connection.LastHealthAt = existing.LastHealthAt
			connection.LastHealthError = existing.LastHealthError
		} else {
			connection.HealthStatus = HealthUnknown
			connection.LastHealthAt = nil
			connection.LastHealthError = ""
		}
		return connection, clearResources, nil
	}
	connection.HealthStatus = HealthUnknown
	connection.LastHealthAt = nil
	connection.LastHealthError = ""
	return connection, false, nil
}

func connectionHealthScopeMatches(existing ServiceConnection, next ServiceConnection) bool {
	return strings.TrimSpace(existing.ConnectionType) == strings.TrimSpace(next.ConnectionType) &&
		strings.TrimSpace(existing.BaseURL) == strings.TrimSpace(next.BaseURL) &&
		strings.TrimSpace(existing.AuthType) == strings.TrimSpace(next.AuthType) &&
		strings.TrimSpace(existing.AuthRef) == strings.TrimSpace(next.AuthRef) &&
		strings.TrimSpace(existing.NetworkScope) == strings.TrimSpace(next.NetworkScope) &&
		existing.TimeoutMS == next.TimeoutMS &&
		existing.RetryMax == next.RetryMax &&
		existing.HealthCheckEnabled == next.HealthCheckEnabled &&
		reflect.DeepEqual(existing.Metadata, next.Metadata)
}

func connectionResourceScopeMatches(existing ServiceConnection, next ServiceConnection) bool {
	if !connectionTypeUsesResourceCatalog(existing.ConnectionType) && !connectionTypeUsesResourceCatalog(next.ConnectionType) {
		return true
	}
	return strings.TrimSpace(existing.ConnectionType) == strings.TrimSpace(next.ConnectionType) &&
		strings.TrimSpace(existing.BaseURL) == strings.TrimSpace(next.BaseURL) &&
		strings.TrimSpace(existing.AuthType) == strings.TrimSpace(next.AuthType) &&
		strings.TrimSpace(existing.AuthRef) == strings.TrimSpace(next.AuthRef) &&
		reflect.DeepEqual(existing.Metadata, next.Metadata)
}

func connectionTypeUsesResourceCatalog(connectionType string) bool {
	return connectionType == TypeDatabase || connectionType == TypeHTTPAPI
}

func (s *Service) Get(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, bool, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return ServiceConnection{}, false, nil
	}
	if s.store != nil {
		return s.store.GetConnection(ctx, tenantID, connectionID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	connection, ok := s.items[connectionKey(tenantID, connectionID)]
	return connection, ok, nil
}

func (s *Service) List(ctx context.Context, tenantID contracts.TenantID, filter ListFilter) ([]ServiceConnection, error) {
	filter = normalizeListFilter(filter)
	if s.store != nil {
		return s.store.ListConnections(ctx, tenantID, filter)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServiceConnection, 0, len(s.items))
	for _, connection := range s.items {
		if connection.TenantID != tenantID {
			continue
		}
		if filter.ConnectionType != "" && connection.ConnectionType != filter.ConnectionType {
			continue
		}
		if filter.Status != "" && connection.Status != filter.Status {
			continue
		}
		if filter.HealthStatus != "" && connection.HealthStatus != filter.HealthStatus {
			continue
		}
		if filter.Environment != "" && connection.Environment != filter.Environment {
			continue
		}
		if !connectionMatchesFilter(connection, filter) {
			continue
		}
		out = append(out, connection)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectionID < out[j].ConnectionID })
	return paginateConnections(out, filter.Cursor, filter.PageSize), nil
}

func normalizeListFilter(filter ListFilter) ListFilter {
	filter.ConnectionType = strings.TrimSpace(filter.ConnectionType)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.HealthStatus = strings.TrimSpace(filter.HealthStatus)
	filter.Environment = strings.TrimSpace(filter.Environment)
	filter.Query = strings.TrimSpace(filter.Query)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	if filter.PageSize < 0 {
		filter.PageSize = 0
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	return filter
}

func connectionMatchesFilter(connection ServiceConnection, filter ListFilter) bool {
	if filter.Query == "" {
		return true
	}
	query := strings.ToLower(filter.Query)
	for _, value := range []string{connection.ConnectionID, connection.Name, connection.Description, connection.BaseURL, connection.ConnectionType, connection.Environment} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func paginateConnections(connections []ServiceConnection, cursor string, pageSize int) []ServiceConnection {
	if cursor != "" {
		out := connections[:0]
		for _, connection := range connections {
			if connection.ConnectionID > cursor {
				out = append(out, connection)
			}
		}
		connections = out
	}
	if pageSize > 0 && len(connections) > pageSize {
		return connections[:pageSize]
	}
	return connections
}

func (s *Service) Delete(ctx context.Context, tenantID contracts.TenantID, connectionID string) error {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id is required", nil)
	}
	if s.store != nil {
		if err := s.store.DeleteConnection(ctx, tenantID, connectionID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	delete(s.items, connectionKey(tenantID, connectionID))
	delete(s.res, connectionKey(tenantID, connectionID))
	delete(s.health, connectionKey(tenantID, connectionID))
	delete(s.rotations, connectionKey(tenantID, connectionID))
	s.mu.Unlock()
	return nil
}

func (s *Service) Enable(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, error) {
	connection, ok, err := s.Get(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, err
	}
	if !ok {
		return ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	connection.Status = StatusEnabled
	return s.Upsert(ctx, connection)
}

func (s *Service) Disable(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, error) {
	connection, ok, err := s.Get(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, err
	}
	if !ok {
		return ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	connection.Status = StatusDisabled
	return s.Upsert(ctx, connection)
}

func (s *Service) Test(ctx context.Context, tenantID contracts.TenantID, connectionID string) (ServiceConnection, []ServiceConnectionResource, error) {
	connection, ok, err := s.Get(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, nil, err
	}
	if !ok {
		return ServiceConnection{}, nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	healthAt := s.now()
	started := healthAt
	var testErr error
	connection.LastHealthAt = &healthAt
	connection.LastHealthError = ""
	switch connection.ConnectionType {
	case TypeHTTPAPI, TypeWebhook, TypeOAuth, TypeCleanCore:
		if strings.TrimSpace(connection.BaseURL) == "" {
			connection.HealthStatus = HealthUnhealthy
			connection.LastHealthError = "base_url is required"
		} else if err := s.probeHTTP(ctx, connection); err != nil {
			connection.HealthStatus = HealthUnhealthy
			connection.LastHealthError = err.Error()
		} else {
			connection.HealthStatus = HealthHealthy
			if connection.ConnectionType == TypeHTTPAPI {
				resources, err := s.discoverHTTPAPIResources(ctx, connection)
				if err != nil {
					connection.HealthStatus = HealthUnhealthy
					connection.LastHealthError = err.Error()
					var runtimeErr *contracts.RuntimeError
					if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeToolArgumentInvalid {
						testErr = err
					}
				} else if err := s.ReplaceResources(ctx, tenantID, connectionID, resources); err != nil {
					return ServiceConnection{}, nil, err
				}
			}
		}
	case TypeDatabase:
		resources, err := s.probeDatabaseAndDiscover(ctx, connection)
		if err != nil {
			connection.HealthStatus = HealthUnhealthy
			connection.LastHealthError = err.Error()
			var runtimeErr *contracts.RuntimeError
			if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeToolArgumentInvalid {
				testErr = err
			}
		} else {
			connection.HealthStatus = HealthHealthy
			if err := s.ReplaceResources(ctx, tenantID, connectionID, resources); err != nil {
				return ServiceConnection{}, nil, err
			}
		}
	default:
		connection.HealthStatus = HealthUnknown
		connection.LastHealthError = "connection test is not implemented for " + connection.ConnectionType
		testErr = contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, connection.LastHealthError, map[string]any{"connection_type": connection.ConnectionType})
	}
	updated, err := s.storeConnectionHealth(ctx, connection, healthAt, connection.HealthStatus, connection.LastHealthError)
	if err != nil {
		return ServiceConnection{}, nil, err
	}
	if err := s.AppendHealthEvent(ctx, ServiceConnectionHealthEvent{
		TenantID:     tenantID,
		ConnectionID: connectionID,
		HealthStatus: connection.HealthStatus,
		Error:        connection.LastHealthError,
		LatencyMS:    int(s.now().Sub(started).Milliseconds()),
		CheckedAt:    healthAt,
	}); err != nil {
		return ServiceConnection{}, nil, err
	}
	resources, err := s.ListResources(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, nil, err
	}
	if testErr != nil {
		return updated, resources, testErr
	}
	return updated, resources, nil
}

func (s *Service) setConnectionHealth(ctx context.Context, tenantID contracts.TenantID, connectionID string, status string, healthError string) (ServiceConnection, error) {
	connection, ok, err := s.Get(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, err
	}
	if !ok {
		return ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	return s.storeConnectionHealth(ctx, connection, s.now(), status, healthError)
}

func (s *Service) storeConnectionHealth(ctx context.Context, connection ServiceConnection, checkedAt time.Time, status string, healthError string) (ServiceConnection, error) {
	connection = normalizeConnection(connection)
	status = strings.TrimSpace(status)
	if !validHealthStatus(status) {
		return ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported connection health_status", map[string]any{"health_status": status})
	}
	connection.HealthStatus = status
	connection.LastHealthAt = &checkedAt
	connection.LastHealthError = strings.TrimSpace(healthError)
	if err := validateConnection(connection); err != nil {
		return ServiceConnection{}, err
	}
	if s.store != nil {
		if err := s.store.UpsertConnection(ctx, connection); err != nil {
			return ServiceConnection{}, err
		}
	}
	s.mu.Lock()
	s.items[connectionKey(connection.TenantID, connection.ConnectionID)] = connection
	s.mu.Unlock()
	return connection, nil
}

func (s *Service) probeDatabaseAndDiscover(ctx context.Context, connection ServiceConnection) ([]ServiceConnectionResource, error) {
	dsn := strings.TrimSpace(connection.BaseURL)
	if dsn == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database base_url is required", map[string]any{"connection_id": connection.ConnectionID})
	}
	driverName, err := DatabaseDriverName(connection)
	if err != nil {
		return nil, err
	}
	db, err := s.openDB(driverName, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	reqCtx, cancel := contextWithTimeout(ctx, connection.TimeoutMS)
	defer cancel()
	if err := db.PingContext(reqCtx); err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "database health check failed", map[string]any{"connection_id": connection.ConnectionID, "error": err.Error()})
	}
	return s.discoverPostgresResources(reqCtx, db, connection)
}

func DatabaseDriverName(connection ServiceConnection) (string, error) {
	driver := strings.ToLower(strings.TrimSpace(metadataString(connection.Metadata, "driver")))
	if driver == "" {
		driver = strings.ToLower(strings.TrimSpace(metadataString(connection.Metadata, "engine")))
	}
	if driver == "" {
		baseURL := strings.ToLower(strings.TrimSpace(connection.BaseURL))
		if strings.HasPrefix(baseURL, "postgres://") || strings.HasPrefix(baseURL, "postgresql://") {
			driver = "postgres"
		}
	}
	if driver == "" {
		driver = "postgres"
	}
	switch driver {
	case "postgres", "postgresql", "pgx":
		return "pgx", nil
	default:
		return "", contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported database driver", map[string]any{"driver": driver})
	}
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return value
	default:
		return ""
	}
}

func (s *Service) discoverPostgresResources(ctx context.Context, db *sql.DB, connection ServiceConnection) ([]ServiceConnectionResource, error) {
	rows, err := db.QueryContext(ctx, `
SELECT t.table_schema, t.table_name, t.table_type,
       c.column_name, c.data_type, c.is_nullable, c.ordinal_position
FROM information_schema.tables t
LEFT JOIN information_schema.columns c
  ON c.table_schema = t.table_schema
 AND c.table_name = t.table_name
WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
  AND t.table_type IN ('BASE TABLE', 'VIEW')
ORDER BY t.table_schema, t.table_name, c.ordinal_position`)
	if err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "database resource discovery failed", map[string]any{"connection_id": connection.ConnectionID, "error": err.Error()})
	}
	defer rows.Close()

	type discoveredResource struct {
		resource ServiceConnectionResource
		columns  []map[string]any
	}
	discovered := map[string]*discoveredResource{}
	order := []string{}
	now := s.now()
	for rows.Next() {
		var tableSchema, tableName, tableType string
		var columnName, dataType, isNullable sql.NullString
		var ordinal sql.NullInt64
		if err := rows.Scan(&tableSchema, &tableName, &tableType, &columnName, &dataType, &isNullable, &ordinal); err != nil {
			return nil, err
		}
		resourceID := tableSchema + "." + tableName
		item, ok := discovered[resourceID]
		if !ok {
			resourceType := "table"
			if tableType == "VIEW" {
				resourceType = "view"
			}
			item = &discoveredResource{
				resource: ServiceConnectionResource{
					TenantID:     connection.TenantID,
					ConnectionID: connection.ConnectionID,
					ResourceID:   resourceID,
					ResourceType: resourceType,
					Name:         resourceID,
					Schema:       map[string]any{"type": "object", "columns": []map[string]any{}},
					Metadata: map[string]any{
						"schema":     tableSchema,
						"table_name": tableName,
						"table_type": tableType,
						"driver":     "postgres",
					},
					DiscoveredAt: now,
				},
			}
			discovered[resourceID] = item
			order = append(order, resourceID)
		}
		if columnName.Valid {
			item.columns = append(item.columns, map[string]any{
				"name":        columnName.String,
				"data_type":   dataType.String,
				"nullable":    strings.EqualFold(isNullable.String, "YES"),
				"ordinal":     ordinal.Int64,
				"json_schema": postgresColumnJSONSchema(dataType.String),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(order)
	resources := make([]ServiceConnectionResource, 0, len(order))
	for _, resourceID := range order {
		item := discovered[resourceID]
		item.resource.Schema["columns"] = item.columns
		resources = append(resources, item.resource)
	}
	return resources, nil
}

func postgresColumnJSONSchema(dataType string) map[string]any {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "smallint", "integer", "bigint":
		return map[string]any{"type": "integer"}
	case "numeric", "decimal", "real", "double precision":
		return map[string]any{"type": "number"}
	case "boolean":
		return map[string]any{"type": "boolean"}
	case "json", "jsonb":
		return map[string]any{"type": "object"}
	case "date", "timestamp without time zone", "timestamp with time zone", "time without time zone", "time with time zone":
		return map[string]any{"type": "string", "format": "date-time"}
	default:
		return map[string]any{"type": "string"}
	}
}

func (s *Service) probeHTTP(ctx context.Context, connection ServiceConnection) error {
	base := strings.TrimRight(connection.BaseURL, "/")
	candidates := []string{base + "/healthz", base}
	if strings.HasSuffix(base, "/healthz") {
		candidates = []string{base}
	}
	var lastErr error
	for _, endpoint := range candidates {
		if err := s.probeHTTPOnce(ctx, endpoint, connection); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (s *Service) probeHTTPOnce(ctx context.Context, endpoint string, connection ServiceConnection) error {
	attempts := connection.RetryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		reqCtx, cancel := contextWithTimeout(ctx, connection.TimeoutMS)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return err
		}
		if connection.AuthRef != "" {
			req.Header.Set("X-Origin-Provider-Auth-Ref", connection.AuthRef)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
		lastErr = contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "service connection health check failed", map[string]any{"endpoint": endpoint, "status": resp.StatusCode})
		if resp.StatusCode < 500 {
			break
		}
	}
	return lastErr
}

func (s *Service) discoverHTTPAPIResources(ctx context.Context, connection ServiceConnection) ([]ServiceConnectionResource, error) {
	openAPIPath := strings.TrimSpace(metadataString(connection.Metadata, "openapi_path"))
	if openAPIPath == "" {
		return nil, nil
	}
	parsed, err := url.Parse(openAPIPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_api openapi_path must be an absolute path on base_url", map[string]any{
			"connection_id": connection.ConnectionID,
			"openapi_path":  openAPIPath,
		})
	}
	endpoint := strings.TrimRight(connection.BaseURL, "/") + parsed.RequestURI()
	reqCtx, cancel := contextWithTimeout(ctx, connection.TimeoutMS)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if connection.AuthRef != "" {
		req.Header.Set("X-Origin-Provider-Auth-Ref", connection.AuthRef)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "http_api openapi discovery failed", map[string]any{"connection_id": connection.ConnectionID, "error": err.Error()})
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "http_api openapi discovery failed", map[string]any{"connection_id": connection.ConnectionID, "endpoint": endpoint, "status": resp.StatusCode})
	}
	var spec map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&spec); err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "http_api openapi discovery returned invalid json", map[string]any{"connection_id": connection.ConnectionID, "error": err.Error()})
	}
	return s.resourcesFromOpenAPISpec(connection, openAPIPath, spec)
}

func (s *Service) resourcesFromOpenAPISpec(connection ServiceConnection, openAPIPath string, spec map[string]any) ([]ServiceConnectionResource, error) {
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_api openapi document must contain paths", map[string]any{"connection_id": connection.ConnectionID})
	}
	now := s.now()
	resources := make([]ServiceConnectionResource, 0)
	for path, pathValue := range paths {
		pathItem, ok := pathValue.(map[string]any)
		if !ok {
			continue
		}
		pathParameters := pathItem["parameters"]
		for method, operationValue := range pathItem {
			method = strings.ToLower(strings.TrimSpace(method))
			if !isOpenAPIOperationMethod(method) {
				continue
			}
			operation, ok := operationValue.(map[string]any)
			if !ok {
				continue
			}
			methodUpper := strings.ToUpper(method)
			operationID := stringValue(operation["operationId"])
			summary := stringValue(operation["summary"])
			description := stringValue(operation["description"])
			resourceID := methodUpper + " " + path
			name := summary
			if name == "" {
				name = operationID
			}
			if name == "" {
				name = resourceID
			}
			schema := map[string]any{
				"type":         "object",
				"method":       methodUpper,
				"path":         path,
				"operation_id": operationID,
				"summary":      summary,
				"description":  description,
				"parameters":   mergeOpenAPIParameters(pathParameters, operation["parameters"]),
				"request_body": operation["requestBody"],
				"responses":    operation["responses"],
			}
			metadata := map[string]any{
				"source":       "openapi",
				"openapi_path": openAPIPath,
				"method":       methodUpper,
				"path":         path,
			}
			if operationID != "" {
				metadata["operation_id"] = operationID
			}
			if tags := stringSliceValue(operation["tags"]); len(tags) > 0 {
				metadata["tags"] = tags
			}
			resources = append(resources, ServiceConnectionResource{
				TenantID:     connection.TenantID,
				ConnectionID: connection.ConnectionID,
				ResourceID:   resourceID,
				ResourceType: "http_operation",
				Name:         name,
				Schema:       schema,
				Metadata:     metadata,
				DiscoveredAt: now,
			})
		}
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ResourceID < resources[j].ResourceID })
	return resources, nil
}

func isOpenAPIOperationMethod(method string) bool {
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stringSliceValue(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text := stringValue(item)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func mergeOpenAPIParameters(pathParameters any, operationParameters any) any {
	out := []any{}
	seen := map[string]int{}
	add := func(values any) {
		raw, ok := values.([]any)
		if !ok {
			return
		}
		for _, value := range raw {
			param, ok := value.(map[string]any)
			if !ok {
				out = append(out, value)
				continue
			}
			key := stringValue(param["in"]) + "\x00" + stringValue(param["name"])
			if key == "\x00" {
				out = append(out, value)
				continue
			}
			if index, ok := seen[key]; ok {
				out[index] = value
				continue
			}
			seen[key] = len(out)
			out = append(out, value)
		}
	}
	add(pathParameters)
	add(operationParameters)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Service) ListResources(ctx context.Context, tenantID contracts.TenantID, connectionID string) ([]ServiceConnectionResource, error) {
	connectionID = strings.TrimSpace(connectionID)
	if s.store != nil {
		return s.store.ListResources(ctx, tenantID, connectionID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	resources := s.res[connectionKey(tenantID, connectionID)]
	out := make([]ServiceConnectionResource, len(resources))
	copy(out, resources)
	return out, nil
}

func (s *Service) ReplaceResources(ctx context.Context, tenantID contracts.TenantID, connectionID string, resources []ServiceConnectionResource) error {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id is required", nil)
	}
	for i := range resources {
		resources[i].TenantID = tenantID
		resources[i].ConnectionID = connectionID
		if resources[i].DiscoveredAt.IsZero() {
			resources[i].DiscoveredAt = s.now()
		}
		if resources[i].Schema == nil {
			resources[i].Schema = map[string]any{}
		}
		if resources[i].Metadata == nil {
			resources[i].Metadata = map[string]any{}
		}
	}
	if s.store != nil {
		return s.store.ReplaceResources(ctx, tenantID, connectionID, resources)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]ServiceConnectionResource, len(resources))
	copy(copied, resources)
	s.res[connectionKey(tenantID, connectionID)] = copied
	return nil
}

func (s *Service) AppendHealthEvent(ctx context.Context, event ServiceConnectionHealthEvent) error {
	event.ConnectionID = strings.TrimSpace(event.ConnectionID)
	if event.ConnectionID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id is required", nil)
	}
	if event.HealthStatus == "" {
		event.HealthStatus = HealthUnknown
	}
	if !validHealthStatus(event.HealthStatus) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported connection health_status", map[string]any{"health_status": event.HealthStatus})
	}
	if event.CheckedAt.IsZero() {
		event.CheckedAt = s.now()
	}
	event.CheckedAt = event.CheckedAt.UTC()
	if event.LatencyMS < 0 {
		event.LatencyMS = 0
	}
	if s.store != nil {
		return s.store.AppendHealthEvent(ctx, event)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := connectionKey(event.TenantID, event.ConnectionID)
	s.health[key] = append(s.health[key], event)
	return nil
}

func (s *Service) ListHealthEvents(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]ServiceConnectionHealthEvent, error) {
	connectionID = strings.TrimSpace(connectionID)
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if s.store != nil {
		return s.store.ListHealthEvents(ctx, tenantID, connectionID, limit)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := s.health[connectionKey(tenantID, connectionID)]
	copied := make([]ServiceConnectionHealthEvent, len(events))
	copy(copied, events)
	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].CheckedAt.After(copied[j].CheckedAt)
	})
	out := make([]ServiceConnectionHealthEvent, 0, len(events))
	for _, event := range copied {
		if len(out) >= limit {
			break
		}
		out = append(out, event)
	}
	return out, nil
}

func (s *Service) RotateSecret(ctx context.Context, tenantID contracts.TenantID, connectionID string, request ServiceConnectionSecretRotationRequest) (ServiceConnection, ServiceConnectionSecretRotation, error) {
	connectionID = strings.TrimSpace(connectionID)
	authRef := strings.TrimSpace(request.AuthRef)
	if connectionID == "" {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id is required", nil)
	}
	if authRef == "" {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_ref is required", nil)
	}
	connection, ok, err := s.Get(ctx, tenantID, connectionID)
	if err != nil {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, err
	}
	if !ok {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	authType := normalizeAuthType(request.AuthType)
	if err := validateAuthConfig(authType, authRef); err != nil {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, err
	}
	if authRef == connection.AuthRef && authType == connection.AuthType {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_ref is unchanged", map[string]any{"connection_id": connectionID})
	}
	rotatedAt := s.now()
	rotation := ServiceConnectionSecretRotation{
		TenantID:            tenantID,
		ConnectionID:        connectionID,
		RotationID:          idgen.New("connrot"),
		AuthType:            authType,
		PreviousAuthRefHash: authRefHash(connection.AuthRef),
		NewAuthRefHash:      authRefHash(authRef),
		Reason:              strings.TrimSpace(request.Reason),
		RotatedBy:           strings.TrimSpace(request.RotatedBy),
		RotatedAt:           rotatedAt,
	}
	connection.AuthRef = authRef
	connection.AuthType = authType
	connection.HealthStatus = HealthUnknown
	connection.LastHealthAt = nil
	connection.LastHealthError = ""
	updated, err := s.Upsert(ctx, connection)
	if err != nil {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, err
	}
	if err := s.AppendSecretRotation(ctx, rotation); err != nil {
		return ServiceConnection{}, ServiceConnectionSecretRotation{}, err
	}
	return updated, rotation, nil
}

func (s *Service) AppendSecretRotation(ctx context.Context, rotation ServiceConnectionSecretRotation) error {
	rotation.ConnectionID = strings.TrimSpace(rotation.ConnectionID)
	if rotation.ConnectionID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_id is required", nil)
	}
	if rotation.RotationID == "" {
		rotation.RotationID = idgen.New("connrot")
	}
	if rotation.RotatedAt.IsZero() {
		rotation.RotatedAt = s.now()
	}
	rotation.RotatedAt = rotation.RotatedAt.UTC()
	rotation.AuthType = normalizeAuthType(rotation.AuthType)
	if rotation.AuthType == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_type is required", nil)
	}
	if !validAuthType(rotation.AuthType) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported auth_type", map[string]any{"auth_type": rotation.AuthType})
	}
	if rotation.AuthType == AuthTypeNone {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_type none is not valid for secret rotation", nil)
	}
	rotation.Reason = strings.TrimSpace(rotation.Reason)
	rotation.RotatedBy = strings.TrimSpace(rotation.RotatedBy)
	if rotation.NewAuthRefHash == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "new_auth_ref_hash is required", nil)
	}
	if s.store != nil {
		return s.store.AppendSecretRotation(ctx, rotation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := connectionKey(rotation.TenantID, rotation.ConnectionID)
	s.rotations[key] = append(s.rotations[key], rotation)
	return nil
}

func (s *Service) ListSecretRotations(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]ServiceConnectionSecretRotation, error) {
	connectionID = strings.TrimSpace(connectionID)
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if s.store != nil {
		return s.store.ListSecretRotations(ctx, tenantID, connectionID, limit)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rotations := s.rotations[connectionKey(tenantID, connectionID)]
	copied := make([]ServiceConnectionSecretRotation, len(rotations))
	copy(copied, rotations)
	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].RotatedAt.After(copied[j].RotatedAt)
	})
	out := make([]ServiceConnectionSecretRotation, 0, len(rotations))
	for _, rotation := range copied {
		if len(out) >= limit {
			break
		}
		out = append(out, rotation)
	}
	return out, nil
}

func authRefHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Templates() []map[string]any {
	return []map[string]any{
		{"template_id": TypeHTTPAPI, "name": "HTTP API", "connection_type": TypeHTTPAPI},
		{"template_id": TypeCleanCore, "name": "CleanCore API", "connection_type": TypeCleanCore},
		{"template_id": TypeDatabase, "name": "Database", "connection_type": TypeDatabase},
		{"template_id": TypeWebhook, "name": "Webhook", "connection_type": TypeWebhook},
		{"template_id": TypeOAuth, "name": "OAuth", "connection_type": TypeOAuth},
		{"template_id": TypeCache, "name": "Cache", "connection_type": TypeCache},
		{"template_id": TypeQueue, "name": "Queue", "connection_type": TypeQueue},
		{"template_id": TypeStorage, "name": "Storage", "connection_type": TypeStorage},
	}
}

func normalizeConnection(connection ServiceConnection) ServiceConnection {
	connection.ConnectionID = strings.TrimSpace(connection.ConnectionID)
	connection.Name = strings.TrimSpace(connection.Name)
	connection.ConnectionType = strings.TrimSpace(connection.ConnectionType)
	connection.Environment = strings.TrimSpace(connection.Environment)
	connection.Status = strings.TrimSpace(connection.Status)
	connection.Description = strings.TrimSpace(connection.Description)
	connection.BaseURL = strings.TrimSpace(connection.BaseURL)
	connection.AuthRef = strings.TrimSpace(connection.AuthRef)
	connection.AuthType = normalizeAuthType(connection.AuthType)
	if connection.AuthType == "" && connection.AuthRef == "" {
		connection.AuthType = AuthTypeNone
	}
	connection.NetworkScope = strings.TrimSpace(connection.NetworkScope)
	if connection.Status == "" {
		connection.Status = StatusDraft
	}
	if connection.HealthStatus == "" {
		connection.HealthStatus = HealthUnknown
	}
	if connection.Environment == "" {
		connection.Environment = "default"
	}
	if connection.Version == "" {
		connection.Version = "v1"
	}
	if connection.Metadata == nil {
		connection.Metadata = map[string]any{}
	}
	if connection.TimeoutMS < 0 {
		connection.TimeoutMS = 0
	}
	if connection.RetryMax < 0 {
		connection.RetryMax = 0
	}
	return connection
}

func validateConnection(connection ServiceConnection) error {
	if strings.TrimSpace(connection.Name) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection name is required", nil)
	}
	if strings.TrimSpace(connection.ConnectionType) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection_type is required", nil)
	}
	if !validConnectionType(connection.ConnectionType) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported connection_type", map[string]any{"connection_type": connection.ConnectionType})
	}
	if !validStatus(connection.Status) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported connection status", map[string]any{"status": connection.Status})
	}
	if !validHealthStatus(connection.HealthStatus) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported connection health_status", map[string]any{"health_status": connection.HealthStatus})
	}
	if connection.TimeoutMS < 0 || connection.RetryMax < 0 {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "connection timeout_ms and retry_max must be non-negative", nil)
	}
	if err := validateAuthConfig(connection.AuthType, connection.AuthRef); err != nil {
		return err
	}
	networkScopeHosts := make([]string, 0)
	for _, host := range splitNetworkScope(connection.NetworkScope) {
		normalizedHost := normalizeNetworkScopeHost(host)
		if normalizedHost == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "network_scope contains invalid host", map[string]any{"network_scope": connection.NetworkScope, "host": host})
		}
		networkScopeHosts = append(networkScopeHosts, normalizedHost)
	}
	if len(networkScopeHosts) > 0 && connectionTypeUsesHTTPNetworkScope(connection.ConnectionType) {
		baseHost := normalizeNetworkScopeHost(connection.BaseURL)
		if baseHost != "" && !networkScopeAllowsHost(baseHost, networkScopeHosts) {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "network_scope must include base_url host", map[string]any{
				"base_url":      connection.BaseURL,
				"base_url_host": baseHost,
			})
		}
	}
	return nil
}

func validConnectionType(value string) bool {
	switch value {
	case TypeCleanCore, TypeHTTPAPI, TypeDatabase, TypeWebhook, TypeOAuth, TypeCache, TypeQueue, TypeStorage:
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	switch value {
	case StatusDraft, StatusEnabled, StatusDisabled:
		return true
	default:
		return false
	}
}

func validHealthStatus(value string) bool {
	switch value {
	case HealthUnknown, HealthHealthy, HealthUnhealthy:
		return true
	default:
		return false
	}
}

func normalizeAuthType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validAuthType(value string) bool {
	switch normalizeAuthType(value) {
	case AuthTypeNone, AuthTypeAPIKey, AuthTypeBearer, AuthTypeBasic, AuthTypeOAuth2, AuthTypeSignedRequest, AuthTypeMTLS:
		return true
	default:
		return false
	}
}

func validateAuthConfig(authType string, authRef string) error {
	authType = normalizeAuthType(authType)
	authRef = strings.TrimSpace(authRef)
	if authType == "" && authRef == "" {
		return nil
	}
	if authType == "" && authRef != "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_type is required when auth_ref is set", nil)
	}
	if !validAuthType(authType) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported auth_type", map[string]any{"auth_type": authType})
	}
	if authType == AuthTypeNone && authRef != "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_type none must not set auth_ref", nil)
	}
	if authType != AuthTypeNone && authRef == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "auth_ref is required when auth_type is set", map[string]any{"auth_type": authType})
	}
	return nil
}

func connectionTypeUsesHTTPNetworkScope(value string) bool {
	switch value {
	case TypeCleanCore, TypeHTTPAPI, TypeWebhook, TypeOAuth:
		return true
	default:
		return false
	}
}

func networkScopeAllowsHost(host string, allowedHosts []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if allowed == "*" || allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func connectionKey(tenantID contracts.TenantID, connectionID string) string {
	return string(tenantID) + "\x00" + strings.TrimSpace(connectionID)
}

func contextWithTimeout(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func splitNetworkScope(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	var jsonValues []string
	if strings.HasPrefix(scope, "[") && json.Unmarshal([]byte(scope), &jsonValues) == nil {
		return jsonValues
	}
	return strings.FieldsFunc(scope, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func normalizeNetworkScopeHost(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "*.") {
		if strings.TrimPrefix(value, "*.") == "" {
			return ""
		}
		return strings.ToLower(value)
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Hostname())
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
