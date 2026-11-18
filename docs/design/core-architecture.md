# Core architecture

## Goal

Looomer should support multiple analysis domains without making the data pipeline or evaluation system depend on one domain. A workflow is a reproducible run over source data, a selected agent composition, prompt revisions, and an output artifact schema.

## Workload and scaling target

The shared core ingests a high volume of records about activity over time. Ingestion is expected to account for most of the platform load. It can continue accepting and normalizing records while every app is idle. The core preserves stable source references so multiple workflows can analyze the same history.

Apps run periodically or on demand. A trigger identifies an app configuration, a task, and the relevant slice of history. The core resolves that input to a snapshot for the run, executes the workflow, and stores its artifact. Trigger frequency and cost per run are separate: an infrequent retrospective may still read a large time window.

Size ingestion capacity against incoming volume and processing lag. Size workflow execution against pending runs, run duration, and model-provider capacity. Each app can change its trigger schedule and scale its own connected systems independently. The shared service implements these paths with a stateless HTTP API, separate ingestion and analysis worker processes, and PostgreSQL for the queues, indexed history, and run records. The local CLI also supports direct runs.

## Building blocks

1. **Data flow** — accepts and normalizes source data, records stable source identifiers and metadata, and makes the same input available to agent runs. The data layer does not encode domain-specific judgments.
2. **Agent** — an independently developed unit with its own system prompt, implementation/version, input contract, and output contract. Agents can run alone or as a composition. A task can also provide shared instructions that apply across the composition.
3. **Artifact** — the durable result of a run. Every artifact uses the common envelope in `src/looomer/core/artifact-envelope.schema.json`; its `payload` follows a domain-specific schema such as the photo-curation schema.
4. **Evaluation** — a separate consumer of run and artifact records. It can inspect source references, agent composition, prompts, schema versions, and outputs, then calculate scenario-specific measures and collect human review. Evaluation must not be embedded in an agent's generation logic.
5. **Run record** — ties the input snapshot, workflow configuration, agent and prompt versions, artifact, and evaluation together for reproducibility and comparison.

## Data flow

```mermaid
flowchart TD
    Sources["Source data"] --> Ingest["Ingest and normalize"]
    Ingest --> Snapshot["Input snapshot and provenance"]
    Snapshot --> Supervisor["Root supervisor agent"]
    Shared["Task shared instructions"] --> Supervisor
    Skills["Versioned skills"] --> Supervisor
    Tools["Allowed tools"] --> Supervisor
    Supervisor --> Specialists["Registered specialists"]
    Specialists --> Supervisor
    Supervisor --> Artifact["Typed artifact"]
    Snapshot --> Eval["Independent evaluator"]
    Supervisor --> Eval
    Specialists --> Eval
    Artifact --> Eval
    Eval --> Review["Human review and feedback"]
```

The diagram describes logical responsibilities, not a commitment to a particular queue, database, orchestration engine, or model provider.

## Contracts and versioning

- Each input item has a stable ID and a source reference. Agents and artifacts refer to IDs rather than relying on array position or display name.
- The common envelope records `artifact_type`, schema version, run ID, creation time, provenance, and a domain payload.
- Workflow definitions identify the root agent, delegation policy, shared instructions, and artifact schema version. Agent definitions separately identify system prompt versions, skill references, allowed tools, and delegation permission.
- Schema changes that alter meaning require a new version. Old artifacts remain interpretable against the schema version they were produced with.
- Evaluators are versioned independently from agents. An evaluation result records evaluator version and metric definitions so results can be compared over time.

## Agent runtime, skills, and tools

An agent is a versioned implementation running inside a harness loop. The harness manages model turns, tool calls, intermediate results, and stopping conditions; an agent is not defined as a single LLM request. Each agent owns its system prompt and declares its available skills and allowed tools.

- **Skills** are versioned instruction and knowledge bundles. They are loaded into the agent context; they do not execute actions.
- **Tools** are callable capabilities with typed inputs/outputs and explicit permissions. They perform operations or retrieve information; their instructions do not replace the agent's system prompt.
- **Agent implementation** combines its system prompt, selected skills, allowed tools, model/runtime configuration, and loop policy.
- **Workflow shared instructions** provide task-specific guidance for one run. The artifact schema is also passed to the root agent and any specialists that need to produce or inspect the final payload.

### Supervisor-led collaboration

The default workflow should start a root or supervisor agent with the user's task, input snapshot, shared instructions, and output schema. The supervisor can decide which specialists to invoke and how to combine their results. It invokes them through a `delegate_agent` capability backed by the registered agent directory; the model does not create arbitrary agents or call hidden implementations directly.

Each specialist runs its own full harness loop with its own system prompt, skills, and tool allowlist. A specialist can return a structured result, evidence, and uncertainty. The supervisor may call multiple specialists, compare their results, request follow-up work, and synthesize the final artifact. The core records every handoff and intermediate result so the evaluation system can reconstruct what happened.

Delegation remains bounded by workflow policy: allowed versioned agent references, maximum handoff depth, maximum agent calls, maximum tool calls, and whether a specialist may delegate further. Start with specialists unable to delegate by default; allow nested delegation only when a workflow explicitly enables it. Token budgets and deadlines can be added when the harness reports the needed usage data.

Simple workflows can still use a deterministic DAG when their sequence is known in advance; supervisor delegation is the default for analysis tasks where the right specialist mix depends on the input.

## Evaluation and feedback

Evaluation runs outside the generation path and consumes immutable run evidence, including the selected root agent, agent and prompt versions, skill versions, tool calls, delegation tree, input snapshot, shared instructions, schema version, and final artifact. The initial loop is:

1. Run an evaluator against the saved artifact and its provenance.
2. Store metric results, evaluator version, and any evidence supporting each result.
3. Let a person accept, edit, or reject the artifact and add structured review notes.
4. Use reviewed examples to improve prompts, agent implementations, and metrics.

Automatically feeding evaluation feedback into agent code (an RSI loop) is a later capability. The first version should preserve human control and make every change attributable.

## Initial boundaries

- Keep source ingestion, domain agents, artifact validation, and evaluation as separable modules.
- Persist enough provenance to reproduce and audit an artifact; avoid putting provider-specific execution details into domain payloads.
- Treat prompts, agent code, schemas, workflow composition, and evaluator versions as distinct versioned inputs.
- Do not assume all outputs are text. The envelope can reference media and structured content through typed payload fields.

## Suggested implementation sequence

1. Build and stabilize generic source snapshot, workflow, agent, run, artifact, and evaluator contracts in `src/looomer/core/runtime.py` — skeleton complete.
2. Add the supervisor runtime, separate skill/tool registries, bounded delegation, and execution trace — skeleton complete.
3. Adapt photo ingestion and curation as the first domain package — initial path complete.
4. Add specialist definitions, skill/tool loading, and richer multimodal photo inputs.
5. Add human review records, then select durable infrastructure based on real workload and usage.

## Current core implementation

Looomer is implemented in Python. `src/looomer/core/runtime.py` defines the generic contracts and runs registered agents with bounded delegation, skill/tool registries, execution traces, artifact validation, and persistence. `src/looomer/core/harness.py` adapts these capabilities to the Strands Agents SDK, which owns each agent's model/tool loop. Provider configuration explicitly selects OpenAI, Anthropic, Google, Ollama, or Bedrock.

The core owns permission checks and shared workflow budgets. Strands tools bridge back to those checks; specialist delegation starts another Strands agent with its own context. Tools execute sequentially. A hook limits each agent to 12 model calls, and runtime failures remain failed runs. The SDK is isolated from domain payloads and the independent evaluator.

JSON Schema validation uses the Python `jsonschema` implementation with Draft 2020-12 and format checks. The JSON repository preserves the original collection layout. The CLI's photo input remains metadata only; visual analysis and human review remain planned work.
