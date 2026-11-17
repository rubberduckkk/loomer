package core

import (
	"context"
	"encoding/json"
)

// Agent owns a harness loop. Implementations decide how to reason, request
// tools, use loaded skills, and finish with a structured output.
type Agent interface {
	Run(context.Context, AgentRequest) (AgentResult, error)
}

// AgentRuntime exposes only the execution capabilities available to the
// current agent. Delegation and ordinary tools remain distinct capabilities.
type AgentRuntime interface {
	CallTool(context.Context, ToolRef, json.RawMessage) (ToolResult, error)
	Delegate(context.Context, AgentRef, string) (AgentResult, error)
}

type Tool interface {
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

type AgentRegistry interface {
	Resolve(AgentRef) (AgentDefinition, Agent, bool)
}

type SkillRegistry interface {
	Resolve(SkillRef) (SkillDefinition, bool)
}

type ToolRegistry interface {
	Resolve(ToolRef) (ToolDefinition, Tool, bool)
}

type Evaluator interface {
	Evaluate(context.Context, EvaluationInput) (EvaluationResult, error)
}

type SchemaValidator interface {
	Validate(schema, value json.RawMessage) error
}
