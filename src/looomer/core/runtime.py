"""Generic contracts and policy enforcement. No domain or model-provider dependencies."""
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import json
from typing import Protocol, Any
from uuid import uuid4

from jsonschema import Draft202012Validator, FormatChecker


def now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def new_id(prefix):
    return prefix + uuid4().hex


def validate(schema, value):
    Draft202012Validator.check_schema(schema)
    Draft202012Validator(schema, format_checker=FormatChecker()).validate(value)


def key(ref):
    return ref["id"] + "@" + ref["version"]


def snapshot(items):
    if not items:
        raise ValueError("snapshot must contain source items")
    items = sorted(items, key=lambda i: i["id"])
    seen, digest = set(), hashlib.sha256()
    for item in items:
        if not item["id"] or not item["uri"] or item["id"] in seen:
            raise ValueError("source IDs and URIs must be nonempty; IDs must be unique")
        seen.add(item["id"])
        metadata = json.dumps(item.get("metadata"), sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        # Match Go encoding/json's HTML escaping for stable existing snapshot identities.
        for char, escaped in [("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")]:
            metadata = metadata.replace(char, escaped)
        digest.update(("\0".join([item["id"], item["uri"], item.get("media_type", ""), item.get("content_hash", ""), metadata]) + "\0").encode())
    return {"schema_version": "0.2.0", "id": "snapshot_" + digest.hexdigest(), "created_at": now(), "items": items}


class Repository(Protocol):
    def save(self, collection: str, identifier: str, value: dict) -> None: ...
    def load(self, collection: str, identifier: str) -> dict: ...


@dataclass
class AgentRequest:
    definition: dict
    task: str
    snapshot: dict
    schema: dict
    shared_instructions: str
    skills: list[dict]
    tools: list[dict]
    available_agents: list[dict]
    runtime: "Runtime"
    parent_call_id: str = ""


class Agent(Protocol):
    def run(self, request: AgentRequest) -> Any: ...


class Registry:
    def __init__(self):
        self.entries = {}

    def register(self, definition, implementation=None):
        self.entries[key(definition)] = (definition, implementation)

    def resolve(self, ref):
        try:
            return self.entries[key(ref)]
        except KeyError as e:
            raise ValueError(f"unregistered reference: {key(ref)}") from e


class Runner:
    def __init__(self, agents: Registry, repository: Repository, skills=None, tools=None):
        self.agents, self.repository = agents, repository
        self.skills, self.tools = skills or Registry(), tools or Registry()

    def run(self, workflow, source, task, schema):
        execution = Execution(self, workflow, source, schema)
        record = execution.record
        try:
            for field in ("workflow_id", "version", "artifact_type", "artifact_schema_version"):
                if not workflow.get(field):
                    raise ValueError(f"{field} is required")
            policy = workflow["delegation"]
            if policy["max_depth"] < 0 or policy["max_agent_calls"] < 1 or policy["max_tool_calls"] < 0:
                raise ValueError("invalid execution limits")
            if not task or not source.get("id") or not source.get("items"):
                raise ValueError("task and input snapshot are required")
            Draft202012Validator.check_schema(schema)
            for ref in [workflow["root_agent"], *policy.get("allowed_agents", [])]:
                self.agents.resolve(ref)
            self.repository.save("snapshots", source["id"], source)
            self.repository.save("workflows", workflow["workflow_id"] + "@" + workflow["version"], workflow)
            execution.persist()
            output = execution.invoke(workflow["root_agent"], task)
            validate(schema, output)
            artifact = {
                "artifact_id": new_id("artifact_"), "artifact_type": workflow["artifact_type"],
                "schema_version": workflow["artifact_schema_version"], "run_id": record["run_id"], "created_at": now(),
                "provenance": {"input_snapshot_id": source["id"], "workflow_id": workflow["workflow_id"],
                    "workflow_version": workflow["version"], "root_agent": workflow["root_agent"],
                    "agents": record["agents"], "shared_instructions_version": workflow.get("shared_instructions_version", ""),
                    "source_refs": [i["id"] for i in source["items"]]}, "payload": output,
            }
            self.repository.save("artifacts", artifact["artifact_id"], artifact)
            record.update(status="completed", completed_at=now(), artifact_id=artifact["artifact_id"])
            execution.persist()
            return record, artifact
        except Exception as error:
            record.update(status="failed", completed_at=now(), error=str(error))
            execution.persist()
            raise


class Execution:
    def __init__(self, runner, workflow, source, schema):
        self.runner, self.workflow, self.source, self.schema = runner, workflow, source, schema
        self.agent_calls = self.tool_calls = 0
        self.record = {"run_id": new_id("run_"), "status": "running", "started_at": now(),
            "input_snapshot_id": source["id"], "workflow_id": workflow["workflow_id"],
            "workflow_version": workflow["version"], "root_agent": workflow["root_agent"],
            "agents": [], "execution_trace": []}

    def persist(self):
        self.runner.repository.save("runs", self.record["run_id"], self.record)

    def event(self, event_type, **fields):
        self.record["execution_trace"].append({"sequence": len(self.record["execution_trace"]) + 1,
            "type": event_type, "at": now(), **fields})
        self.persist()

    def invoke(self, ref, task, parent="", depth=0):
        policy = self.workflow["delegation"]
        if depth > policy["max_depth"] or self.agent_calls >= policy["max_agent_calls"]:
            raise ValueError("agent delegation limit exceeded")
        self.agent_calls += 1
        definition, agent = self.runner.agents.resolve(ref)
        if not definition.get("system_prompt") or not definition.get("prompt_version"):
            raise ValueError("agent prompt and version are required")
        call_id = new_id("call_")
        fields = dict(agent_call_id=call_id, parent_call_id=parent, agent_id=ref["id"], agent_version=ref["version"])
        self.record["agents"].append({"call_id": call_id, "parent_call_id": parent,
            "agent_id": ref["id"], "agent_version": ref["version"], "prompt_version": definition["prompt_version"],
            "skills": definition.get("skills", []), "tools": definition.get("tools", [])})
        self.event("agent_started", **fields, input={"task": task})
        try:
            skills = []
            for skill_ref in definition.get("skills", []):
                skills.append(self.runner.skills.resolve(skill_ref)[0])
                self.event("skill_loaded", **fields, skill_id=skill_ref["id"], skill_version=skill_ref["version"])
            tools = [self.runner.tools.resolve(t)[0] for t in definition.get("tools", [])]
            available = policy.get("allowed_agents", []) if definition.get("can_delegate") else []
            request = AgentRequest(definition, task, self.source, self.schema,
                self.workflow.get("shared_instructions", ""), skills, tools, available,
                Runtime(self, definition, call_id, depth), parent)
            result = agent.run(request)
            json.dumps(result, allow_nan=False)
            self.event("agent_completed", **fields, output=result)
            return result
        except Exception as error:
            self.event("agent_failed", **fields, error=str(error))
            raise


class Runtime:
    def __init__(self, execution, definition, call_id, depth):
        self.execution, self.definition, self.call_id, self.depth = execution, definition, call_id, depth

    def delegate(self, ref, task):
        e = self.execution
        if not self.definition.get("can_delegate") or ref not in e.workflow["delegation"].get("allowed_agents", []):
            raise ValueError("delegation is not allowed")
        if not task.strip():
            raise ValueError("delegated task is required")
        e.event("agent_delegated", agent_call_id=self.call_id, input={"agent": ref, "task": task})
        return e.invoke(ref, task, self.call_id, self.depth + 1)

    def call_tool(self, ref, value):
        e = self.execution
        if ref not in self.definition.get("tools", []):
            raise ValueError("tool is not allowed")
        if e.tool_calls >= e.workflow["delegation"]["max_tool_calls"]:
            raise ValueError("tool call limit exceeded")
        e.tool_calls += 1
        definition, implementation = e.runner.tools.resolve(ref)
        fields = dict(agent_call_id=self.call_id, tool_id=ref["id"], tool_version=ref["version"], input=value)
        try:
            validate(definition.get("input_schema", {}), value)
            result = implementation(value)
            json.dumps(result, allow_nan=False)
            e.event("tool_called", **fields, output=result)
            return result
        except Exception as error:
            e.event("tool_failed", **fields, error=str(error))
            raise
