"""Atomic local JSON persistence, compatible with the original record layout."""
import json
import os
from pathlib import Path
import tempfile


class FileRepository:
    collections = {"snapshots", "workflows", "runs", "artifacts", "evaluations"}

    def __init__(self, root):
        self.root = Path(root).resolve()

    def path(self, collection, identifier):
        if collection not in self.collections or not identifier or identifier in {".", ".."} or any(c in identifier for c in "/\\\x00"):
            raise ValueError("invalid repository collection or identifier")
        return self.root / collection / (identifier + ".json")

    def save(self, collection, identifier, value):
        path = self.path(collection, identifier)
        path.parent.mkdir(parents=True, exist_ok=True)
        pending = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, prefix=".pending-", delete=False) as f:
                pending = f.name
                json.dump(value, f, indent=2, allow_nan=False)
                f.write("\n")
                f.flush()
                os.fsync(f.fileno())
            os.replace(pending, path)
        finally:
            if pending and os.path.exists(pending):
                os.unlink(pending)

    def load(self, collection, identifier):
        return json.loads(self.path(collection, identifier).read_text())
