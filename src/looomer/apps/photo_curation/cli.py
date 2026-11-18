"""Local photo reference workflow."""
import argparse
import json
from pathlib import Path
import sys

from looomer.core import Runner
from .photos import scan, to_snapshot, load_workflow, registries, evaluate
from looomer.core.storage import FileRepository


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n")


def main(argv=None):
    parser = argparse.ArgumentParser(prog="looomer")
    commands = parser.add_subparsers(dest="command", required=True)
    photos = commands.add_parser("photos").add_subparsers(dest="action", required=True)
    scan_parser = photos.add_parser("scan")
    scan_parser.add_argument("--source", required=True)
    scan_parser.add_argument("--output", default="photo-catalog.json")
    curate = photos.add_parser("curate")
    curate.add_argument("--catalog", required=True)
    curate.add_argument("--workflow")
    curate.add_argument("--store", default=".looomer")
    curate.add_argument("--output", default="photo-curation-artifact.json")
    curate.add_argument("--run-output", default="photo-curation-run.json")
    evaluation = photos.add_parser("evaluate")
    evaluation.add_argument("--artifact", required=True)
    evaluation.add_argument("--store", default=".looomer")
    evaluation.add_argument("--output", default="photo-curation-evaluation.json")
    args = parser.parse_args(argv)
    try:
        if args.action == "scan":
            catalog = scan(args.source)
            write_json(args.output, catalog)
            print(f"Cataloged {len(catalog['photos'])} photos\nSnapshot: {catalog['snapshot_id']}")
        elif args.action == "curate":
            from looomer.core.harness import StrandsAgent, model_from_env
            catalog = json.loads(Path(args.catalog).read_text())
            workflow, schema = load_workflow(args.workflow)
            agents, skills = registries(StrandsAgent(model_from_env()))
            run, artifact = Runner(agents, FileRepository(args.store), skills).run(workflow,
                to_snapshot(catalog), "Curate an ordered photo set from this catalog and produce the photo-curation artifact.", schema)
            write_json(args.output, artifact)
            write_json(args.run_output, run)
            print(f"Created artifact {artifact['artifact_id']}\nRun: {run['run_id']}")
        else:
            result = evaluate(FileRepository(args.store), args.artifact)
            write_json(args.output, result)
            print(f"Evaluation {result['evaluation_id']}: passed={result['passed']}")
            return 0 if result["passed"] else 1
        return 0
    except Exception as error:
        print(f"looomer: {error}", file=sys.stderr)
        return 1
