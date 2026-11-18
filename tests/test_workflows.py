import copy
import json

import pytest
from PIL import Image
from strands.models import Model

from looomer.core import Registry, Runner, snapshot
from looomer.core.harness import StrandsAgent, model_from_env
from looomer.apps.photo_curation.photos import scan, to_snapshot, load_workflow, registries, evaluate
from looomer.core.storage import FileRepository


class ScriptedModel(Model):
    """A provider double; the actual Strands agent and tool executor remain real."""
    def __init__(self, responses):
        self.responses = iter(responses)
        self.requests = []

    def get_config(self):
        return {"model_id": "scripted"}

    def update_config(self, **kwargs):
        pass

    async def structured_output(self, *args, **kwargs):
        raise NotImplementedError
        yield

    async def stream(self, messages, tool_specs=None, system_prompt=None, **kwargs):
        self.requests.append(copy.deepcopy(messages))
        response = next(self.responses)
        yield {"messageStart": {"role": "assistant"}}
        if isinstance(response, tuple):
            name, value = response
            yield {"contentBlockStart": {"start": {"toolUse": {"toolUseId": "call", "name": name}}}}
            yield {"contentBlockDelta": {"delta": {"toolUse": {"input": json.dumps(value)}}}}
            yield {"contentBlockStop": {"contentBlockIndex": 0}}
            yield {"messageStop": {"stopReason": "tool_use"}}
        else:
            yield {"contentBlockDelta": {"delta": {"text": json.dumps(response)}}}
            yield {"contentBlockStop": {"contentBlockIndex": 0}}
            yield {"messageStop": {"stopReason": "end_turn"}}
        yield {"metadata": {"usage": {"inputTokens": 1, "outputTokens": 1, "totalTokens": 2}, "metrics": {"latencyMs": 1}}}


def photo_run(tmp_path, responses=None):
    folder = tmp_path / "photos"
    folder.mkdir()
    Image.new("RGB", (12, 8)).save(folder / "one.jpg")
    catalog = scan(folder)
    photo_id = catalog["photos"][0]["photo_id"]
    payload = {"brief": {"target_count": 1}, "concept": "One photo", "selection": [
        {"photo_id": photo_id, "order": 1, "role": "opener", "rationale": "Landscape orientation"}]}
    responses = responses or [("delegate_agent", {"agent": "photo-metadata-reviewer@0.1.0", "task": "Review metadata"}), {"finding": "landscape"}, payload]
    model = ScriptedModel(responses)
    agents, skills = registries(StrandsAgent(model))
    repository = FileRepository(tmp_path / "store")
    workflow, schema = load_workflow()
    run, artifact = Runner(agents, repository, skills).run(workflow, to_snapshot(catalog), "Curate", schema)
    return repository, model, run, artifact


def test_real_strands_delegation_and_photo_evaluation(tmp_path):
    repository, model, run, artifact = photo_run(tmp_path)
    assert len(model.requests) == 3
    assert any("toolResult" in c for m in model.requests[-1] for c in m["content"])
    assert len(run["agents"]) == 2
    assert run["agents"][1]["parent_call_id"] == run["agents"][0]["call_id"]
    assert evaluate(repository, artifact["artifact_id"])["passed"]
    assert repository.load("runs", run["run_id"])["status"] == "completed"


def test_invalid_output_persists_failure(tmp_path):
    with pytest.raises(Exception):
        photo_run(tmp_path, [{"invalid": True}])
    records = list((tmp_path / "store" / "runs").glob("*.json"))
    assert len(records) == 1
    assert json.loads(records[0].read_text())["status"] == "failed"
    assert not (tmp_path / "store" / "artifacts").exists()


def test_evaluation_rejects_foreign_ids(tmp_path):
    repo, _, _, artifact = photo_run(tmp_path)
    artifact["payload"]["selection"][0]["photo_id"] = "foreign"
    repo.save("artifacts", artifact["artifact_id"], artifact)
    assert not evaluate(repo, artifact["artifact_id"])["passed"]


def test_generic_tool_loop_and_budget(tmp_path):
    ref = {"id": "analyst", "version": "1"}
    tool_ref = {"id": "lookup", "version": "1"}
    definition = {**ref, "system_prompt": "Analyze sources", "prompt_version": "1", "tools": [tool_ref]}
    workflow = {"workflow_id": "generic", "version": "1", "artifact_type": "analysis", "artifact_schema_version": "1",
        "root_agent": ref, "delegation": {"max_depth": 0, "max_agent_calls": 1, "max_tool_calls": 1}}
    source = snapshot([{"id": "source", "uri": "memory:source"}])
    for budget in (1, 0):
        workflow["delegation"]["max_tool_calls"] = budget
        model = ScriptedModel([("tool_lookup", {"query": "source"}), {"answer": 42}])
        agents, tools = Registry(), Registry()
        agents.register(definition, StrandsAgent(model))
        tools.register({**tool_ref, "input_schema": {"type": "object", "required": ["query"], "properties": {"query": {"type": "string"}}}}, lambda value: {"value": 42})
        repo = FileRepository(tmp_path / str(budget))
        runner = Runner(agents, repo, tools=tools)
        if budget:
            run, artifact = runner.run(workflow, source, "Analyze", {"type": "object"})
            assert artifact["payload"] == {"answer": 42}
            assert any(e["type"] == "tool_called" for e in run["execution_trace"])
        else:
            with pytest.raises(ValueError, match="limit"):
                runner.run(workflow, source, "Analyze", {"type": "object"})


def test_snapshot_stable_and_repository_paths(tmp_path):
    items = [{"id": "b", "uri": "file:b"}, {"id": "a", "uri": "file:a"}]
    assert snapshot(items)["id"] == snapshot(items[::-1])["id"]
    with pytest.raises(ValueError):
        snapshot([items[0], items[0]])
    with pytest.raises(ValueError):
        FileRepository(tmp_path).load("runs", "../escape")


@pytest.mark.parametrize("provider,credential", [("openai", "OPENAI_API_KEY"), ("anthropic", "ANTHROPIC_API_KEY"), ("google", "GOOGLE_API_KEY"), ("ollama", None)])
def test_provider_configuration_without_aws(provider, credential):
    pytest.importorskip({"openai": "openai", "anthropic": "anthropic", "google": "google.genai", "ollama": "ollama"}[provider])
    env = {"LOOOMER_PROVIDER": provider, "LOOOMER_MODEL": "test-model"}
    if credential:
        env[credential] = "test-key"
    assert model_from_env(env).get_config()["model_id"] == "test-model"


def test_provider_must_be_explicit():
    with pytest.raises(ValueError, match="LOOOMER_PROVIDER"):
        model_from_env({"LOOOMER_MODEL": "test"})


def test_turn_limit_and_delegation_permission(tmp_path):
    from looomer.core import AgentRequest, Execution, Runtime
    from looomer.core.harness import TurnLimit
    limit = TurnLimit(1)
    limit.before_model(None)
    with pytest.raises(RuntimeError, match="model turns"):
        limit.before_model(None)
    ref = {"id": "root", "version": "1"}
    workflow = {"workflow_id": "test", "version": "1", "root_agent": ref,
        "delegation": {"max_depth": 0, "max_agent_calls": 1, "max_tool_calls": 0, "allowed_agents": [ref]}}
    execution = Execution(Runner(Registry(), FileRepository(tmp_path)), workflow,
                          {"id": "source", "items": []}, {})
    with pytest.raises(ValueError, match="not allowed"):
        Runtime(execution, ref, "call", 0).delegate(ref, "task")
    with pytest.raises(ValueError, match="limit"):
        execution.invoke(ref, "task", depth=1)


def test_local_workflow_matches_registered_photo_app():
    from looomer.apps.photo_curation.photos import resource
    workflow, schema = load_workflow()
    manifest = resource("app.json")
    assert workflow == manifest["workflow"]
    assert schema == manifest["schema"]
