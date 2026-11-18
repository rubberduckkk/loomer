# Looomer

Product domain: `looomer.com`.

Looomer is a Python data-to-artifact workbench: reusable ingestion and provenance, pluggable analysis agents, typed JSON artifacts, and independent evaluation. Photo-set curation is the first domain.

## Shared service

Install `.[service]` and deploy `looomer-core serve`, `looomer-core worker ingest`, and `looomer-core worker analysis` against the same PostgreSQL database. Sources submit event batches independently of app runs. Apps are versioned JSON manifests and can trigger analysis over a time window through HTTP. Only analysis workers need model access. For setup, API examples, concurrency and replay behavior, see the [deployment reference](docs/deployment.md).

## Install

Requires Python 3.11+ with venv support.

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install -e '.[dev]'
```

The checked-in `requirements-lock.txt` records the tested Python 3.14 environment, including optional providers and test dependencies. For that environment, install it before installing Looomer with `pip install -r requirements-lock.txt` and `pip install -e . --no-deps`.

## Model providers

Strands owns the agent loop. Provider configuration is explicit; there is no implicit Bedrock fallback. Choose a model that supports tool calling.

```sh
export LOOOMER_PROVIDER=openai
export LOOOMER_MODEL=YOUR_MODEL_ID
export OPENAI_API_KEY=YOUR_API_KEY
# Optional: OPENAI_BASE_URL for an OpenAI-compatible endpoint
```

Other supported configurations:

- `LOOOMER_PROVIDER=anthropic`: install `.[anthropic]`, set `ANTHROPIC_API_KEY`.
- `LOOOMER_PROVIDER=google`: install `.[google]`, set `GOOGLE_API_KEY`.
- `LOOOMER_PROVIDER=ollama`: install `.[ollama]`, optionally set `OLLAMA_HOST` (defaults to `http://localhost:11434`).
- `LOOOMER_PROVIDER=bedrock`: use the AWS credential chain and optionally `AWS_REGION`.

Every provider requires `LOOOMER_MODEL`. Non-AWS providers do not require AWS credentials. Model capabilities, including images and tool calling, vary by model and endpoint.

## Photo workflow

```sh
looomer photos scan --source ./my-photo-set --output ./work/catalog.json
looomer photos curate --catalog ./work/catalog.json --store ./work/store \
  --output ./work/artifact.json --run-output ./work/run.json
looomer photos evaluate --store ./work/store --artifact ARTIFACT_ID \
  --output ./work/evaluation.json
```

`python -m looomer` is equivalent to `looomer`. The default workflow and schemas are packaged, so commands work outside the repository. `--workflow PATH` loads a custom workflow with its schema path resolved relative to the workflow file.

The catalog contains **metadata only**, not image pixels. Selection must not imply visual inspection. The scanner hashes files, preserves relative source paths, and records dimensions when Pillow supports the codec. Keep the original folder available. EXIF extraction, multimodal analysis, explicit user brief input, and human review/edit history remain future work. Current constraint evaluation checks the brief in the generated payload.

## Repository layout

```text
src/looomer/
  core/                Shared runtime, service, workers, manifest loader, and storage
  apps/
    activity_recap/    Activity recap manifest
    photo_curation/    Photo manifest, schema, local CLI, ingestion, and evaluation
examples/core/         Generic workflow and agent definition examples
```

## Architecture
For the deployment topology, storage mounts, and staging invocation, see the [deployment reference](docs/deployment.md).

- `src/looomer/core/runtime.py`: generic agent/repository protocols, registries, snapshots, workflow execution, permissions, delegation budgets, trace, and artifact validation.
- `src/looomer/core/harness.py`: Strands integration and provider factory. Each supervisor/specialist invocation gets its own Strands agent, tools, skills, and bounded model loop.
- `src/looomer/core/storage.py`: atomic JSON records in `snapshots/`, `workflows/`, `runs/`, `artifacts/`, and `evaluations/`.
- `src/looomer/apps/photo_curation/photos.py`: photo ingestion, agent definitions, and independent evaluation.

The Python implementation replaces the original Go implementation. Existing JSON record shapes and collection names remain readable; no repository conversion is required. Snapshot hashing retains the original field-based algorithm. Newly scanned metadata may differ because image codec support and timestamp precision differ. Evaluation uses version `0.2.0`, adding independent payload schema validation.

Looomer enforces tool/delegation permissions outside Strands. Tools execute sequentially to preserve shared workflow budgets and trace order. Model calls are limited to 12 per agent invocation. Tool failures invalidate the run even if the SDK feeds the error back to the model. Final JSON is validated with `jsonschema`; there is no custom model loop or custom schema validator.

Run `pytest` for offline tests using a scripted model through the real Strands loop. These exercise delegation, tool execution, failure persistence, evaluation, and provider construction; they do not verify live provider APIs. Optional-provider tests require `pip install -e '.[dev,anthropic,google,ollama]'`.

See [core architecture](docs/design/core-architecture.md), [photo workflow](src/looomer/apps/photo_curation/README.md), and [generic workflow example](examples/core/workflow.json).
