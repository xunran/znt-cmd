package contracts

type AgentSourceKind string

const (
	AgentSourceKindPackage AgentSourceKind = "package"
	AgentSourceKindPlugin  AgentSourceKind = "plugin_service"
)
