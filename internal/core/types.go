package core

import (
	"encoding/json"
	"time"
)

const ContractVersion = "0.2.0"

type SourceItem struct {
	ID          string                     `json:"id"`
	URI         string                     `json:"uri"`
	MediaType   string                     `json:"media_type,omitempty"`
	ContentHash string                     `json:"content_hash,omitempty"`
	Metadata    map[string]json.RawMessage `json:"metadata,omitempty"`
}

type InputSnapshot struct {
	SchemaVersion string       `json:"schema_version"`
	ID            string       `json:"id"`
	CreatedAt     time.Time    `json:"created_at"`
	Items         []SourceItem `json:"items"`
}

type AgentRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type SkillRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ToolRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// AgentDefinition separates an agent's own prompt and capabilities from the
// workflow task that invokes it.
type AgentDefinition struct {
	ID            string     `json:"id"`
	Version       string     `json:"version"`
	Description   string     `json:"description,omitempty"`
	PromptVersion string     `json:"prompt_version"`
	SystemPrompt  string     `json:"system_prompt"`
	Skills        []SkillRef `json:"skills,omitempty"`
	Tools         []ToolRef  `json:"tools,omitempty"`
	CanDelegate   bool       `json:"can_delegate,omitempty"`
}

type SkillDefinition struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Instructions string `json:"instructions"`
}

type ToolDefinition struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type DelegationPolicy struct {
	AllowedAgents []AgentRef `json:"allowed_agents,omitempty"`
	MaxDepth      int        `json:"max_depth"`
	MaxAgentCalls int        `json:"max_agent_calls"`
	MaxToolCalls  int        `json:"max_tool_calls"`
}

type AgentSummary struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type WorkflowDefinition struct {
	WorkflowID                string           `json:"workflow_id"`
	Version                   string           `json:"version"`
	ArtifactType              string           `json:"artifact_type"`
	ArtifactSchemaVersion     string           `json:"artifact_schema_version"`
	RootAgent                 AgentRef         `json:"root_agent"`
	SharedInstructions        string           `json:"shared_instructions,omitempty"`
	SharedInstructionsVersion string           `json:"shared_instructions_version,omitempty"`
	Delegation                DelegationPolicy `json:"delegation"`
}

type AgentProvenance struct {
	CallID        string     `json:"call_id"`
	ParentCallID  string     `json:"parent_call_id,omitempty"`
	AgentID       string     `json:"agent_id"`
	AgentVersion  string     `json:"agent_version"`
	PromptVersion string     `json:"prompt_version"`
	Skills        []SkillRef `json:"skills,omitempty"`
	Tools         []ToolRef  `json:"tools,omitempty"`
}

type TraceEvent struct {
	Sequence     int             `json:"sequence"`
	Type         string          `json:"type"`
	At           time.Time       `json:"at"`
	AgentCallID  string          `json:"agent_call_id,omitempty"`
	ParentCallID string          `json:"parent_call_id,omitempty"`
	AgentID      string          `json:"agent_id,omitempty"`
	AgentVersion string          `json:"agent_version,omitempty"`
	ToolID       string          `json:"tool_id,omitempty"`
	ToolVersion  string          `json:"tool_version,omitempty"`
	SkillID      string          `json:"skill_id,omitempty"`
	SkillVersion string          `json:"skill_version,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	Error        string          `json:"error,omitempty"`
}

type ArtifactProvenance struct {
	InputSnapshotID           string            `json:"input_snapshot_id"`
	WorkflowID                string            `json:"workflow_id"`
	WorkflowVersion           string            `json:"workflow_version"`
	RootAgent                 AgentRef          `json:"root_agent"`
	Agents                    []AgentProvenance `json:"agents"`
	SharedInstructionsVersion string            `json:"shared_instructions_version,omitempty"`
	SourceRefs                []string          `json:"source_refs"`
}

type ArtifactEnvelope struct {
	ArtifactID    string             `json:"artifact_id"`
	ArtifactType  string             `json:"artifact_type"`
	SchemaVersion string             `json:"schema_version"`
	RunID         string             `json:"run_id"`
	CreatedAt     time.Time          `json:"created_at"`
	Provenance    ArtifactProvenance `json:"provenance"`
	Payload       json.RawMessage    `json:"payload"`
}

type RunRecord struct {
	RunID           string            `json:"run_id"`
	Status          string            `json:"status"`
	StartedAt       time.Time         `json:"started_at"`
	CompletedAt     time.Time         `json:"completed_at,omitempty"`
	InputSnapshotID string            `json:"input_snapshot_id"`
	WorkflowID      string            `json:"workflow_id"`
	WorkflowVersion string            `json:"workflow_version"`
	RootAgent       AgentRef          `json:"root_agent"`
	Agents          []AgentProvenance `json:"agents"`
	ExecutionTrace  []TraceEvent      `json:"execution_trace"`
	ArtifactID      string            `json:"artifact_id,omitempty"`
	Error           string            `json:"error,omitempty"`
}

type AgentRequest struct {
	RunID              string            `json:"run_id"`
	CallID             string            `json:"call_id"`
	ParentCallID       string            `json:"parent_call_id,omitempty"`
	Definition         AgentDefinition   `json:"definition"`
	Task               string            `json:"task"`
	Snapshot           InputSnapshot     `json:"snapshot"`
	SharedInstructions string            `json:"shared_instructions,omitempty"`
	ArtifactSchema     json.RawMessage   `json:"artifact_schema,omitempty"`
	Skills             []SkillDefinition `json:"skills,omitempty"`
	Tools              []ToolDefinition  `json:"tools,omitempty"`
	AvailableAgents    []AgentSummary    `json:"available_agents,omitempty"`
	Runtime            AgentRuntime      `json:"-"`
}

type AgentResult struct {
	Output json.RawMessage `json:"output"`
}

type ToolResult struct {
	Output json.RawMessage `json:"output"`
}

type EvaluationInput struct {
	Snapshot InputSnapshot      `json:"snapshot"`
	Workflow WorkflowDefinition `json:"workflow"`
	Run      RunRecord          `json:"run"`
	Artifact ArtifactEnvelope   `json:"artifact"`
}

type EvaluationResult struct {
	EvaluationID string          `json:"evaluation_id"`
	EvaluatorID  string          `json:"evaluator_id"`
	Version      string          `json:"version"`
	ArtifactID   string          `json:"artifact_id"`
	EvaluatedAt  time.Time       `json:"evaluated_at"`
	Passed       bool            `json:"passed"`
	Metrics      json.RawMessage `json:"metrics"`
}
