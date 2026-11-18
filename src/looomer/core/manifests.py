"""Trusted, immutable app configurations and external JSON tool bindings."""
import os
import re

import httpx
from jsonschema import Draft202012Validator

from .runtime import Registry, key


def tag_list(value):
    if not isinstance(value, list) or any(not isinstance(t, str) or not t.strip() for t in value):
        raise ValueError("tags must be an array of nonempty strings")
    return sorted({t.strip() for t in value})


def check_manifest(m):
    tag_list(m.get("required_tags", []))
    for name in ("app", "version", "source"):
        if not isinstance(m.get(name), str) or not m[name].strip():
            raise ValueError(f"{name} is required")
    if type(m.get("max_items")) is not int or not 1 <= m["max_items"] <= 10000:
        raise ValueError("max_items must be between 1 and 10000")
    workflow = m["workflow"]
    if workflow["workflow_id"] != m["app"] or workflow["version"] != m["version"]:
        raise ValueError("app identity must match workflow identity")
    Draft202012Validator.check_schema(m["schema"])
    bindings = {}
    for group in ("agents", "tools", "skills"):
        definitions = m.get(group, [])
        bindings[group] = {key(d) for d in definitions}
        if len(bindings[group]) != len(definitions):
            raise ValueError(f"duplicate {group} definition")
    for definition in m["agents"]:
        if not definition["system_prompt"] or not definition["prompt_version"]:
            raise ValueError("agent prompt and version required")
        for group in ("tools", "skills"):
            if any(key(ref) not in bindings[group] for ref in definition.get(group, [])):
                raise ValueError(f"undefined {group} reference")
    policy = workflow["delegation"]
    if any(key(ref) not in bindings["agents"] for ref in [workflow["root_agent"], *policy.get("allowed_agents", [])]):
        raise ValueError("undefined agent reference")
    for definition in m.get("tools", []):
        if not re.fullmatch("[A-Z][A-Z0-9_]*", definition["connector"]):
            raise ValueError("invalid connector binding")
        Draft202012Validator.check_schema(definition["input_schema"])


def connector(binding, job_id):
    prefix = "LOOOMER_CONNECTOR_" + binding
    url = os.environ[prefix + "_URL"]
    token = os.environ.get(prefix + "_TOKEN")
    headers = {"X-Looomer-Job": job_id}
    if token:
        headers["Authorization"] = "Bearer " + token

    def call(value):
        response = httpx.post(url, json=value, headers=headers, timeout=30)
        response.raise_for_status()
        return response.json()
    return call


def registries(manifest, harness, job_id):
    agents, skills, tools = Registry(), Registry(), Registry()
    for definition in manifest["agents"]:
        agents.register(definition, harness)
    for definition in manifest.get("skills", []):
        skills.register(definition)
    for definition in manifest.get("tools", []):
        tools.register(definition, connector(definition["connector"], job_id))
    return agents, skills, tools
