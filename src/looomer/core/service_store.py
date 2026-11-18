"""PostgreSQL storage shared by API replicas and both worker pools."""
from contextlib import contextmanager
from datetime import timezone

import psycopg
from psycopg.types.json import Jsonb

from .runtime import new_id


DDL = """
CREATE TABLE IF NOT EXISTS looomer_apps (
    app text NOT NULL, version text NOT NULL, manifest jsonb NOT NULL,
    PRIMARY KEY (app, version)
);
CREATE TABLE IF NOT EXISTS looomer_history (
    source text NOT NULL, item_id text NOT NULL, occurred_at timestamptz NOT NULL,
    item jsonb NOT NULL, PRIMARY KEY (source, item_id)
);
ALTER TABLE looomer_history ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS looomer_history_tags ON looomer_history USING gin(tags);
CREATE INDEX IF NOT EXISTS looomer_history_window ON looomer_history(source, occurred_at, item_id);
CREATE TABLE IF NOT EXISTS looomer_jobs (
    id text PRIMARY KEY, kind text NOT NULL, request_key text, payload jsonb NOT NULL,
    submitted_at timestamptz NOT NULL DEFAULT now(), status text NOT NULL DEFAULT 'pending',
    result jsonb, error text, UNIQUE(kind, request_key)
);
CREATE TABLE IF NOT EXISTS looomer_queue (
    job_id text PRIMARY KEY REFERENCES looomer_jobs(id), kind text NOT NULL,
    enqueued_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS looomer_queue_next ON looomer_queue(kind,enqueued_at,job_id);
CREATE TABLE IF NOT EXISTS looomer_records (
    app text NOT NULL, collection text NOT NULL, id text NOT NULL, value jsonb NOT NULL,
    PRIMARY KEY (app, collection, id)
);
"""


class Conflict(ValueError):
    pass


class Database:
    def __init__(self, dsn):
        self.dsn = dsn

    @contextmanager
    def connect(self):
        with psycopg.connect(self.dsn) as conn:
            yield conn

    def init(self):
        with self.connect() as conn:
            conn.execute(DDL)

    def register(self, manifest):
        app, version = manifest["app"], manifest["version"]
        with self.connect() as conn:
            conn.execute("INSERT INTO looomer_apps VALUES (%s,%s,%s) ON CONFLICT DO NOTHING",
                         (app, version, Jsonb(manifest)))
            if self.manifest(conn, app, version) != manifest:
                raise Conflict("app version already registered with different configuration")

    @staticmethod
    def manifest(conn, app, version):
        row = conn.execute("SELECT manifest FROM looomer_apps WHERE app=%s AND version=%s",
                           (app, version)).fetchone()
        if not row:
            raise KeyError("unknown app version")
        return row[0]

    def submit(self, kind, payload, request_key):
        job_id = new_id("job_")
        with self.connect() as conn:
            if kind == "analysis":
                self.manifest(conn, payload["app"], payload["version"])
            row = conn.execute("""
                INSERT INTO looomer_jobs(id,kind,request_key,payload) VALUES (%s,%s,%s,%s)
                ON CONFLICT(kind,request_key) DO NOTHING RETURNING id
                """, (job_id, kind, request_key, Jsonb(payload))).fetchone()
            if row:
                conn.execute("INSERT INTO looomer_queue(job_id,kind) VALUES (%s,%s)", (job_id, kind))
                return row[0]
            existing, original = conn.execute(
                "SELECT id,payload FROM looomer_jobs WHERE kind=%s AND request_key=%s",
                (kind, request_key)).fetchone()
            if original != payload:
                raise Conflict("idempotency key previously used for a different request")
            return existing

    def status(self, job_id):
        with self.connect() as conn:
            row = conn.execute("SELECT kind,status,result,error FROM looomer_jobs WHERE id=%s",
                               (job_id,)).fetchone()
            if not row:
                raise KeyError("unknown job")
            return dict(zip(("kind", "status", "result", "error"), row), id=job_id)


class RecordRepository:
    def __init__(self, conn, app):
        self.conn, self.app = conn, app

    def save(self, collection, identifier, value):
        self.conn.execute("""
            INSERT INTO looomer_records VALUES (%s,%s,%s,%s)
            ON CONFLICT(app,collection,id) DO UPDATE SET value=excluded.value
            """, (self.app, collection, identifier, Jsonb(value)))

    def load(self, collection, identifier):
        row = self.conn.execute("SELECT value FROM looomer_records WHERE app=%s AND collection=%s AND id=%s",
                                (self.app, collection, identifier)).fetchone()
        if not row:
            raise KeyError("unknown record")
        return row[0]


def ingest(conn, payload):
    # Insert in a consistent order and check every submitted value, including
    # duplicate IDs within the batch. The worker wraps both statements atomically.
    args = (payload["source"], Jsonb(payload["events"]))
    conn.execute("""
        INSERT INTO looomer_history(source,item_id,occurred_at,item,tags)
        SELECT %s, item->>'id', occurred_at, item, coalesce(tags, '{}')
        FROM jsonb_to_recordset(%s) AS e(occurred_at timestamptz,item jsonb,tags text[])
        ORDER BY item->>'id' ON CONFLICT DO NOTHING
        """, args)
    conflict = conn.execute("""
        SELECT h.item_id FROM looomer_history h
        JOIN jsonb_to_recordset(%s) AS e(occurred_at timestamptz,item jsonb,tags text[])
          ON h.item_id=e.item->>'id'
        WHERE h.source=%s AND (h.item <> e.item OR h.occurred_at <> e.occurred_at OR h.tags <> coalesce(e.tags, '{}'))
        LIMIT 1
        """, (args[1], args[0])).fetchone()
    if conflict:
        raise Conflict("existing source item has different content: " + conflict[0])
    return {"received": len(payload["events"])}


def selection_filter(source, payload, tags):
    conditions = ["source=%s"]
    args = [source]
    for field, clause in (("start", "occurred_at >= %s"), ("end", "occurred_at < %s")):
        if field in payload:
            conditions.append(clause)
            args.append(payload[field])
    if tags:
        conditions.append("tags @> %s::text[]")
        args.append(tags)
    return " AND ".join(conditions), args


def select_items(conn, source, payload, limit, tags=()):
    where, args = selection_filter(source, payload, tags)
    args.append(limit + 1)
    rows = conn.execute("SELECT item,occurred_at,tags FROM looomer_history WHERE " + where
                        + " ORDER BY occurred_at,item_id LIMIT %s", args).fetchall()
    if len(rows) > limit:
        raise ValueError("selection exceeds app max_items; narrow the time window")
    items = []
    for item, occurred_at, event_tags in rows:
        # Keep the event envelope distinct from app-supplied metadata.
        items.append({**item, "metadata": {"source": source, "occurred_at": occurred_at.astimezone(timezone.utc).isoformat(),
                                          **({"tags": event_tags} if event_tags else {}), "original": item.get("metadata", {})}})
    return items
