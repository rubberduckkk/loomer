"""Photo ingestion and evaluation plugged into the generic core."""
from datetime import datetime, timezone
import hashlib
from importlib.resources import files
import json
from pathlib import Path

from PIL import Image, UnidentifiedImageError

from looomer.core import Registry, snapshot, now, new_id, validate

EXTENSIONS = {".jpg", ".jpeg", ".png", ".gif", ".tif", ".tiff", ".webp", ".heic"}


def resource(name):
    return json.loads(files(__package__).joinpath(name).read_text())


def load_workflow(path=None):
    if path:
        path = Path(path)
        workflow = json.loads(path.read_text())
        schema = json.loads((path.parent / workflow.pop("artifact_schema")).read_text())
    else:
        workflow = resource("workflow.json")
        schema = resource(workflow.pop("artifact_schema"))
    if workflow["artifact_type"] != "photo-curation":
        raise ValueError("photo workflow must produce photo-curation")
    return workflow, schema


def scan(source):
    root = Path(source).resolve()
    if not root.is_dir():
        raise ValueError("source must be a directory")
    photos = []
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.is_symlink() or path.suffix.lower() not in EXTENSIONS:
            continue
        relative = path.relative_to(root).as_posix()
        with path.open("rb") as f:
            content_hash = hashlib.file_digest(f, "sha256").hexdigest()
        stat = path.stat()
        photo = {"photo_id": "photo_" + hashlib.sha256((relative + "\0" + content_hash).encode()).hexdigest(),
            "path": relative, "filename": path.name, "content_hash": content_hash, "size_bytes": stat.st_size,
            "modified_at": datetime.fromtimestamp(stat.st_mtime, timezone.utc).isoformat().replace("+00:00", "Z")}
        try:
            with Image.open(path) as image:
                photo.update(width=image.width, height=image.height, format=image.format.lower())
        except (UnidentifiedImageError, OSError):
            pass
        photos.append(photo)
    catalog = {"schema_version": "0.1.0", "created_at": now(), "source_name": root.name, "photos": photos}
    catalog["snapshot_id"] = to_snapshot(catalog)["id"]
    return catalog


def to_snapshot(catalog):
    items = []
    for p in catalog["photos"]:
        fmt = p.get("format") or Path(p["filename"]).suffix.lower().lstrip(".")
        fmt = {"jpg": "jpeg", "tif": "tiff"}.get(fmt, fmt)
        metadata = {k: p[k] for k in ("path", "filename", "size_bytes", "modified_at")}
        metadata.update(width=p.get("width", 0), height=p.get("height", 0), format=p.get("format", ""))
        items.append({"id": p["photo_id"], "uri": "photo://" + p["photo_id"], "media_type": "image/" + fmt,
            "content_hash": p["content_hash"], "metadata": {"photo": metadata}})
    return snapshot(items)


def registries(harness):
    manifest = resource("app.json")
    agents, skills = Registry(), Registry()
    for definition in manifest["agents"]:
        agents.register(definition, harness)
    for definition in manifest["skills"]:
        skills.register(definition)
    return agents, skills


def evaluate(repository, artifact_id):
    artifact = repository.load("artifacts", artifact_id)
    if artifact["artifact_type"] != "photo-curation":
        raise ValueError("expected photo-curation artifact")
    provenance = artifact["provenance"]
    run = repository.load("runs", artifact["run_id"])
    source = repository.load("snapshots", provenance["input_snapshot_id"])
    workflow = repository.load("workflows", provenance["workflow_id"] + "@" + provenance["workflow_version"])
    checks = []

    def check(name, passed):
        checks.append({"name": name, "passed": bool(passed)})

    check("run-artifact-provenance-consistent", run["status"] == "completed" and
        run["run_id"] == artifact["run_id"] and run["artifact_id"] == artifact_id and
        run["input_snapshot_id"] == source["id"] and workflow["workflow_id"] == provenance["workflow_id"] and
        workflow["version"] == provenance["workflow_version"])
    check("catalog-snapshot-matches-artifact", source["id"] == provenance["input_snapshot_id"])
    payload = artifact["payload"]
    try:
        validate(resource("photo-curation-artifact.schema.json"), payload)
        check("payload-schema-valid", True)
    except Exception as error:
        check("payload-schema-valid", False)
        checks[-1]["details"] = [str(error)]
    if checks[-1]["passed"]:
        known = {i["id"] for i in source["items"]}
        selection = payload["selection"]
        selected = [s["photo_id"] for s in selection]
        refs = selected + [p["photo_id"] for p in payload.get("omissions", [])]
        for alternate in payload.get("alternates", []):
            refs.extend([alternate["photo_id"], alternate["replaces_photo_id"]])
        check("photo-ids-resolve-and-are-unique", set(refs) <= known and len(set(selected)) == len(selected))
        check("order-is-contiguous", sorted(s["order"] for s in selection) == list(range(1, len(selection) + 1)))
        brief = payload["brief"]
        check("selection-count-matches-brief", len(selection) == brief["target_count"])
        check("brief-constraints-satisfied", set(brief.get("must_include", [])) <= set(selected) and
              not set(brief.get("must_exclude", [])).intersection(selected))
    result = {"evaluation_id": new_id("evaluation_"), "evaluator_id": "photo-curation-review", "version": "0.2.0",
        "artifact_id": artifact_id, "evaluated_at": now(), "passed": all(c["passed"] for c in checks),
        "metrics": {"checks": checks}}
    repository.save("evaluations", result["evaluation_id"], result)
    return result
