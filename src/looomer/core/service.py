"""Shared Looomer entry points. Only the analysis worker constructs a model."""
import argparse
from datetime import datetime, timezone
import json
import logging
import os
import secrets
import time

import psycopg
from psycopg.types.json import Jsonb
from starlette.applications import Starlette
from starlette.concurrency import run_in_threadpool
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Route

from . import manifests
from .runtime import Runner, snapshot
from .service_store import Conflict, Database, RecordRepository, ingest, select_items, selection_filter


def nonempty(value, name):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{name} must be a nonempty string")
    return value


def timestamp(value):
    parsed = datetime.fromisoformat(nonempty(value, "timestamp").replace("Z", "+00:00"))
    if parsed.utcoffset() is None:
        raise ValueError("timestamp requires a UTC offset")
    return parsed.astimezone(timezone.utc).isoformat()


def clean_payload(kind, body):
    if not isinstance(body, dict):
        raise ValueError("expected a JSON object")
    if kind == "ingest":
        nonempty(body.get("source"), "source")
        events = body.get("events")
        if not isinstance(events, list) or not 1 <= len(events) <= 1000:
            raise ValueError("expected 1 to 1000 events")
        for event in events:
            if not isinstance(event, dict) or not isinstance(event.get("item"), dict):
                raise ValueError("expected an event with a source item")
            if "tags" in event:
                event["tags"] = manifests.tag_list(event["tags"])
            item = event["item"]
            for field in ("id", "uri"):
                nonempty(item.get(field), field)
            for field in ("media_type", "content_hash"):
                if field in item:
                    nonempty(item[field], field)
            event["occurred_at"] = timestamp(event.get("occurred_at"))
        return body
    for field in ("app", "version", "task"):
        nonempty(body.get(field), field)
    if "all_tags" in body:
        body["all_tags"] = manifests.tag_list(body["all_tags"])
    if body.keys() - {"app", "version", "task", "start", "end", "all_tags"}:
        raise ValueError("unknown run parameter")
    for field in ("start", "end"):
        if field in body:
            body[field] = timestamp(body[field])
    if "start" in body and "end" in body and datetime.fromisoformat(body["start"]) >= datetime.fromisoformat(body["end"]):
        raise ValueError("start must precede end")
    return body


def create_api(db, token):
    if not token:
        raise ValueError("LOOOMER_API_TOKEN is required")

    def authorized(request):
        supplied = request.headers.get("authorization", "").encode()
        return secrets.compare_digest(supplied, ("Bearer " + token).encode())

    async def submit(request: Request):
        if not authorized(request):
            return JSONResponse({"error": "unauthorized"}, 401)
        raw = bytearray()
        async for chunk in request.stream():
            if len(raw) + len(chunk) > 4 * 1024 * 1024:
                return JSONResponse({"error": "request exceeds 4 MiB"}, 413)
            raw.extend(chunk)
        kind = "ingest" if request.url.path == "/v1/events" else "analysis"
        try:
            payload = clean_payload(kind, json.loads(raw))
            request_key = request.headers.get("idempotency-key")
            if request_key is not None and not 1 <= len(request_key) <= 200:
                raise ValueError("idempotency key must have 1 to 200 characters")
            job = await run_in_threadpool(db.submit, kind, payload, request_key)
            return JSONResponse({"job_id": job}, 202, headers={"Location": "/v1/jobs/" + job})
        except Conflict as error:
            return JSONResponse({"error": str(error)}, 409)
        except (ValueError, TypeError, psycopg.DataError) as error:
            return JSONResponse({"error": str(error)}, 400)
        except KeyError:
            return JSONResponse({"error": "unknown app version"}, 404)

    async def status(request):
        if not authorized(request):
            return JSONResponse({"error": "unauthorized"}, 401)
        try:
            return JSONResponse(await run_in_threadpool(db.status, request.path_params["job_id"]))
        except KeyError:
            return JSONResponse({"error": "unknown job"}, 404)

    async def record(request):
        if not authorized(request):
            return JSONResponse({"error": "unauthorized"}, 401)
        params = request.path_params
        if params["collection"] not in {"runs", "snapshots", "artifacts", "workflows", "evaluations"}:
            return JSONResponse({"error": "unknown collection"}, 404)

        def read():
            with db.connect() as conn:
                return RecordRepository(conn, params["app"]).load(params["collection"], params["identifier"])
        try:
            return JSONResponse(await run_in_threadpool(read))
        except KeyError:
            return JSONResponse({"error": "unknown record"}, 404)

    def inspect(request):
        if not authorized(request):
            return JSONResponse({"error": "unauthorized"}, 401)
        with db.connect() as conn:
            if request.url.path == "/v1/jobs":
                rows = conn.execute("""
                    SELECT id, kind, status, submitted_at, coalesce(payload->>'source', payload->>'app')
                    FROM looomer_jobs ORDER BY submitted_at DESC, id DESC LIMIT 100
                """).fetchall()
                return JSONResponse([dict(id=i, kind=k, status=s, submitted_at=t.isoformat(), name=n)
                                     for i, k, s, t, n in rows])
            if request.url.path == "/v1/apps":
                rows = conn.execute("SELECT manifest FROM looomer_apps ORDER BY app,version").fetchall()
                return JSONResponse([dict({k: m[k] for k in ("app", "version", "source", "max_items")},
                                          required_tags=m.get("required_tags", []))
                                     for (m,) in rows])
            if request.url.path == "/v1/events/count":
                query = request.query_params
                try:
                    required = []
                    if "app" in query or "version" in query:
                        manifest = db.manifest(conn, nonempty(query.get("app"), "app"),
                                               nonempty(query.get("version"), "version"))
                        source = manifest["source"]
                        required = manifest.get("required_tags", [])
                    else:
                        source = nonempty(query.get("source"), "source")
                    tags = manifests.tag_list(required + query.getlist("all_tags"))
                    start, end = [timestamp(query[k]) if query.get(k) else None for k in ("start", "end")]
                    if start and end and datetime.fromisoformat(start) >= datetime.fromisoformat(end):
                        raise ValueError("start must precede end")
                except KeyError:
                    return JSONResponse({"error": "unknown app version"}, 404)
                except ValueError as error:
                    return JSONResponse({"error": str(error)}, 400)
                bounds = {k: v for k, v in (("start", start), ("end", end)) if v}
                where, args = selection_filter(source, bounds, tags)
                count = conn.execute("SELECT count(*) FROM looomer_history WHERE " + where, args).fetchone()[0]
                return JSONResponse({"count": count})
            row = conn.execute("SELECT payload FROM looomer_jobs WHERE id=%s",
                               (request.path_params["job_id"],)).fetchone()
            return JSONResponse(row[0]) if row else JSONResponse({"error": "unknown job"}, 404)

    return Starlette(routes=[Route("/v1/jobs", inspect), Route("/v1/apps", inspect),
                             Route("/v1/events/count", inspect), Route("/v1/jobs/{job_id}/input", inspect),
                             Route("/v1/events", submit, methods=["POST"]),
                             Route("/v1/runs", submit, methods=["POST"]),
                             Route("/v1/jobs/{job_id}", status),
                             Route("/v1/apps/{app}/records/{collection}/{identifier}", record)])


def default_harness():
    from .harness import StrandsAgent, model_from_env
    return StrandsAgent(model_from_env())


def analyze(db, conn, job, payload, harness_factory):
    manifest = db.manifest(conn, payload["app"], payload["version"])
    tags = manifests.tag_list(manifest.get("required_tags", []) + payload.get("all_tags", []))
    items = select_items(conn, manifest["source"], payload, manifest["max_items"], tags)
    source = snapshot(items)
    agents, skills, tools = manifests.registries(manifest, harness_factory(), job)
    repository = RecordRepository(conn, manifest["app"])
    run, artifact = Runner(agents, repository, skills, tools).run(
        manifest["workflow"], source, payload["task"], manifest["schema"])
    return {"run_id": run["run_id"], "artifact": artifact, "all_tags": tags}


def work_once(db, kind, harness_factory=default_harness):
    # The queue row stays locked for the job. Disconnecting releases it for replay.
    # Status lives in a separate row so clients can observe progress immediately.
    with db.connect() as conn:
        entry = conn.execute("""
            SELECT job_id FROM looomer_queue WHERE kind=%s ORDER BY enqueued_at,job_id
            LIMIT 1 FOR UPDATE SKIP LOCKED
            """, (kind,)).fetchone()
        if not entry:
            return False
        job = entry[0]
        with db.connect() as status_conn:
            payload = status_conn.execute(
                "UPDATE looomer_jobs SET status='running' WHERE id=%s RETURNING payload", (job,)).fetchone()[0]
        try:
            if kind == "ingest":
                with conn.transaction():
                    result = ingest(conn, payload)
            else:
                result = analyze(db, conn, job, payload, harness_factory)
            conn.execute("UPDATE looomer_jobs SET status='completed',result=%s WHERE id=%s",
                         (Jsonb(result), job))
        except psycopg.Error:
            # Roll back the claim when PostgreSQL cannot commit the outcome.
            raise
        except Exception as error:
            logging.exception("job %s failed", job)
            conn.execute("UPDATE looomer_jobs SET status='failed',error=%s WHERE id=%s", (str(error), job))
        conn.execute("DELETE FROM looomer_queue WHERE job_id=%s", (job,))
    return True


def main(argv=None):
    parser = argparse.ArgumentParser(prog="looomer-core")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("init")
    register = sub.add_parser("register")
    register.add_argument("manifest")
    serve = sub.add_parser("serve")
    serve.add_argument("--host", default="127.0.0.1")
    serve.add_argument("--port", type=int, default=8080)
    worker = sub.add_parser("worker")
    worker.add_argument("kind", choices=("ingest", "analysis"))
    worker.add_argument("--once", action="store_true")
    args = parser.parse_args(argv)
    db = Database(os.environ["LOOOMER_DATABASE_URL"])
    if args.command == "init":
        db.init()
    elif args.command == "register":
        with open(args.manifest) as f:
            manifest = json.load(f)
        manifests.check_manifest(manifest)
        db.register(manifest)
    elif args.command == "serve":
        import uvicorn
        uvicorn.run(create_api(db, os.environ["LOOOMER_API_TOKEN"]), host=args.host, port=args.port)
    else:
        while True:
            worked = work_once(db, args.kind)
            if args.once:
                return
            if not worked:
                time.sleep(0.5)
