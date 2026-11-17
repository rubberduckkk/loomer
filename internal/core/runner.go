package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type Runner struct {
	Agents     AgentRegistry
	Skills     SkillRegistry
	Tools      ToolRegistry
	Repository Repository
	Validator  SchemaValidator
}

func (r Runner) Run(ctx context.Context, workflow WorkflowDefinition, snapshot InputSnapshot, task string, artifactSchema json.RawMessage) (RunRecord, *ArtifactEnvelope, error) {
	started := time.Now().UTC()
	runID, err := makeID("run_")
	if err != nil {
		return RunRecord{}, nil, err
	}
	record := RunRecord{
		RunID: runID, Status: "running", StartedAt: started,
		InputSnapshotID: snapshot.ID, WorkflowID: workflow.WorkflowID,
		WorkflowVersion: workflow.Version, RootAgent: workflow.RootAgent,
		Agents: []AgentProvenance{}, ExecutionTrace: []TraceEvent{},
	}
	if err := r.validate(workflow, snapshot, task, artifactSchema); err != nil {
		return failRun(record, err)
	}
	if err := r.Repository.SaveSnapshot(ctx, snapshot); err != nil {
		return failRun(record, fmt.Errorf("persist input snapshot: %w", err))
	}
	if err := r.Repository.SaveWorkflow(ctx, workflow); err != nil {
		return failRun(record, fmt.Errorf("persist workflow: %w", err))
	}
	if err := r.Repository.SaveRun(ctx, record); err != nil {
		return failRun(record, fmt.Errorf("persist initial run record: %w", err))
	}

	execution := &executionContext{
		runner: r, ctx: ctx, workflow: workflow, snapshot: snapshot,
		run: &record, artifactSchema: artifactSchema,
		validator: r.Validator,
	}
	result, err := execution.invoke(ctx, workflow.RootAgent, task, "", 0)
	if err != nil {
		return execution.fail(err)
	}
	validator := r.Validator
	if validator == nil {
		validator = JSONSchemaValidator{}
	}
	if err := validator.Validate(artifactSchema, result.Output); err != nil {
		return execution.fail(fmt.Errorf("root agent output does not match artifact schema: %w", err))
	}

	artifactID, err := makeID("artifact_")
	if err != nil {
		return execution.fail(err)
	}
	artifact := &ArtifactEnvelope{
		ArtifactID: artifactID, ArtifactType: workflow.ArtifactType,
		SchemaVersion: workflow.ArtifactSchemaVersion, RunID: runID,
		CreatedAt: time.Now().UTC(),
		Provenance: ArtifactProvenance{
			InputSnapshotID: snapshot.ID, WorkflowID: workflow.WorkflowID,
			WorkflowVersion: workflow.Version, RootAgent: workflow.RootAgent,
			Agents:                    append([]AgentProvenance(nil), record.Agents...),
			SharedInstructionsVersion: workflow.SharedInstructionsVersion,
			SourceRefs:                sourceIDs(snapshot),
		},
		Payload: append(json.RawMessage(nil), result.Output...),
	}
	if err := r.Repository.SaveArtifact(ctx, *artifact); err != nil {
		return execution.fail(fmt.Errorf("persist artifact: %w", err))
	}
	record.Status = "completed"
	record.CompletedAt = time.Now().UTC()
	record.ArtifactID = artifactID
	if err := r.Repository.SaveRun(ctx, record); err != nil {
		return record, artifact, fmt.Errorf("artifact %s was saved, but completed run record could not be persisted: %w", artifactID, err)
	}
	return record, artifact, nil
}

func (r Runner) validate(workflow WorkflowDefinition, snapshot InputSnapshot, task string, schema json.RawMessage) error {
	if r.Repository == nil {
		return fmt.Errorf("repository is required")
	}
	if r.Agents == nil {
		return fmt.Errorf("agent registry is required")
	}
	if workflow.WorkflowID == "" || workflow.Version == "" || workflow.ArtifactType == "" || workflow.ArtifactSchemaVersion == "" {
		return fmt.Errorf("workflow_id, version, artifact_type, and artifact_schema_version are required")
	}
	if workflow.RootAgent.ID == "" || workflow.RootAgent.Version == "" {
		return fmt.Errorf("workflow root_agent id and version are required")
	}
	if snapshot.ID == "" || len(snapshot.Items) == 0 {
		return fmt.Errorf("input snapshot is empty or missing an ID")
	}
	if task == "" {
		return fmt.Errorf("task is required")
	}
	if len(schema) == 0 || !json.Valid(schema) {
		return fmt.Errorf("artifact schema must be valid JSON")
	}
	if _, _, ok := r.Agents.Resolve(workflow.RootAgent); !ok {
		return fmt.Errorf("root agent %s@%s is not registered", workflow.RootAgent.ID, workflow.RootAgent.Version)
	}
	for _, ref := range workflow.Delegation.AllowedAgents {
		if _, _, ok := r.Agents.Resolve(ref); !ok {
			return fmt.Errorf("delegation agent %s@%s is not registered", ref.ID, ref.Version)
		}
	}
	if workflow.Delegation.MaxDepth < 0 || workflow.Delegation.MaxAgentCalls < 1 || workflow.Delegation.MaxToolCalls < 0 {
		return fmt.Errorf("delegation max_depth and max_tool_calls must be non-negative; max_agent_calls must be positive")
	}
	return nil
}

type executionContext struct {
	runner         Runner
	ctx            context.Context
	workflow       WorkflowDefinition
	snapshot       InputSnapshot
	run            *RunRecord
	artifactSchema json.RawMessage
	validator      SchemaValidator
	agentCalls     int
	toolCalls      int
}

func (e *executionContext) invoke(ctx context.Context, ref AgentRef, task, parentCallID string, depth int) (AgentResult, error) {
	if err := ctx.Err(); err != nil {
		return AgentResult{}, err
	}
	if depth > e.workflow.Delegation.MaxDepth {
		return AgentResult{}, fmt.Errorf("agent delegation exceeded max_depth %d", e.workflow.Delegation.MaxDepth)
	}
	e.agentCalls++
	if e.agentCalls > e.workflow.Delegation.MaxAgentCalls {
		return AgentResult{}, fmt.Errorf("agent calls exceeded max_agent_calls %d", e.workflow.Delegation.MaxAgentCalls)
	}
	definition, agent, ok := e.runner.Agents.Resolve(ref)
	if !ok {
		return AgentResult{}, fmt.Errorf("agent %s@%s is not registered", ref.ID, ref.Version)
	}
	if definition.ID != ref.ID || definition.Version != ref.Version || definition.PromptVersion == "" || definition.SystemPrompt == "" {
		return AgentResult{}, fmt.Errorf("agent registry returned incomplete definition for %s@%s", ref.ID, ref.Version)
	}
	callID, err := makeID("call_")
	if err != nil {
		return AgentResult{}, err
	}
	provenance := AgentProvenance{
		CallID: callID, ParentCallID: parentCallID,
		AgentID: ref.ID, AgentVersion: ref.Version, PromptVersion: definition.PromptVersion,
		Skills: append([]SkillRef(nil), definition.Skills...), Tools: append([]ToolRef(nil), definition.Tools...),
	}
	e.run.Agents = append(e.run.Agents, provenance)
	if err := e.addEvent(TraceEvent{
		Type: "agent_started", AgentCallID: callID, ParentCallID: parentCallID,
		AgentID: ref.ID, AgentVersion: ref.Version,
		Input: json.RawMessage(mustJSON(map[string]string{"task": task})),
	}); err != nil {
		return AgentResult{}, err
	}

	skills, err := e.loadSkills(definition, callID, parentCallID)
	if err != nil {
		return AgentResult{}, err
	}
	tools, err := e.loadTools(definition)
	if err != nil {
		return AgentResult{}, err
	}
	availableAgents := []AgentSummary{}
	if definition.CanDelegate {
		availableAgents = make([]AgentSummary, 0, len(e.workflow.Delegation.AllowedAgents))
		for _, availableRef := range e.workflow.Delegation.AllowedAgents {
			availableDefinition, _, _ := e.runner.Agents.Resolve(availableRef)
			availableAgents = append(availableAgents, AgentSummary{
				ID: availableRef.ID, Version: availableRef.Version, Description: availableDefinition.Description,
			})
		}
	}
	runtime := &agentRuntime{execution: e, definition: definition, callID: callID, parentCallID: parentCallID, depth: depth}
	request := AgentRequest{
		RunID: e.run.RunID, CallID: callID, ParentCallID: parentCallID,
		Definition: definition, Task: task, Snapshot: e.snapshot,
		SharedInstructions: e.workflow.SharedInstructions,
		ArtifactSchema:     append(json.RawMessage(nil), e.artifactSchema...),
		Skills:             skills, Tools: tools, AvailableAgents: availableAgents, Runtime: runtime,
	}
	result, runErr := agent.Run(ctx, request)
	if runErr != nil {
		_ = e.addEvent(TraceEvent{
			Type: "agent_failed", AgentCallID: callID, ParentCallID: parentCallID,
			AgentID: ref.ID, AgentVersion: ref.Version, Error: runErr.Error(),
		})
		return AgentResult{}, fmt.Errorf("agent %s@%s: %w", ref.ID, ref.Version, runErr)
	}
	if !json.Valid(result.Output) {
		return AgentResult{}, fmt.Errorf("agent %s@%s returned invalid JSON", ref.ID, ref.Version)
	}
	if err := e.addEvent(TraceEvent{
		Type: "agent_completed", AgentCallID: callID, ParentCallID: parentCallID,
		AgentID: ref.ID, AgentVersion: ref.Version, Output: append(json.RawMessage(nil), result.Output...),
	}); err != nil {
		return AgentResult{}, err
	}
	return result, nil
}

func (e *executionContext) loadSkills(definition AgentDefinition, callID, parentCallID string) ([]SkillDefinition, error) {
	if len(definition.Skills) == 0 {
		return nil, nil
	}
	if e.runner.Skills == nil {
		return nil, fmt.Errorf("agent %s declares skills but no skill registry is configured", definition.ID)
	}
	skills := make([]SkillDefinition, 0, len(definition.Skills))
	for _, ref := range definition.Skills {
		skill, ok := e.runner.Skills.Resolve(ref)
		if !ok {
			return nil, fmt.Errorf("skill %s@%s is not registered", ref.ID, ref.Version)
		}
		skills = append(skills, skill)
		if err := e.addEvent(TraceEvent{
			Type: "skill_loaded", AgentCallID: callID, ParentCallID: parentCallID,
			AgentID: definition.ID, AgentVersion: definition.Version,
			SkillID: ref.ID, SkillVersion: ref.Version,
		}); err != nil {
			return nil, err
		}
	}
	return skills, nil
}

func (e *executionContext) loadTools(definition AgentDefinition) ([]ToolDefinition, error) {
	if len(definition.Tools) == 0 {
		return nil, nil
	}
	if e.runner.Tools == nil {
		return nil, fmt.Errorf("agent %s declares tools but no tool registry is configured", definition.ID)
	}
	tools := make([]ToolDefinition, 0, len(definition.Tools))
	for _, ref := range definition.Tools {
		definition, _, ok := e.runner.Tools.Resolve(ref)
		if !ok {
			return nil, fmt.Errorf("tool %s@%s is not registered", ref.ID, ref.Version)
		}
		tools = append(tools, definition)
	}
	return tools, nil
}

func (e *executionContext) addEvent(event TraceEvent) error {
	event.Sequence = len(e.run.ExecutionTrace) + 1
	event.At = time.Now().UTC()
	e.run.ExecutionTrace = append(e.run.ExecutionTrace, event)
	if err := e.runner.Repository.SaveRun(e.ctx, *e.run); err != nil {
		return fmt.Errorf("persist execution trace event %d: %w", event.Sequence, err)
	}
	return nil
}

func (e *executionContext) fail(err error) (RunRecord, *ArtifactEnvelope, error) {
	e.run.Status = "failed"
	e.run.CompletedAt = time.Now().UTC()
	e.run.Error = err.Error()
	if saveErr := e.runner.Repository.SaveRun(e.ctx, *e.run); saveErr != nil {
		err = fmt.Errorf("%w; persist failed run: %v", err, saveErr)
		e.run.Error = err.Error()
	}
	return *e.run, nil, err
}

func (e *executionContext) callTool(ctx context.Context, caller AgentDefinition, callID string, ref ToolRef, input json.RawMessage) (ToolResult, error) {
	if !containsTool(caller.Tools, ref) {
		return ToolResult{}, fmt.Errorf("agent %s is not allowed to call tool %s@%s", caller.ID, ref.ID, ref.Version)
	}
	e.toolCalls++
	if e.toolCalls > e.workflow.Delegation.MaxToolCalls {
		return ToolResult{}, fmt.Errorf("tool calls exceeded max_tool_calls %d", e.workflow.Delegation.MaxToolCalls)
	}
	if e.runner.Tools == nil {
		return ToolResult{}, fmt.Errorf("tool registry is not configured")
	}
	definition, tool, ok := e.runner.Tools.Resolve(ref)
	if !ok {
		return ToolResult{}, fmt.Errorf("tool %s@%s is not registered", ref.ID, ref.Version)
	}
	if !json.Valid(input) {
		return ToolResult{}, fmt.Errorf("tool input is not valid JSON")
	}
	if len(definition.InputSchema) > 0 {
		validator := e.validator
		if validator == nil {
			validator = JSONSchemaValidator{}
		}
		if err := validator.Validate(definition.InputSchema, input); err != nil {
			return ToolResult{}, fmt.Errorf("tool input schema check failed: %w", err)
		}
	}
	output, err := tool.Execute(ctx, input)
	if err != nil {
		_ = e.addEvent(TraceEvent{
			Type: "tool_failed", AgentCallID: callID, AgentID: caller.ID, AgentVersion: caller.Version,
			ToolID: ref.ID, ToolVersion: ref.Version, Input: append(json.RawMessage(nil), input...), Error: err.Error(),
		})
		return ToolResult{}, err
	}
	if !json.Valid(output) {
		return ToolResult{}, fmt.Errorf("tool %s@%s returned invalid JSON", ref.ID, ref.Version)
	}
	if err := e.addEvent(TraceEvent{
		Type: "tool_called", AgentCallID: callID, AgentID: caller.ID, AgentVersion: caller.Version,
		ToolID: ref.ID, ToolVersion: ref.Version,
		Input: append(json.RawMessage(nil), input...), Output: append(json.RawMessage(nil), output...),
	}); err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Output: output}, nil
}

func (e *executionContext) delegate(ctx context.Context, caller AgentDefinition, callID string, depth int, target AgentRef, task string) (AgentResult, error) {
	if !caller.CanDelegate {
		return AgentResult{}, fmt.Errorf("agent %s is not allowed to delegate", caller.ID)
	}
	if !slices.Contains(e.workflow.Delegation.AllowedAgents, target) {
		return AgentResult{}, fmt.Errorf("workflow does not allow delegation to agent %q", target.ID)
	}
	if task == "" {
		return AgentResult{}, fmt.Errorf("delegated task is required")
	}
	encoded, _ := json.Marshal(map[string]any{"agent_id": target.ID, "version": target.Version, "task": task})
	if err := e.addEvent(TraceEvent{
		Type: "agent_delegated", AgentCallID: callID, ParentCallID: callID,
		AgentID: caller.ID, AgentVersion: caller.Version, Input: encoded,
	}); err != nil {
		return AgentResult{}, err
	}
	return e.invoke(ctx, target, task, callID, depth+1)
}

type agentRuntime struct {
	execution    *executionContext
	definition   AgentDefinition
	callID       string
	parentCallID string
	depth        int
}

func (r *agentRuntime) CallTool(ctx context.Context, ref ToolRef, input json.RawMessage) (ToolResult, error) {
	return r.execution.callTool(ctx, r.definition, r.callID, ref, input)
}

func (r *agentRuntime) Delegate(ctx context.Context, ref AgentRef, task string) (AgentResult, error) {
	return r.execution.delegate(ctx, r.definition, r.callID, r.depth, ref, task)
}

func containsTool(tools []ToolRef, target ToolRef) bool {
	return slices.Contains(tools, target)
}

func failRun(record RunRecord, err error) (RunRecord, *ArtifactEnvelope, error) {
	record.Status = "failed"
	record.CompletedAt = time.Now().UTC()
	record.Error = err.Error()
	return record, nil, err
}

func sourceIDs(snapshot InputSnapshot) []string {
	ids := make([]string, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func makeID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
