# Deploy the shared core

Deploy three independently replicated process roles against one PostgreSQL database. The HTTP API accepts work, ingestion workers build indexed event history in batches, and analysis workers execute registered apps. An app can be a JSON manifest plus a manual client or an external schedule. The bundled photo manifest and the activity recap example use the same service.

| Process | Command | Runtime needs |
| --- | --- | --- |
| API | `looomer-core serve --host 0.0.0.0 --port 8080` | Database and bearer token |
| Ingestion worker | `looomer-core worker ingest` | Database |
| Analysis worker | `looomer-core worker analysis` | Database, model provider and any configured connectors |

## Bootstrap

Use Python 3.11+ and a persistent PostgreSQL database. The service has been integration tested on PostgreSQL 16. Install the service extra in each role, for example with `pip install '.[service]'` from the source checkout. Add any required model provider extras on analysis workers. Set `LOOOMER_DATABASE_URL` in each role. Run these once from the release checkout using a database role that can create tables and indexes:

```sh
looomer-core init
looomer-core register src/looomer/apps/activity_recap/app.json
looomer-core register src/looomer/apps/photo_curation/app.json
```

Set a strong `LOOOMER_API_TOKEN` on API replicas and trusted clients. Set `LOOOMER_PROVIDER`, `LOOOMER_MODEL`, and provider credentials on analysis workers. See [provider setup](../README.md#model-providers). Start each of the three commands above under a process supervisor. The API defaults to loopback. For a remote deployment, provide private networking or a TLS reverse proxy. The bearer token grants access to all activity and records in this single trust domain.

## Ingest independently of apps

A source submits batches of up to 1,000 events and 4 MiB per request. The event timestamp requires an offset. Source item IDs must be stable and unique within the source. Retransmitting an identical item is safe. Conflicting content for the same ID fails the entire batch.

```sh
api=http://127.0.0.1:8080
auth="Authorization: Bearer $LOOOMER_API_TOKEN"
curl --fail-with-body -H "$auth" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: activity-import-001' "$api/v1/events" \
  -d '{"source":"activity","events":[{"occurred_at":"2026-11-17T09:00:00Z","item":{"id":"walk-01","uri":"journal:walk-01","metadata":{"activity":"morning walk"}}}]}'
```

Submission returns HTTP 202 and a job ID. Poll `GET /v1/jobs/{job_id}` with the same authorization header. A completed ingestion job means its batch is available for analysis. Producers can continue while apps are idle or model calls are in flight.

## Trigger and retrieve a run

Manual clients and schedulers use the same endpoint. Each registered app version fixes its source and maximum input size. The worker selects committed events when execution begins. The optional UTC-offset time bounds include the start and exclude the end. An empty or oversized selection fails explicitly.

```sh
curl --fail-with-body -H "$auth" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: activity-recap-2026-11-17' "$api/v1/runs" \
  -d '{"app":"activity-recap","version":"1","task":"Summarize today.","start":"2026-11-17T00:00:00Z","end":"2026-11-18T00:00:00Z"}'
```

Poll the returned job ID for pending, running, completed, or failed. Completed analyses return the artifact and run ID. Fetch the trace at `GET /v1/apps/{app}/records/runs/{run_id}`. The same route accepts snapshots, artifacts, and workflows. External evaluators can retrieve the linked evidence. The original `looomer photos` commands still support standalone filesystem runs.

## Connect external systems

Source adapters send generic events. Configure external JSON tools in the trusted manifest's `tools` array with an ID, version, description, input_schema, and a connector name such as `CALENDAR`. Add that tool reference to the agent's tools and give the workflow a positive tool-call budget. On analysis workers set `LOOOMER_CONNECTOR_CALENDAR_URL` and optionally `LOOOMER_CONNECTOR_CALENDAR_TOKEN`. The runtime validates the arguments and sends an HTTP POST containing those arguments. The connector returns JSON. Credentials and endpoint addresses stay in worker configuration. Register a new app version when definitions change.

## Scaling and recovery

Replicate each process role independently. Each worker claims one job at a time with PostgreSQL row locking and SKIP LOCKED. To inspect demand, group looomer_jobs by kind and status and check the oldest pending submitted_at. Keep sufficient PostgreSQL connections for API concurrency and worker processes. Each worker briefly uses a second connection to publish running status.

Queue removal and result publication commit together. A database disconnect or terminated worker releases its claim and another worker can replay the job. An analysis replay may select newer committed history within its requested window and may repeat model and external tool calls. Use replay-safe connectors; each tool request includes X-Looomer-Job. Keep ingestion and analysis requests independently idempotent by setting an Idempotency-Key. An ordinary app error records a failed job; submit a new key for an intentional rerun. Back up PostgreSQL for queue, history, configuration, and artifact recovery.

For offline service integration tests, point LOOOMER_TEST_DATABASE_URL at a disposable database with no other workers or queued work and run pytest. The tests exercise the HTTP API and real competing database connections.

## Select events by tag

Supply optional event-level tags beside occurred_at and item, for example
`"tags": ["album:candidate", "trip:kyoto"]`. Tags are case-sensitive strings.
Ingestion trims surrounding whitespace, removes duplicates, and treats order as
irrelevant. Labels belong to the immutable source event. Reusing its ID with a
different tag set conflicts. Existing events have an empty set.

An app manifest can declare `"required_tags": ["album:candidate"]`. Register a
new app version to change its defaults. A run can add `"all_tags": ["trip:kyoto"]`.
The worker requires every tag from both lists, along with the app source and the
requested event-time window. Omitted lists add no restrictions.

In Core admin, select an app and enter additional required tags one per line.
The count previews the selection at that moment. To request it via HTTP, use
`GET /v1/events/count?app=photo-set-curation&version=0.2.0&all_tags=trip%3Akyoto`.
Repeat all_tags for additional labels. A completed job records the combined set
in result.all_tags and its input snapshot records the selected items.
