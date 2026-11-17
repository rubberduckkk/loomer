package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rubberduckkk/loomer/internal/core"
)

// ChatModel is the model boundary used by the agent loop. Keeping it separate
// from the harness allows providers and test doubles to be swapped later.
type ChatModel interface {
	Complete(context.Context, []Message, []FunctionTool) (Message, error)
}

type ReActAgent struct {
	Model         ChatModel
	MaxIterations int
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type FunctionTool struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type loopTool struct {
	name   string
	schema FunctionTool
	call   func(context.Context, json.RawMessage) (string, error)
}

func (a ReActAgent) Run(ctx context.Context, request core.AgentRequest) (core.AgentResult, error) {
	if a.Model == nil {
		return core.AgentResult{}, fmt.Errorf("chat model is required")
	}
	maxIterations := a.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 12
	}
	messages := []Message{{Role: "system", Content: buildInstructions(request)}}
	input, err := json.Marshal(map[string]any{
		"task": request.Task, "input_snapshot": request.Snapshot,
		"artifact_schema": json.RawMessage(request.ArtifactSchema),
	})
	if err != nil {
		return core.AgentResult{}, fmt.Errorf("encode agent input: %w", err)
	}
	messages = append(messages, Message{Role: "user", Content: string(input)})
	loopTools := buildTools(request)
	toolSchemas := make([]FunctionTool, 0, len(loopTools))
	toolByName := make(map[string]loopTool, len(loopTools))
	for _, t := range loopTools {
		toolByName[t.name] = t
		toolSchemas = append(toolSchemas, t.schema)
	}

	for iteration := 0; iteration < maxIterations; iteration++ {
		response, err := a.Model.Complete(ctx, messages, toolSchemas)
		if err != nil {
			return core.AgentResult{}, fmt.Errorf("model turn %d: %w", iteration+1, err)
		}
		messages = append(messages, response)
		if len(response.ToolCalls) == 0 {
			content, _ := response.Content.(string)
			content = strings.TrimSpace(content)
			if content == "" {
				return core.AgentResult{}, fmt.Errorf("model returned an empty final response")
			}
			var compact json.RawMessage
			if err := json.Unmarshal([]byte(content), &compact); err != nil {
				return core.AgentResult{}, fmt.Errorf("model final response is not JSON: %w", err)
			}
			return core.AgentResult{Output: compact}, nil
		}
		for _, call := range response.ToolCalls {
			tool, ok := toolByName[call.Function.Name]
			if !ok {
				return core.AgentResult{}, fmt.Errorf("model requested unavailable tool %q", call.Function.Name)
			}
			arguments := json.RawMessage(call.Function.Arguments)
			if !json.Valid(arguments) {
				return core.AgentResult{}, fmt.Errorf("model supplied invalid arguments for tool %q", call.Function.Name)
			}
			output, err := tool.call(ctx, arguments)
			if err != nil {
				return core.AgentResult{}, fmt.Errorf("tool %s: %w", call.Function.Name, err)
			}
			messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: output})
		}
	}
	return core.AgentResult{}, fmt.Errorf("agent reached its %d-iteration limit without a final answer", maxIterations)
}

func buildInstructions(request core.AgentRequest) string {
	var b strings.Builder
	b.WriteString(request.Definition.SystemPrompt)
	if request.SharedInstructions != "" {
		b.WriteString("\n\nWorkflow instructions:\n")
		b.WriteString(request.SharedInstructions)
	}
	for _, skill := range request.Skills {
		fmt.Fprintf(&b, "\n\nSkill %s@%s:\n%s", skill.ID, skill.Version, skill.Instructions)
	}
	if request.ParentCallID == "" {
		b.WriteString("\n\nReturn the final result as a single JSON value that conforms to the supplied artifact_schema. Use tool calls when needed. Do not wrap JSON in markdown fences.")
	} else {
		b.WriteString("\n\nReturn a concise, useful JSON result for the task assigned to you. Do not wrap JSON in markdown fences.")
	}
	return b.String()
}

func buildTools(request core.AgentRequest) []loopTool {
	tools := make([]loopTool, 0, len(request.Tools)+1)
	for _, definition := range request.Tools {
		ref := core.ToolRef{ID: definition.ID, Version: definition.Version}
		parameters := definition.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		name := "tool_" + safeName(definition.ID)
		tools = append(tools, loopTool{
			name:   name,
			schema: FunctionTool{Type: "function", Function: FunctionSchema{Name: name, Description: definition.Description, Parameters: parameters}},
			call: func(ctx context.Context, input json.RawMessage) (string, error) {
				result, err := request.Runtime.CallTool(ctx, ref, input)
				return string(result.Output), err
			},
		})
	}
	if request.Definition.CanDelegate && len(request.AvailableAgents) > 0 {
		refs := make(map[string]core.AgentRef, len(request.AvailableAgents))
		choices := make([]string, 0, len(request.AvailableAgents))
		for _, agent := range request.AvailableAgents {
			key := agent.ID + "@" + agent.Version
			refs[key] = core.AgentRef{ID: agent.ID, Version: agent.Version}
			choices = append(choices, key)
		}
		parameters, _ := json.Marshal(map[string]any{
			"type": "object", "required": []string{"agent", "task"}, "properties": map[string]any{
				"agent": map[string]any{"type": "string", "enum": choices, "description": "Available specialist agent ID and version"},
				"task":  map[string]any{"type": "string", "description": "Focused task for the specialist"},
			},
		})
		tools = append(tools, loopTool{
			name:   "delegate_agent",
			schema: FunctionTool{Type: "function", Function: FunctionSchema{Name: "delegate_agent", Description: "Delegate a focused part of the task to an available specialist agent.", Parameters: parameters}},
			call: func(ctx context.Context, input json.RawMessage) (string, error) {
				var args struct {
					Agent string `json:"agent"`
					Task  string `json:"task"`
				}
				if err := json.Unmarshal(input, &args); err != nil {
					return "", err
				}
				ref, ok := refs[args.Agent]
				if !ok {
					return "", fmt.Errorf("agent %q is not available", args.Agent)
				}
				result, err := request.Runtime.Delegate(ctx, ref, args.Task)
				return string(result.Output), err
			},
		})
	}
	return tools
}

func safeName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

type OpenAICompatibleConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

func OpenAICompatibleFromEnv() (*OpenAICompatibleModel, error) {
	config := OpenAICompatibleConfig{
		BaseURL: os.Getenv("OPENAI_BASE_URL"), APIKey: os.Getenv("OPENAI_API_KEY"), Model: os.Getenv("LOOMER_MODEL"),
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("LOOMER_MODEL is required")
	}
	return NewOpenAICompatibleModel(config)
}

type OpenAICompatibleModel struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func NewOpenAICompatibleModel(config OpenAICompatibleConfig) (*OpenAICompatibleModel, error) {
	parsed, err := url.Parse(strings.TrimRight(config.BaseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid model base URL %q", config.BaseURL)
	}
	if config.Model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &OpenAICompatibleModel{endpoint: strings.TrimRight(config.BaseURL, "/") + "/chat/completions", apiKey: config.APIKey, model: config.Model, client: config.Client}, nil
}

func (m *OpenAICompatibleModel) Complete(ctx context.Context, messages []Message, tools []FunctionTool) (Message, error) {
	body := struct {
		Model          string            `json:"model"`
		Messages       []Message         `json:"messages"`
		Tools          []FunctionTool    `json:"tools,omitempty"`
		ToolChoice     string            `json:"tool_choice,omitempty"`
		ResponseFormat map[string]string `json:"response_format,omitempty"`
	}{Model: m.model, Messages: messages, Tools: tools}
	if len(tools) > 0 {
		body.ToolChoice = "auto"
	}
	if len(tools) == 0 {
		body.ResponseFormat = map[string]string{"type": "json_object"}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Message{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	response, err := m.client.Do(req)
	if err != nil {
		return Message{}, err
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return Message{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Message{}, fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBytes)))
	}
	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBytes, &result); err != nil {
		return Message{}, fmt.Errorf("decode model response: %w", err)
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("provider returned no choices")
	}
	return result.Choices[0].Message, nil
}
