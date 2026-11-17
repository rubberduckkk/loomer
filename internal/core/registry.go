package core

import "sync"

type AgentRegistryImpl struct {
	mu     sync.RWMutex
	agents map[AgentRef]registeredAgent
}

type registeredAgent struct {
	definition AgentDefinition
	loop       Agent
}

func NewAgentRegistry() *AgentRegistryImpl {
	return &AgentRegistryImpl{agents: make(map[AgentRef]registeredAgent)}
}

func (r *AgentRegistryImpl) Register(definition AgentDefinition, loop Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = make(map[AgentRef]registeredAgent)
	}
	r.agents[AgentRef{ID: definition.ID, Version: definition.Version}] = registeredAgent{
		definition: definition,
		loop:       loop,
	}
}

func (r *AgentRegistryImpl) Resolve(ref AgentRef) (AgentDefinition, Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.agents[ref]
	return registered.definition, registered.loop, ok
}

type SkillRegistryImpl struct {
	mu     sync.RWMutex
	skills map[SkillRef]SkillDefinition
}

func NewSkillRegistry() *SkillRegistryImpl {
	return &SkillRegistryImpl{skills: make(map[SkillRef]SkillDefinition)}
}

func (r *SkillRegistryImpl) Register(skill SkillDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skills == nil {
		r.skills = make(map[SkillRef]SkillDefinition)
	}
	r.skills[SkillRef{ID: skill.ID, Version: skill.Version}] = skill
}

func (r *SkillRegistryImpl) Resolve(ref SkillRef) (SkillDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[ref]
	return skill, ok
}

type ToolRegistryImpl struct {
	mu    sync.RWMutex
	tools map[ToolRef]registeredTool
}

type registeredTool struct {
	definition ToolDefinition
	tool       Tool
}

func NewToolRegistry() *ToolRegistryImpl {
	return &ToolRegistryImpl{tools: make(map[ToolRef]registeredTool)}
}

func (r *ToolRegistryImpl) Register(definition ToolDefinition, tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[ToolRef]registeredTool)
	}
	r.tools[ToolRef{ID: definition.ID, Version: definition.Version}] = registeredTool{
		definition: definition,
		tool:       tool,
	}
}

func (r *ToolRegistryImpl) Resolve(ref ToolRef) (ToolDefinition, Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.tools[ref]
	return registered.definition, registered.tool, ok
}
