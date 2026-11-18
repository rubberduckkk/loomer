"""Strands owns the model/tool loop; Looomer owns capabilities and durable evidence."""
import asyncio
import json
import os
import re

from strands import Agent
from strands.types.tools import AgentTool
from strands.hooks import BeforeModelCallEvent, HookProvider
from strands.tools.executors import SequentialToolExecutor


def model_from_env(env=None):
    env = os.environ if env is None else env
    provider, model_id = env.get("LOOOMER_PROVIDER"), env.get("LOOOMER_MODEL")
    if not provider or not model_id:
        raise ValueError("LOOOMER_PROVIDER and LOOOMER_MODEL are required")
    try:
        if provider == "openai":
            from strands.models.openai import OpenAIModel
            args = {"api_key": required(env, "OPENAI_API_KEY")}
            if env.get("OPENAI_BASE_URL"):
                args["base_url"] = env["OPENAI_BASE_URL"]
            params = {}
            if env.get("LOOOMER_MAX_OUTPUT_TOKENS"):
                limit = int(env["LOOOMER_MAX_OUTPUT_TOKENS"])
                if limit < 1:
                    raise ValueError("LOOOMER_MAX_OUTPUT_TOKENS must be positive")
                params["max_tokens"] = limit
            return OpenAIModel(model_id=model_id, client_args=args, params=params)
        if provider == "anthropic":
            from strands.models.anthropic import AnthropicModel
            return AnthropicModel(model_id=model_id, max_tokens=4096,
                                  client_args={"api_key": required(env, "ANTHROPIC_API_KEY")})
        if provider == "google":
            from strands.models.gemini import GeminiModel
            return GeminiModel(model_id=model_id, client_args={"api_key": required(env, "GOOGLE_API_KEY")})
        if provider == "ollama":
            from strands.models.ollama import OllamaModel
            return OllamaModel(model_id=model_id, host=env.get("OLLAMA_HOST", "http://localhost:11434"))
        if provider == "bedrock":
            from strands.models.bedrock import BedrockModel
            return BedrockModel(model_id=model_id, region_name=env.get("AWS_REGION", "us-east-1"))
    except ImportError as error:
        raise ValueError(f"Install the provider extra: pip install 'looomer[{provider}]'") from error
    raise ValueError(f"unsupported LOOOMER_PROVIDER: {provider}")


def required(env, name):
    if not env.get(name):
        raise ValueError(f"{name} is required")
    return env[name]


class TurnLimit(HookProvider):
    def __init__(self, limit):
        self.limit, self.count = limit, 0

    def register_hooks(self, registry):
        registry.add_callback(BeforeModelCallEvent, self.before_model)

    def before_model(self, event):
        self.count += 1
        if self.count > self.limit:
            raise RuntimeError(f"agent exceeded {self.limit} model turns")


class StrandsAgent:
    def __init__(self, model, max_iterations=12):
        if max_iterations < 1:
            raise ValueError("max_iterations must be positive")
        self.model, self.max_iterations = model, max_iterations

    def run(self, request):
        failures = []
        tools = []

        for definition in request.tools:
            ref = {k: definition[k] for k in ("id", "version")}
            name = "tool_" + re.sub(r"[^a-zA-Z0-9_]", "_", definition["id"])
            tools.append(RuntimeTool(name, definition.get("description") or definition["id"],
                definition.get("input_schema", {"type": "object", "properties": {}}),
                lambda value, ref=ref: request.runtime.call_tool(ref, value), failures))
        if request.available_agents:
            refs = {r["id"] + "@" + r["version"]: r for r in request.available_agents}
            tools.append(RuntimeTool("delegate_agent", "Delegate a focused task to a registered specialist.",
                {"type": "object", "required": ["agent", "task"], "properties": {
                    "agent": {"type": "string", "enum": list(refs)}, "task": {"type": "string"}}},
                lambda value: request.runtime.delegate(refs[value["agent"]], value["task"]), failures))
        names = [t.tool_name for t in tools]
        if len(set(names)) != len(names):
            raise ValueError("tool names collide after normalization")
        instructions = "\n\n".join([request.definition["system_prompt"], request.shared_instructions,
            *[f"Skill {s['id']}@{s['version']}:\n{s['instructions']}" for s in request.skills],
            "Return a single JSON value, without markdown fences. " + (
                "Conform to artifact_schema." if not request.parent_call_id else "Return your specialist findings.")])
        agent = Agent(model=self.model, system_prompt=instructions, tools=tools,
            callback_handler=None, hooks=[TurnLimit(self.max_iterations)],
            tool_executor=SequentialToolExecutor())
        result = agent(json.dumps({"task": request.task, "input_snapshot": request.snapshot,
                                   "artifact_schema": request.schema}))
        if failures:
            raise failures[0]
        if result.stop_reason != "end_turn":
            raise ValueError(f"agent did not finish: {result.stop_reason}")
        return json.loads(str(result))


class RuntimeTool(AgentTool):
    """Adapt a runtime-defined JSON tool without SDK argument-name injection."""
    def __init__(self, name, description, schema, call, failures):
        super().__init__()
        self.name, self.description, self.schema = name, description, schema
        self.call, self.failures = call, failures

    @property
    def tool_name(self):
        return self.name

    @property
    def tool_type(self):
        return "function"

    @property
    def tool_spec(self):
        return {"name": self.name, "description": self.description, "inputSchema": {"json": self.schema}}

    async def stream(self, tool_use, invocation_state, **kwargs):
        try:
            from .runtime import validate
            validate(self.schema, tool_use["input"])
            value = await asyncio.to_thread(self.call, tool_use["input"])
            yield {"toolUseId": tool_use["toolUseId"], "status": "success", "content": [{"text": json.dumps(value)}]}
        except Exception as error:
            self.failures.append(error)
            yield {"toolUseId": tool_use["toolUseId"], "status": "error", "content": [{"text": str(error)}]}
