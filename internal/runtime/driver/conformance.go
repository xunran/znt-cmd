package driver

import "znt/internal/contracts"

type ConformanceReport struct {
	CarrierKind     contracts.AgentCarrierKind         `json:"carrier_kind"`
	RuntimeContract contracts.RuntimeContractKind      `json:"runtime_contract"`
	Status          contracts.RuntimeConformanceStatus `json:"status"`
	Checks          map[string]bool                    `json:"checks,omitempty"`
}
