package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"znt/internal/contracts"
	serviceconnection "znt/internal/serviceconnection"
	storagerepo "znt/internal/storage/repository"
)

type ServiceConnectionStore struct {
	db *sql.DB
}

type serviceConnectionSQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *ServiceConnectionStore) UpsertConnection(ctx context.Context, connection serviceconnection.ServiceConnection) error {
	return s.upsertConnection(ctx, s.db, connection)
}

func (s *ServiceConnectionStore) UpsertConnectionAndReplaceResources(ctx context.Context, connection serviceconnection.ServiceConnection, resources []serviceconnection.ServiceConnectionResource) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.upsertConnection(ctx, tx, connection); err != nil {
		return err
	}
	if err := replaceServiceConnectionResources(ctx, tx, connection.TenantID, connection.ConnectionID, resources); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ServiceConnectionStore) upsertConnection(ctx context.Context, exec serviceConnectionSQLExecutor, connection serviceconnection.ServiceConnection) error {
	metadata, err := jsonValue(connection.Metadata)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = exec.ExecContext(ctx, `
INSERT INTO service_connections (
  tenant_id, connection_id, connection_type, name, environment, status,
  health_status, description, base_url, auth_type, auth_ref, network_scope,
  timeout_ms, retry_max, health_check_enabled, last_health_at, last_health_error,
  metadata_json, version, created_at, updated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (tenant_id, connection_id) DO UPDATE SET
  connection_type=EXCLUDED.connection_type,
  name=EXCLUDED.name,
  environment=EXCLUDED.environment,
  status=EXCLUDED.status,
  health_status=EXCLUDED.health_status,
  description=EXCLUDED.description,
  base_url=EXCLUDED.base_url,
  auth_type=EXCLUDED.auth_type,
  auth_ref=EXCLUDED.auth_ref,
  network_scope=EXCLUDED.network_scope,
  timeout_ms=EXCLUDED.timeout_ms,
  retry_max=EXCLUDED.retry_max,
  health_check_enabled=EXCLUDED.health_check_enabled,
  last_health_at=EXCLUDED.last_health_at,
  last_health_error=EXCLUDED.last_health_error,
  metadata_json=EXCLUDED.metadata_json,
  version=EXCLUDED.version,
  updated_at=EXCLUDED.updated_at`,
		connection.TenantID, connection.ConnectionID, connection.ConnectionType, connection.Name, connection.Environment, connection.Status, connection.HealthStatus,
		nullString(connection.Description), nullString(connection.BaseURL), nullString(connection.AuthType), nullString(connection.AuthRef),
		nullString(connection.NetworkScope), connection.TimeoutMS, connection.RetryMax, connection.HealthCheckEnabled,
		nullTime(connection.LastHealthAt), nullString(connection.LastHealthError), metadata, connection.Version, now, now,
	)
	return err
}

func (s *ServiceConnectionStore) GetConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) (serviceconnection.ServiceConnection, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tenant_id, connection_id, connection_type, name, environment, status,
  health_status, description, base_url, auth_type, auth_ref, network_scope, timeout_ms,
  retry_max, health_check_enabled, last_health_at, last_health_error,
  metadata_json, version
FROM service_connections
WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID)
	connection, err := scanServiceConnection(row)
	if err != nil {
		if errors.Is(err, storagerepo.ErrNotFound) {
			return serviceconnection.ServiceConnection{}, false, nil
		}
		return serviceconnection.ServiceConnection{}, false, err
	}
	return connection, true, nil
}

func (s *ServiceConnectionStore) ListConnections(ctx context.Context, tenantID contracts.TenantID, filter serviceconnection.ListFilter) ([]serviceconnection.ServiceConnection, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, connection_id, connection_type, name, environment, status,
  health_status, description, base_url, auth_type, auth_ref, network_scope, timeout_ms,
  retry_max, health_check_enabled, last_health_at, last_health_error,
  metadata_json, version
FROM service_connections
WHERE tenant_id=$1
  AND ($2 = '' OR connection_type = $2)
  AND ($3 = '' OR status = $3)
  AND ($4 = '' OR health_status = $4)
  AND ($5 = '' OR environment = $5)
  AND ($6 = '' OR connection_id ILIKE '%' || $6 || '%' OR name ILIKE '%' || $6 || '%' OR description ILIKE '%' || $6 || '%' OR base_url ILIKE '%' || $6 || '%' OR connection_type ILIKE '%' || $6 || '%' OR environment ILIKE '%' || $6 || '%')
  AND ($7 = '' OR connection_id > $7)
ORDER BY connection_id
LIMIT NULLIF($8, 0)`, tenantID, filter.ConnectionType, filter.Status, filter.HealthStatus, filter.Environment, filter.Query, filter.Cursor, filter.PageSize)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]serviceconnection.ServiceConnection, 0)
	for rows.Next() {
		connection, err := scanServiceConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, connection)
	}
	return out, rows.Err()
}

func (s *ServiceConnectionStore) DeleteConnection(ctx context.Context, tenantID contracts.TenantID, connectionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_connection_resources WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_connection_health_events WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_connection_secret_rotations WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_connections WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ServiceConnectionStore) UpsertResource(ctx context.Context, resource serviceconnection.ServiceConnectionResource) error {
	schemaJSON, err := jsonValue(resource.Schema)
	if err != nil {
		return err
	}
	metadata, err := jsonValue(resource.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO service_connection_resources (
  tenant_id, connection_id, resource_id, resource_type, name,
  schema_json, metadata_json, discovered_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (tenant_id, connection_id, resource_id) DO UPDATE SET
  resource_type=EXCLUDED.resource_type,
  name=EXCLUDED.name,
  schema_json=EXCLUDED.schema_json,
  metadata_json=EXCLUDED.metadata_json,
  discovered_at=EXCLUDED.discovered_at`,
		resource.TenantID, resource.ConnectionID, resource.ResourceID, resource.ResourceType, resource.Name,
		schemaJSON, metadata, resource.DiscoveredAt,
	)
	return err
}

func (s *ServiceConnectionStore) ReplaceResources(ctx context.Context, tenantID contracts.TenantID, connectionID string, resources []serviceconnection.ServiceConnectionResource) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceServiceConnectionResources(ctx, tx, tenantID, connectionID, resources); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceServiceConnectionResources(ctx context.Context, exec serviceConnectionSQLExecutor, tenantID contracts.TenantID, connectionID string, resources []serviceconnection.ServiceConnectionResource) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM service_connection_resources WHERE tenant_id=$1 AND connection_id=$2`, tenantID, connectionID); err != nil {
		return err
	}
	for _, resource := range resources {
		schemaJSON, err := jsonValue(resource.Schema)
		if err != nil {
			return err
		}
		metadata, err := jsonValue(resource.Metadata)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, `
INSERT INTO service_connection_resources (
  tenant_id, connection_id, resource_id, resource_type, name,
  schema_json, metadata_json, discovered_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenantID, connectionID, resource.ResourceID, resource.ResourceType, resource.Name,
			schemaJSON, metadata, resource.DiscoveredAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *ServiceConnectionStore) ListResources(ctx context.Context, tenantID contracts.TenantID, connectionID string) ([]serviceconnection.ServiceConnectionResource, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, connection_id, resource_id, resource_type, name,
  schema_json, metadata_json, discovered_at
FROM service_connection_resources
WHERE tenant_id=$1 AND connection_id=$2
ORDER BY resource_type, name`, tenantID, connectionID)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]serviceconnection.ServiceConnectionResource, 0)
	for rows.Next() {
		resource, err := scanServiceConnectionResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, resource)
	}
	return out, rows.Err()
}

func (s *ServiceConnectionStore) AppendHealthEvent(ctx context.Context, event serviceconnection.ServiceConnectionHealthEvent) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO service_connection_health_events (
  tenant_id, connection_id, health_status, error, latency_ms, checked_at
)
VALUES ($1,$2,$3,$4,$5,$6)`,
		event.TenantID, event.ConnectionID, event.HealthStatus, nullString(event.Error), event.LatencyMS, event.CheckedAt,
	)
	return err
}

func (s *ServiceConnectionStore) ListHealthEvents(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]serviceconnection.ServiceConnectionHealthEvent, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, connection_id, health_status, error, latency_ms, checked_at
FROM service_connection_health_events
WHERE tenant_id=$1 AND connection_id=$2
ORDER BY checked_at DESC, id DESC
LIMIT $3`, tenantID, connectionID, limit)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]serviceconnection.ServiceConnectionHealthEvent, 0)
	for rows.Next() {
		event, err := scanServiceConnectionHealthEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *ServiceConnectionStore) AppendSecretRotation(ctx context.Context, rotation serviceconnection.ServiceConnectionSecretRotation) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO service_connection_secret_rotations (
  rotation_id, tenant_id, connection_id, auth_type, previous_auth_ref_hash,
  new_auth_ref_hash, reason, rotated_by, rotated_at
)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (rotation_id) DO NOTHING`,
		rotation.RotationID, rotation.TenantID, rotation.ConnectionID, nullString(rotation.AuthType), nullString(rotation.PreviousAuthRefHash),
		rotation.NewAuthRefHash, nullString(rotation.Reason), nullString(rotation.RotatedBy), rotation.RotatedAt,
	)
	return err
}

func (s *ServiceConnectionStore) ListSecretRotations(ctx context.Context, tenantID contracts.TenantID, connectionID string, limit int) ([]serviceconnection.ServiceConnectionSecretRotation, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT tenant_id, connection_id, rotation_id, auth_type, previous_auth_ref_hash,
  new_auth_ref_hash, reason, rotated_by, rotated_at
FROM service_connection_secret_rotations
WHERE tenant_id=$1 AND connection_id=$2
ORDER BY rotated_at DESC, rotation_id DESC
LIMIT $3`, tenantID, connectionID, limit)
	if err != nil {
		if isSchemaNotReadyError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make([]serviceconnection.ServiceConnectionSecretRotation, 0)
	for rows.Next() {
		rotation, err := scanServiceConnectionSecretRotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rotation)
	}
	return out, rows.Err()
}

func scanServiceConnection(row interface {
	Scan(dest ...any) error
}) (serviceconnection.ServiceConnection, error) {
	var connection serviceconnection.ServiceConnection
	var tenantID, description, baseURL, authType, authRef, networkScope, lastHealthError sql.NullString
	var lastHealthAt sql.NullTime
	var metadata []byte
	if err := row.Scan(
		&tenantID, &connection.ConnectionID, &connection.ConnectionType, &connection.Name, &connection.Environment, &connection.Status, &connection.HealthStatus,
		&description, &baseURL, &authType, &authRef, &networkScope, &connection.TimeoutMS,
		&connection.RetryMax, &connection.HealthCheckEnabled, &lastHealthAt, &lastHealthError,
		&metadata, &connection.Version,
	); err != nil {
		return serviceconnection.ServiceConnection{}, mapSQLError(err)
	}
	connection.TenantID = contracts.TenantID(tenantID.String)
	connection.Description = description.String
	connection.BaseURL = baseURL.String
	connection.AuthType = authType.String
	connection.AuthRef = authRef.String
	connection.NetworkScope = networkScope.String
	connection.LastHealthAt = timePtr(lastHealthAt)
	connection.LastHealthError = lastHealthError.String
	connection.Metadata = map[string]any{}
	_ = scanJSON(metadata, &connection.Metadata)
	return connection, nil
}

func scanServiceConnectionResource(row interface {
	Scan(dest ...any) error
}) (serviceconnection.ServiceConnectionResource, error) {
	var resource serviceconnection.ServiceConnectionResource
	var tenantID string
	var schemaJSON, metadata []byte
	if err := row.Scan(
		&tenantID, &resource.ConnectionID, &resource.ResourceID, &resource.ResourceType, &resource.Name,
		&schemaJSON, &metadata, &resource.DiscoveredAt,
	); err != nil {
		return serviceconnection.ServiceConnectionResource{}, mapSQLError(err)
	}
	resource.TenantID = contracts.TenantID(tenantID)
	resource.Schema = map[string]any{}
	resource.Metadata = map[string]any{}
	_ = scanJSON(schemaJSON, &resource.Schema)
	_ = scanJSON(metadata, &resource.Metadata)
	return resource, nil
}

func scanServiceConnectionHealthEvent(row interface {
	Scan(dest ...any) error
}) (serviceconnection.ServiceConnectionHealthEvent, error) {
	var event serviceconnection.ServiceConnectionHealthEvent
	var tenantID, lastError sql.NullString
	if err := row.Scan(
		&tenantID, &event.ConnectionID, &event.HealthStatus, &lastError, &event.LatencyMS, &event.CheckedAt,
	); err != nil {
		return serviceconnection.ServiceConnectionHealthEvent{}, mapSQLError(err)
	}
	event.TenantID = contracts.TenantID(tenantID.String)
	event.Error = lastError.String
	event.CheckedAt = event.CheckedAt.UTC()
	return event, nil
}

func scanServiceConnectionSecretRotation(row interface {
	Scan(dest ...any) error
}) (serviceconnection.ServiceConnectionSecretRotation, error) {
	var rotation serviceconnection.ServiceConnectionSecretRotation
	var tenantID, authType, previousHash, reason, rotatedBy sql.NullString
	if err := row.Scan(
		&tenantID, &rotation.ConnectionID, &rotation.RotationID, &authType, &previousHash,
		&rotation.NewAuthRefHash, &reason, &rotatedBy, &rotation.RotatedAt,
	); err != nil {
		return serviceconnection.ServiceConnectionSecretRotation{}, mapSQLError(err)
	}
	rotation.TenantID = contracts.TenantID(tenantID.String)
	rotation.AuthType = authType.String
	rotation.PreviousAuthRefHash = previousHash.String
	rotation.Reason = reason.String
	rotation.RotatedBy = rotatedBy.String
	rotation.RotatedAt = rotation.RotatedAt.UTC()
	return rotation, nil
}
