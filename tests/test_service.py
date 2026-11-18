import json
import os
from pathlib import Path
from threading import Event, Thread
from uuid import uuid4

import pytest

pytest.importorskip("psycopg")
from starlette.testclient import TestClient
from looomer.core import manifests
from looomer.core.service import create_api, work_once
from looomer.core.service_store import Database, Conflict

pytestmark = pytest.mark.skipif(not os.getenv("LOOOMER_TEST_DATABASE_URL"), reason="requires test PostgreSQL")


def test_independent_workers_and_versioned_app():
    db = Database(os.environ["LOOOMER_TEST_DATABASE_URL"])
    db.init()
    manifest = json.loads(Path("src/looomer/apps/photo_curation/app.json").read_text())
    unique = uuid4().hex
    manifest["app"] = manifest["workflow"]["workflow_id"] = unique
    manifest["source"] = unique
    manifest["required_tags"] = ["candidate"]
    manifests.check_manifest(manifest)
    db.register(manifest)
    db.register(manifest)
    with pytest.raises(Conflict):
        db.register(dict(manifest, source="elsewhere"))
    client = TestClient(create_api(db, "secret"))
    assert client.post("/v1/events", json={}).status_code == 401
    client.headers["Authorization"] = "Bearer secret"

    def batch(events):
        return {"source": unique, "events": [{"occurred_at": "2026-01-01T00:00:00Z",
                "item": {"id": name, "uri": uri}} for name, uri in events]}

    def post(path, body, **headers):
        response = client.post(path, json=body, headers=headers)
        assert response.status_code == 202, response.text
        return response.json()["job_id"]

    def status(job):
        return client.get("/v1/jobs/" + job).json()

    initial = batch([("a", "photo:a"), ("default-only", "photo:d"), ("user-only", "photo:u")])
    for event, tags in zip(initial["events"], [["trip", "candidate"], ["candidate"], ["trip"]]):
        event["tags"] = tags
    first = post("/v1/events", initial, **{"Idempotency-Key": unique})
    assert post("/v1/events", initial, **{"Idempotency-Key": unique}) == first
    assert work_once(db, "ingest")
    assert status(first)["status"] == "completed"
    assert client.get("/v1/jobs/" + first + "/input").json()["source"] == unique
    assert first in [row["id"] for row in client.get("/v1/jobs").json()]
    assert unique in [row["app"] for row in client.get("/v1/apps").json()]
    assert client.get("/v1/events/count", params={"source": unique}).json() == {"count": 3}
    preview = {"app": unique, "version": manifest["version"]}
    assert client.get("/v1/events/count", params=preview).json() == {"count": 2}
    assert client.get("/v1/events/count", params={**preview, "all_tags": "trip"}).json() == {"count": 1}
    conflict = post("/v1/events", batch([("0", "photo:0"), ("a", "changed")]))
    work_once(db, "ingest")
    assert status(conflict)["status"] == "failed"

    started, release = Event(), Event()

    class Harness:
        def run(self, request):
            assert [item["id"] for item in request.snapshot["items"]] == ["a"]
            started.set()
            assert release.wait(10)
            return {"brief": {"target_count": 1}, "concept": "one photo",
                    "selection": [{"photo_id": "a", "order": 1, "role": "opener", "rationale": "catalog"}]}

    request = {"app": unique, "version": manifest["version"], "task": "Curate", "end": "2026-02-01T00:00:00Z"}
    request["all_tags"] = ["trip"]
    analysis = post("/v1/runs", request)
    worker = Thread(target=work_once, args=(db, "analysis", Harness))
    worker.start()
    try:
        assert started.wait(10)
        assert status(analysis)["status"] == "running"
        assert not work_once(db, "analysis", Harness)
        second = post("/v1/events", batch([("b", "photo:b")]))
        assert work_once(db, "ingest")
        assert status(second)["status"] == "completed"
    finally:
        release.set()
        worker.join(10)
    result = status(analysis)["result"]
    assert result["all_tags"] == ["candidate", "trip"]
    record = client.get(f"/v1/apps/{unique}/records/runs/{result['run_id']}").json()
    assert record["artifact_id"] == result["artifact"]["artifact_id"]
    empty = post("/v1/runs", dict(request, start="2026-01-02T00:00:00Z"))
    work_once(db, "analysis", Harness)
    assert status(empty)["status"] == "failed"

    # Losing a worker's transaction releases its claim. The next worker can
    # finish the same job without a second submission or a manual queue reset.
    replay = post("/v1/runs", request)

    def crash():
        raise SystemExit(1)

    with pytest.raises(SystemExit):
        work_once(db, "analysis", crash)
    assert status(replay)["status"] == "running"

    class RecoveryHarness:
        def run(self, request):
            return result["artifact"]["payload"]

    work_once(db, "analysis", RecoveryHarness)
    assert status(replay)["status"] == "completed"
