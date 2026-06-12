param(
    [switch]$StrictTestCoverage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$root = Get-RepoRoot
$failures = New-Object System.Collections.Generic.List[string]
$warnings = New-Object System.Collections.Generic.List[string]

function Add-Failure {
    param([string]$Message)
    $failures.Add($Message) | Out-Null
}

function Add-Warning {
    param([string]$Message)
    $warnings.Add($Message) | Out-Null
}

function Read-JsonFile {
    param([string]$Path)
    return Get-Content $Path -Raw | ConvertFrom-Json
}

$serverPath = Join-Path $root "internal\server\server.go"
$openAPIPath = Join-Path $root "docs\openapi.clean-core.v1.json"
$configPath = Join-Path $root "config.example.json"
$composePath = Join-Path $root "docker-compose.yml"
$helmValuesPath = Join-Path $root "deploy\helm\clean-core\values.yaml"
$helmConfigMapPath = Join-Path $root "deploy\helm\clean-core\templates\configmap.yaml"
$helmSecretPath = Join-Path $root "deploy\helm\clean-core\templates\secret.yaml"
$serverTestPath = Join-Path $root "internal\server\server_test.go"

$server = Get-Content $serverPath -Raw
$openapi = Read-JsonFile $openAPIPath
$config = Read-JsonFile $configPath
$serverTests = Get-Content $serverTestPath -Raw

$allowedBody = [regex]::Match($server, 'func allowedCommand[\s\S]*?default:').Value
$allowedCommands = [regex]::Matches($allowedBody, '"([a-z0-9_.]+)"') |
    ForEach-Object { $_.Groups[1].Value } |
    Sort-Object -Unique
$openAPICommands = @($openapi.components.schemas.AgentEnvelope.properties.command.enum) | Sort-Object -Unique

$missingFromOpenAPI = @($allowedCommands | Where-Object { $_ -notin $openAPICommands })
$missingFromServer = @($openAPICommands | Where-Object { $_ -notin $allowedCommands })

foreach ($command in $missingFromOpenAPI) {
    Add-Failure "server command '$command' is missing from OpenAPI AgentEnvelope command enum"
}
foreach ($command in $missingFromServer) {
    Add-Failure "OpenAPI command '$command' is missing from server allowedCommand"
}

$dispatchBody = [regex]::Match($server, 'func dispatchCommand[\s\S]*?default:').Value
$dispatchCommands = [regex]::Matches($dispatchBody, '"([a-z0-9_.]+)"') |
    ForEach-Object { $_.Groups[1].Value } |
    Sort-Object -Unique
foreach ($command in $allowedCommands) {
    if ($command -notin $dispatchCommands) {
        Add-Failure "allowed command '$command' is not handled by dispatchCommand"
    }
    if ($serverTests -notmatch [regex]::Escape($command)) {
        Add-Warning "server command '$command' has no direct string evidence in internal/server/server_test.go"
    }
}

$configMap = @{
    service_name = "CLEAN_CORE_SERVICE_NAME"
    version = "CLEAN_CORE_VERSION"
    env = "CLEAN_CORE_ENV"
    http_addr = "CLEAN_CORE_HTTP_ADDR"
    log_level = "CLEAN_CORE_LOG_LEVEL"
    database_url = "CLEAN_CORE_DATABASE_URL"
    readiness = "CLEAN_CORE_READINESS"
    service_token = "CLEAN_CORE_SERVICE_TOKEN"
    model_provider = "CLEAN_CORE_MODEL_PROVIDER"
    model_base_url = "CLEAN_CORE_MODEL_BASE_URL"
    model_api_key = "CLEAN_CORE_MODEL_API_KEY"
    model_name = "CLEAN_CORE_MODEL_NAME"
    model_max_tokens = "CLEAN_CORE_MODEL_MAX_TOKENS"
    model_temperature = "CLEAN_CORE_MODEL_TEMPERATURE"
    model_thinking = "CLEAN_CORE_MODEL_THINKING"
    model_reasoning_effort = "CLEAN_CORE_MODEL_REASONING_EFFORT"
    external_bridge_base_url = "CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL"
    external_bridge_token = "CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN"
    disabled_agent_ids = "CLEAN_CORE_DISABLED_AGENT_IDS"
    disabled_tool_ids = "CLEAN_CORE_DISABLED_TOOL_IDS"
    disable_handoff = "CLEAN_CORE_DISABLE_HANDOFF"
    disable_external_tools_invoke = "CLEAN_CORE_DISABLE_EXTERNAL_TOOLS_INVOKE"
}

$configText = Get-Content $configPath -Raw
$configGo = Get-Content (Join-Path $root "internal\app\config\config.go") -Raw
$composeText = Get-Content $composePath -Raw
$helmValuesText = Get-Content $helmValuesPath -Raw
$helmConfigMapText = Get-Content $helmConfigMapPath -Raw
$helmSecretText = Get-Content $helmSecretPath -Raw

foreach ($entry in $configMap.GetEnumerator()) {
    $jsonKey = $entry.Key
    $envKey = $entry.Value
    if (-not ($config.PSObject.Properties.Name -contains $jsonKey)) {
        Add-Failure "config.example.json is missing '$jsonKey'"
    }
    if ($configGo -notmatch [regex]::Escape($envKey)) {
        Add-Failure "config.go does not read env '$envKey'"
    }
}

$deploymentEnvText = $composeText + "`n" + $helmValuesText + "`n" + $helmConfigMapText + "`n" + $helmSecretText
$deploymentRequiredEnv = @(
    "CLEAN_CORE_ENV",
    "CLEAN_CORE_HTTP_ADDR",
    "CLEAN_CORE_LOG_LEVEL",
    "CLEAN_CORE_SERVICE_TOKEN",
    "CLEAN_CORE_DATABASE_URL",
    "CLEAN_CORE_MODEL_PROVIDER",
    "CLEAN_CORE_MODEL_BASE_URL",
    "CLEAN_CORE_MODEL_API_KEY",
    "CLEAN_CORE_MODEL_NAME",
    "CLEAN_CORE_MODEL_MAX_TOKENS",
    "CLEAN_CORE_MODEL_TEMPERATURE",
    "CLEAN_CORE_MODEL_THINKING",
    "CLEAN_CORE_MODEL_REASONING_EFFORT",
    "CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL",
    "CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN"
)
foreach ($envKey in $deploymentRequiredEnv) {
    if ($deploymentEnvText -notmatch [regex]::Escape($envKey)) {
        Add-Failure "deployment config is missing '$envKey'"
    }
}

$requiredPaths = @(
    "/healthz",
    "/readyz",
    "/v1/commands",
    "/v1/agents",
    "/v1/agents/{agent_id}",
    "/v1/agents/{agent_id}/drafts",
    "/v1/agents/{agent_id}/drafts/{draft_id}",
    "/v1/agents/{agent_id}/drafts/{draft_id}/validate",
    "/v1/agents/{agent_id}/drafts/{draft_id}/review",
    "/v1/agents/{agent_id}/drafts/{draft_id}/publish",
    "/v1/agents/{agent_id}/versions",
    "/v1/agents/{agent_id}/versions/{version}",
    "/v1/agents/{agent_id}/versions/{version}/activate",
    "/v1/agents/{agent_id}/runtime-hooks",
    "/v1/agents/{agent_id}/prompt-profile",
    "/v1/agents/{agent_id}/prompt-profile/governance",
    "/v1/agents/{agent_id}/prompt-profile/versions",
    "/v1/agents/{agent_id}/prompt-profile/activate",
    "/v1/agents/{agent_id}/tool-bindings",
    "/v1/agents/{agent_id}/tool-bindings/governance",
    "/v1/agents/{agent_id}/tool-bindings/versions",
    "/v1/agents/{agent_id}/tool-bindings/activate",
    "/v1/agents/{agent_id}/skills",
    "/v1/agents/{agent_id}/skills/governance",
    "/v1/agents/{agent_id}/skills/{skill_id}",
    "/v1/agents/{agent_id}/skills/{skill_id}/governance",
    "/v1/agents/{agent_id}/skills/{skill_id}/versions",
    "/v1/agents/{agent_id}/skills/{skill_id}/activate",
    "/v1/agents/{agent_id}/collaborators",
    "/v1/agents/{agent_id}/collaborators/governance",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/governance",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/versions",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/activate",
    "/v1/agents/{agent_id}/exported-tools",
    "/v1/agents/{agent_id}/exported-tools/governance",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/governance",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/versions",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/activate",
    "/v1/tool-providers",
    "/v1/tool-provider-governance",
    "/v1/tool-providers/{provider_id}/health",
    "/v1/tool-providers/{provider_id}/operations",
    "/v1/tool-providers/{provider_id}/operations/{operation_id}",
    "/v1/tool-providers/{provider_id}/operations/{operation_id}/publish",
    "/v1/tool-providers/{provider_id}/operations/{operation_id}/test",
    "/v1/service-connections",
    "/v1/service-connections/templates",
    "/v1/service-connections/{connection_id}",
    "/v1/service-connections/{connection_id}/test",
    "/v1/service-connections/{connection_id}/enable",
    "/v1/service-connections/{connection_id}/disable",
    "/v1/service-connections/{connection_id}/resources",
    "/v1/service-connections/{connection_id}/health-events",
    "/v1/service-connections/{connection_id}/impact",
    "/v1/service-connections/{connection_id}/usage",
    "/v1/service-connections/{connection_id}/secret-rotations",
    "/v1/tool-groups",
    "/v1/tool-manifests",
    "/v1/tool-manifests/{tool_id}",
    "/v1/runtime-hook-providers",
    "/v1/runtime-hook-providers/{provider_id}/catalog",
    "/v1/runtime-hook-providers/{provider_id}/catalog/sync",
    "/v1/runtime-hook-providers/{provider_id}/health",
    "/v1/runtime-hook-manifests",
    "/v1/runtime-hook-manifests/{hook_id}",
    "/v1/runtime-hook-manifests/{hook_id}/versions",
    "/v1/runtime-hook-manifests/{hook_id}/versions/{version}",
    "/v1/runtime-hook-manifests/{hook_id}/versions/{version}/activate",
    "/v1/runtime-hook-events",
    "/v1/runtime-hook-governance",
    "/v1/runtime-hook-approvals",
    "/v1/knowledge-bases",
    "/v1/knowledge-bases/{knowledge_base_id}",
    "/v1/knowledge-bases/{knowledge_base_id}/documents",
    "/v1/knowledge-bases/{knowledge_base_id}/index-jobs",
    "/v1/knowledge-bases/{knowledge_base_id}/index-jobs/{job_id}",
    "/v1/knowledge-search",
    "/v1/cross-group-share-policies",
    "/v1/cross-group-share-policies/{policy_id}",
    "/v1/cross-groups/search",
    "/v1/tasks/start",
    "/v1/readiness/report",
    "/v1/release/go-no-go",
    "/v1/usage/evidence",
    "/v1/agent-packages/canary-hits",
    "/v1/evals/results/{eval_run_id}",
    "/v1/traces/{trace_id}",
    "/v1/traces/{trace_id}/replay",
    "/v1/handoffs/{handoff_id}",
    "/v1/handoffs/{handoff_id}/trace"
)
foreach ($path in $requiredPaths) {
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $path)) {
        Add-Failure "OpenAPI is missing path '$path'"
    }
}

function Assert-PathQueryParameters {
    param(
        [string]$Path,
        [string]$Method,
        [string[]]$Names
    )
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $Path)) {
        return
    }
    $pathItem = $openapi.paths.PSObject.Properties[$Path].Value
    if (-not ($pathItem.PSObject.Properties.Name -contains $Method)) {
        Add-Failure "OpenAPI path '$Path' is missing method '$Method'"
        return
    }
    $operation = $pathItem.$Method
    $parameterNames = @()
    if ($operation.PSObject.Properties.Name -contains "parameters") {
        $parameterNames = @($operation.parameters | ForEach-Object { $_.name })
    }
    foreach ($name in $Names) {
        if ($name -notin $parameterNames) {
            Add-Failure "OpenAPI $Method $Path is missing query parameter '$name'"
        }
    }
}

function Assert-PathMethods {
    param(
        [string]$Path,
        [string[]]$Methods
    )
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $Path)) {
        return
    }
    $pathItem = $openapi.paths.PSObject.Properties[$Path].Value
    foreach ($method in $Methods) {
        if (-not ($pathItem.PSObject.Properties.Name -contains $method)) {
            Add-Failure "OpenAPI path '$Path' is missing method '$method'"
        }
    }
}

function Assert-PathOperationDescriptionContains {
    param(
        [string]$Path,
        [string]$Method,
        [string]$ExpectedText
    )
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $Path)) {
        Add-Failure "OpenAPI path '$Path' is missing"
        return
    }
    $pathItem = $openapi.paths.PSObject.Properties[$Path].Value
    if (-not ($pathItem.PSObject.Properties.Name -contains $Method)) {
        Add-Failure "OpenAPI path '$Path' is missing method '$Method'"
        return
    }
    $operation = $pathItem.PSObject.Properties[$Method].Value
    if (-not ($operation.PSObject.Properties.Name -contains "description") -or [string]$operation.description -notmatch [regex]::Escape($ExpectedText)) {
        Add-Failure "OpenAPI operation '$Method $Path' description must mention '$ExpectedText'"
    }
}

function Assert-PathRequestSchemaRef {
    param(
        [string]$Path,
        [string]$Method,
        [string]$ExpectedRef
    )
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $Path)) {
        Add-Failure "OpenAPI path '$Path' is missing"
        return
    }
    $pathItem = $openapi.paths.PSObject.Properties[$Path].Value
    if (-not ($pathItem.PSObject.Properties.Name -contains $Method)) {
        Add-Failure "OpenAPI path '$Path' is missing method '$Method'"
        return
    }
    $operation = $pathItem.PSObject.Properties[$Method].Value
    if (-not ($operation.PSObject.Properties.Name -contains "requestBody")) {
        Add-Failure "OpenAPI operation '$Method $Path' is missing requestBody"
        return
    }
    $schema = $operation.requestBody.content.'application/json'.schema
    $actual = $schema.'$ref'
    if ($actual -ne $ExpectedRef) {
        Add-Failure "OpenAPI operation '$Method $Path' request schema must be '$ExpectedRef', got '$actual'"
    }
}

function Assert-SchemaPropertyEnum {
    param(
        [string]$SchemaName,
        [string]$PropertyName,
        [string[]]$Values
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        Add-Failure "OpenAPI is missing schema '$SchemaName'"
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (-not ($schema.properties.PSObject.Properties.Name -contains $PropertyName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing property '$PropertyName'"
        return
    }
    $property = $schema.properties.PSObject.Properties[$PropertyName].Value
    $actual = @($property.enum)
    foreach ($value in $Values) {
        if ($value -notin $actual) {
            Add-Failure "OpenAPI schema '$SchemaName.$PropertyName' enum is missing '$value'"
        }
    }
}

function Assert-SchemaPropertyEnumExactly {
    param(
        [string]$SchemaName,
        [string]$PropertyName,
        [string[]]$Values
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (-not ($schema.properties.PSObject.Properties.Name -contains $PropertyName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing property '$PropertyName'"
        return
    }
    $property = $schema.properties.PSObject.Properties[$PropertyName].Value
    $actual = @($property.enum)
    foreach ($value in $Values) {
        if ($value -notin $actual) {
            Add-Failure "OpenAPI schema '$SchemaName.$PropertyName' enum is missing '$value'"
        }
    }
    foreach ($value in $actual) {
        if ($value -notin $Values) {
            Add-Failure "OpenAPI schema '$SchemaName.$PropertyName' enum must not include '$value'"
        }
    }
}

function Assert-SchemaLacksProperties {
    param(
        [string]$SchemaName,
        [string[]]$PropertyNames
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (-not ($schema.PSObject.Properties.Name -contains "properties")) {
        return
    }
    foreach ($name in $PropertyNames) {
        if ($schema.properties.PSObject.Properties.Name -contains $name) {
            Add-Failure "OpenAPI schema '$SchemaName' must not expose legacy property '$name'"
        }
    }
}

function Assert-SchemaHasProperties {
    param(
        [string]$SchemaName,
        [string[]]$PropertyNames
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing"
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (-not ($schema.PSObject.Properties.Name -contains "properties")) {
        Add-Failure "OpenAPI schema '$SchemaName' has no properties"
        return
    }
    foreach ($name in $PropertyNames) {
        if (-not ($schema.properties.PSObject.Properties.Name -contains $name)) {
            Add-Failure "OpenAPI schema '$SchemaName' is missing property '$name'"
        }
    }
}

function Assert-SchemaPropertyDescriptionContains {
    param(
        [string]$SchemaName,
        [string]$PropertyName,
        [string]$ExpectedText
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing"
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (-not ($schema.PSObject.Properties.Name -contains "properties") -or -not ($schema.properties.PSObject.Properties.Name -contains $PropertyName)) {
        Add-Failure "OpenAPI schema '$SchemaName.$PropertyName' is missing"
        return
    }
    $property = $schema.properties.PSObject.Properties[$PropertyName].Value
    if (-not ($property.PSObject.Properties.Name -contains "description") -or [string]$property.description -notmatch [regex]::Escape($ExpectedText)) {
        Add-Failure "OpenAPI schema '$SchemaName.$PropertyName' description must mention '$ExpectedText'"
    }
}

function Assert-SchemaRequiresProperties {
    param(
        [string]$SchemaName,
        [string[]]$PropertyNames
    )
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing"
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    $actual = @($schema.required)
    foreach ($name in $PropertyNames) {
        if ($name -notin $actual) {
            Add-Failure "OpenAPI schema '$SchemaName' required list is missing '$name'"
        }
    }
}

function Assert-SchemaHasNoRequired {
    param([string]$SchemaName)
    $schemas = $openapi.components.schemas
    if (-not ($schemas.PSObject.Properties.Name -contains $SchemaName)) {
        Add-Failure "OpenAPI schema '$SchemaName' is missing"
        return
    }
    $schema = $schemas.PSObject.Properties[$SchemaName].Value
    if (($schema.PSObject.Properties.Name -contains "required") -and @($schema.required).Count -gt 0) {
        Add-Failure "OpenAPI schema '$SchemaName' must not require fields because it is a PATCH request schema"
    }
}

Assert-PathQueryParameters "/v1/tool-providers" "get" @("q", "provider_type", "status", "health_status", "include_managed", "page_size", "cursor")
Assert-PathQueryParameters "/v1/tool-manifests" "get" @("q", "provider_id", "executor_type", "status", "risk_level", "visibility", "page_size", "cursor")
Assert-PathQueryParameters "/v1/service-connections" "get" @("q", "connection_type", "status", "health_status", "environment", "page_size", "cursor")
Assert-PathQueryParameters "/v1/service-connections/{connection_id}/usage" "get" @("trace_id", "limit", "from", "to")
Assert-PathMethods "/v1/service-connections/{connection_id}" @("get", "put", "patch", "delete")
Assert-PathMethods "/v1/tool-providers/{provider_id}" @("get", "put", "patch", "delete")
Assert-PathMethods "/v1/tool-groups/{group_id}" @("get", "put", "patch", "delete")
Assert-PathMethods "/v1/tool-manifests/{tool_id}" @("get", "put", "patch", "delete")
Assert-PathMethods "/v1/tool-providers/{provider_id}/operations/{operation_id}" @("get", "put", "patch")
Assert-PathMethods "/v1/tool-providers/{provider_id}/operations/from-resource" @("post")
Assert-PathOperationDescriptionContains "/v1/service-connections/{connection_id}" "put" "Full replacement"
Assert-PathOperationDescriptionContains "/v1/service-connections/{connection_id}" "patch" "preserves omitted fields"
Assert-PathOperationDescriptionContains "/v1/service-connections/{connection_id}" "patch" "auth_type and auth_ref must be patched together"
Assert-PathOperationDescriptionContains "/v1/tool-providers" "get" "external tool sources by default"
Assert-PathOperationDescriptionContains "/v1/tool-providers" "get" "include_managed=true"
Assert-PathOperationDescriptionContains "/v1/tool-providers/{provider_id}" "patch" "preserves omitted fields"
Assert-PathOperationDescriptionContains "/v1/tool-providers/{provider_id}" "patch" "provider_id"
Assert-PathOperationDescriptionContains "/v1/tool-groups/{group_id}" "patch" "preserves omitted fields"
Assert-PathOperationDescriptionContains "/v1/tool-groups/{group_id}" "patch" "group_id"
Assert-PathOperationDescriptionContains "/v1/tool-manifests/{tool_id}" "patch" "preserves omitted fields"
Assert-PathOperationDescriptionContains "/v1/tool-manifests/{tool_id}" "patch" "tool_id"
Assert-PathOperationDescriptionContains "/v1/tool-providers/{provider_id}/operations/{operation_id}" "patch" "preserves omitted fields"
Assert-PathOperationDescriptionContains "/v1/tool-providers/{provider_id}/operations/{operation_id}" "patch" "operation_id"
Assert-PathRequestSchemaRef "/v1/service-connections/{connection_id}" "patch" "#/components/schemas/ServiceConnectionPatchRequest"
Assert-PathRequestSchemaRef "/v1/tool-providers/{provider_id}" "patch" "#/components/schemas/ToolProviderPatchRequest"
Assert-PathRequestSchemaRef "/v1/tool-groups/{group_id}" "patch" "#/components/schemas/ToolGroupPatchRequest"
Assert-PathRequestSchemaRef "/v1/tool-manifests/{tool_id}" "patch" "#/components/schemas/ToolManifestPatchRequest"
Assert-PathRequestSchemaRef "/v1/tool-providers/{provider_id}/operations/{operation_id}" "patch" "#/components/schemas/AdapterOperationPatchRequest"
Assert-PathRequestSchemaRef "/v1/tool-providers/{provider_id}/operations/from-resource" "post" "#/components/schemas/AdapterOperationFromResourceRequest"
Assert-SchemaHasNoRequired "ServiceConnectionPatchRequest"
Assert-SchemaHasNoRequired "ToolProviderPatchRequest"
Assert-SchemaHasNoRequired "ToolGroupPatchRequest"
Assert-SchemaHasNoRequired "ToolManifestPatchRequest"
Assert-SchemaHasNoRequired "AdapterOperationPatchRequest"
Assert-SchemaPropertyEnum "ToolProvider" "provider_type" @("static_tool_host", "agent_plugin_service", "mcp", "http_api_adapter", "database_adapter")
Assert-SchemaPropertyEnumExactly "ToolProviderUpsertRequest" "provider_type" @("static_tool_host", "agent_plugin_service", "mcp")
Assert-SchemaPropertyEnumExactly "ToolGroup" "status" @("draft", "enabled", "disabled")
Assert-SchemaPropertyEnum "ToolExecutorSpec" "type" @("static_tool_host", "agent_plugin_service", "mcp", "agent_tool", "http_api_adapter", "database_adapter")
Assert-SchemaHasProperties "ToolProviderGovernanceSummary" @("agent_plugin_service_tools_total")
Assert-SchemaHasProperties "ServiceConnectionUsage" @("trace_id", "from", "to", "summary", "providers", "tools", "recent_events")
Assert-SchemaPropertyEnum "ServiceConnection" "auth_type" @("none", "api_key", "bearer", "basic", "oauth2", "signed_request", "mtls")
Assert-SchemaPropertyEnum "ServiceConnectionSecretRotationRequest" "auth_type" @("api_key", "bearer", "basic", "oauth2", "signed_request", "mtls")
Assert-SchemaPropertyEnum "ServiceConnectionSecretRotation" "auth_type" @("api_key", "bearer", "basic", "oauth2", "signed_request", "mtls")
Assert-SchemaLacksProperties "ToolExecutorSpec" @("url", "method", "headers")
Assert-SchemaLacksProperties "ToolManifest" @("manifest", "tool", "executor_type", "provider_id", "operation")
Assert-SchemaLacksProperties "ToolManifestPatchRequest" @("manifest", "tool", "executor_type", "provider_id", "operation")
Assert-SchemaLacksProperties "ToolProvider" @("provider", "endpoint", "endpoint_ref", "auth_ref", "secret_ref", "token_ref")
Assert-SchemaLacksProperties "ToolProviderUpsertRequest" @("provider", "endpoint", "endpoint_ref", "auth_ref", "secret_ref", "token_ref", "health_status", "last_health_check_at", "last_health_error")
Assert-SchemaLacksProperties "ToolProviderPatchRequest" @("provider", "endpoint", "endpoint_ref", "auth_ref", "secret_ref", "token_ref", "health_status", "last_health_check_at", "last_health_error")
Assert-SchemaLacksProperties "ToolGroup" @("group", "tool_ids", "metadata")
Assert-SchemaLacksProperties "ToolGroupUpsertRequest" @("group", "tool_ids", "metadata")
Assert-SchemaLacksProperties "ToolGroupPatchRequest" @("group", "tool_ids", "metadata")
Assert-SchemaLacksProperties "AdapterOperation" @("operation")
Assert-SchemaLacksProperties "AdapterOperationPatchRequest" @("operation")
Assert-SchemaLacksProperties "ServiceConnectionUpsertRequest" @("connection", "secret_ref", "token_ref", "health_status", "last_health_at", "last_health_error")
Assert-SchemaLacksProperties "ServiceConnectionPatchRequest" @("connection", "secret_ref", "token_ref", "health_status", "last_health_at", "last_health_error")
Assert-SchemaPropertyDescriptionContains "ServiceConnection" "auth_type" "auth_ref"
Assert-SchemaPropertyDescriptionContains "ServiceConnection" "auth_ref" "Plain secret values must not be submitted"
Assert-SchemaPropertyDescriptionContains "ServiceConnection" "connection_type" "model-only"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionUpsertRequest" "connection_type" "not implemented"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionPatchRequest" "connection_type" "not implemented"
Assert-SchemaPropertyDescriptionContains "ServiceConnection" "metadata" "metadata.openapi_path"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionUpsertRequest" "metadata" "metadata.openapi_path"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionPatchRequest" "metadata" "metadata.openapi_path"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionResource" "resource_type" "http_operation"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionResource" "resource_type" "table/view"
Assert-SchemaPropertyDescriptionContains "ServiceConnectionSecretRotationRequest" "auth_type" "must not be none"
Assert-SchemaRequiresProperties "ServiceConnectionSecretRotationRequest" @("auth_ref", "auth_type")
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "method" "HTTP API adapter only"
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "request_mapping" "HTTP API adapter only"
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "request_mapping" "path_params"
Assert-SchemaPropertyDescriptionContains "AdapterOperationPatchRequest" "request_mapping" "headers"
Assert-SchemaRequiresProperties "AdapterOperationFromResourceRequest" @("service_connection_id", "resource_id")
Assert-SchemaPropertyDescriptionContains "AdapterOperationFromResourceRequest" "resource_id" "ServiceConnectionResource"
Assert-SchemaPropertyDescriptionContains "AdapterOperationFromResourceRequest" "service_connection_id" "managed_database_adapter"
Assert-SchemaPropertyDescriptionContains "AdapterOperationFromResourceRequest" "query_template" "Database adapter only"
Assert-SchemaPropertyDescriptionContains "AdapterOperationFromResourceRequest" "redact_columns" "discovered resource column schema"
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "resource_id" "Database adapter only"
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "query_template" "Database adapter only"
Assert-SchemaPropertyDescriptionContains "AdapterOperation" "read_only" "Database adapter only"

if ($StrictTestCoverage) {
    foreach ($warning in $warnings) {
        Add-Failure $warning
    }
    $warnings.Clear()
}

$report = [pscustomobject]@{
    status = if ($failures.Count -eq 0) { "passed" } else { "failed" }
    command_count = $allowedCommands.Count
    warnings = @($warnings)
    failures = @($failures)
}

$reportDir = New-E2EReportDir -Name "contract"
Write-E2EJson -Path (Join-Path $reportDir "contract-report.json") -Value $report

if ($warnings.Count -gt 0) {
    Write-Host "Contract warnings:"
    foreach ($warning in $warnings) {
        Write-Host "WARN: $warning"
    }
}
if ($failures.Count -gt 0) {
    Write-Host "Contract failures:"
    foreach ($failure in $failures) {
        Write-Host "FAIL: $failure"
    }
    Write-Host "Report: $reportDir"
    exit 1
}

Write-Host "Contract verification passed. commands=$($allowedCommands.Count) report=$reportDir"
