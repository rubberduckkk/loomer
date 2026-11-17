# Loomer

Loomer is a data-to-artifact workbench: reusable data ingestion and provenance, pluggable analysis agents, typed artifacts, and an independent evaluation loop.

The first end-to-end workflow is **photo-set curation**. It takes a collection of photos and produces a reviewable selection with a narrative sequence and per-photo rationale.

## Design

- [Core architecture](docs/design/core-architecture.md)
- [Photo-set curation workflow](docs/workflows/photo-set-curation.md)
- [Generic workflow example](examples/core/workflow.json)
- [Supervisor definition](examples/core/agents/analysis-supervisor.json)
- [Specialist definition](examples/core/agents/source-inspector.json)
- [Synthesis specialist definition](examples/core/agents/result-summarizer.json)
- [Skill definition](examples/core/skills/evidence-synthesis.json)
- [Tool definition](examples/core/tools/source-search.json)
- [Common artifact envelope](schemas/artifact-envelope.schema.json)
- [Photo curation artifact](schemas/photo-curation-artifact.schema.json)
- [Example workflow definition](examples/photo-set-curation/workflow.json)

The generic workflow example uses the supervisor-led delegation model described in the architecture document.

## Core first

The reusable core lives in `internal/core`. It defines domain-neutral source snapshots, a root-agent workflow, versioned agent/skill/tool contracts, bounded agent delegation, an execution trace, evaluator interfaces, a generic artifact envelope whose payload is JSON, and JSON Schema validation for the contract keywords used by this repo. `internal/storage` provides a JSON file repository implementing the core persistence boundary. Domain packages adapt their own inputs, metadata, payload schemas, and evaluation rules to these contracts.

The file repository stores JSON records in `snapshots/`, `workflows/`, `runs/`, `artifacts/`, and `evaluations/` under the root passed to `storage.NewFileRepository`. The validator supports the schema keywords currently used in `schemas/`; it is not a complete implementation of every JSON Schema draft keyword.

The `photos` CLI adapts its photo catalog and curation payload to the generic snapshot, runner, artifact, repository, and evaluator contracts. `internal/agent` supplies a ReAct loop and OpenAI-compatible chat-completions adapter; model tool calls are routed through the core runtime and its permission checks.

## Photo reference path

The current local CLI creates a photo catalog, asks a model-backed supervisor to produce a curation artifact, and evaluates deterministic constraints independently from curation. The supervisor can delegate metadata review to a specialist and receives a versioned curation skill. The initial photo catalog contains metadata only, so it does not enable visual inspection of the images.

```sh
go run ./cmd/loomer photos scan --source ./my-photo-set --output ./work/catalog.json
go run ./cmd/loomer photos curate \
  --catalog ./work/catalog.json \
  --store ./work/loomer-store \
  --output ./work/artifact.json \
  --run-output ./work/run.json
go run ./cmd/loomer photos evaluate \
  --store ./work/loomer-store \
  --artifact artifact_ID_FROM_CURATE \
  --output ./work/evaluation.json
```

The curate command prints the artifact ID used by evaluate. Configure `OPENAI_API_KEY` and `LOOMER_MODEL` before running it. Set `OPENAI_BASE_URL` when using an OpenAI-compatible endpoint other than the default. The generated payload must use photo IDs from the catalog; `target_count` must match the number selected and `order` values must be contiguous.

The scanner recognizes common photo extensions, creates stable IDs from each relative path and file content, and stores the content hash separately. It records paths relative to the supplied folder; keep that folder available to open the original images. Unsupported image codecs may have no decoded width/height in the catalog.

The core records the model-backed agent, prompt version, tool/delegation events, and artifact provenance. The evaluator loads the persisted snapshot, workflow, run, and artifact, then saves its independent result in the repository.
