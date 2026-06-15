package contracts

type AgentSourceKind string

const (
	AgentSourceKindPackage AgentSourceKind = "package"
	AgentSourceKindPlugin  AgentSourceKind = "plugin_service"
)

type AgentCarrierKind string

const (
	AgentCarrierKindNativeAgent       AgentCarrierKind = "native_agent"
	AgentCarrierKindAgentPluginSource AgentCarrierKind = "agent_plugin_source"
	AgentCarrierKindWorkflowGraph     AgentCarrierKind = "workflow_graph"
	AgentCarrierKindExternalRuntime   AgentCarrierKind = "external_runtime"
)

type RuntimeContractKind string

const (
	RuntimeContractManaged   RuntimeContractKind = "managed"
	RuntimeContractConnected RuntimeContractKind = "connected"
	RuntimeContractObserved  RuntimeContractKind = "observed"
)

type RuntimeConformanceStatus string

const (
	RuntimeConformanceUnknown RuntimeConformanceStatus = "unknown"
	RuntimeConformancePassed  RuntimeConformanceStatus = "passed"
	RuntimeConformanceFailed  RuntimeConformanceStatus = "failed"
)

func CarrierKindForSource(kind AgentSourceKind) AgentCarrierKind {
	switch kind {
	case AgentSourceKindPlugin:
		return AgentCarrierKindAgentPluginSource
	default:
		return AgentCarrierKindNativeAgent
	}
}

func DefaultRuntimeContractForCarrier(kind AgentCarrierKind) RuntimeContractKind {
	switch kind {
	case AgentCarrierKindNativeAgent, AgentCarrierKindAgentPluginSource:
		return RuntimeContractManaged
	case AgentCarrierKindWorkflowGraph:
		return RuntimeContractManaged
	case AgentCarrierKindExternalRuntime:
		return RuntimeContractConnected
	default:
		return ""
	}
}

func NormalizeSourceKind(kind AgentSourceKind) AgentSourceKind {
	if kind == "" {
		return AgentSourceKindPackage
	}
	return kind
}

func NormalizeCarrierKind(sourceKind AgentSourceKind, carrierKind AgentCarrierKind) AgentCarrierKind {
	if carrierKind != "" {
		return carrierKind
	}
	return CarrierKindForSource(NormalizeSourceKind(sourceKind))
}

func NormalizeRuntimeContract(carrierKind AgentCarrierKind, contract RuntimeContractKind) RuntimeContractKind {
	if contract != "" {
		return contract
	}
	if carrierKind == "" {
		carrierKind = AgentCarrierKindNativeAgent
	}
	return DefaultRuntimeContractForCarrier(carrierKind)
}
