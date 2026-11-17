package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rubberduckkk/loomer/internal/agent"
	"github.com/rubberduckkk/loomer/internal/core"
	"github.com/rubberduckkk/loomer/internal/loomer"
	"github.com/rubberduckkk/loomer/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "loomer:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	switch args[0] {
	case "photos":
		return runPhotos(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q; run 'loomer help'", args[0])
	}
}

func runPhotos(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected photos subcommand: scan, curate, or evaluate")
	}
	switch args[0] {
	case "scan":
		return scanPhotos(args[1:])
	case "curate":
		return curatePhotos(args[1:])
	case "evaluate":
		return evaluatePhotos(args[1:])
	default:
		return fmt.Errorf("unknown photos command %q", args[0])
	}
}

func scanPhotos(args []string) error {
	flags := flag.NewFlagSet("photos scan", flag.ContinueOnError)
	source := flags.String("source", "", "directory containing source photos")
	output := flags.String("output", "photo-catalog.json", "catalog JSON output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("--source is required")
	}
	catalog, err := loomer.ScanPhotos(*source)
	if err != nil {
		return err
	}
	snapshot, err := catalog.ToInputSnapshot()
	if err != nil {
		return err
	}
	catalog.SnapshotID = snapshot.ID
	if err := loomer.WriteJSON(*output, catalog); err != nil {
		return err
	}
	fmt.Printf("Cataloged %d photos\nSnapshot: %s\nOutput: %s\n", len(catalog.Photos), catalog.SnapshotID, *output)
	return nil
}

func curatePhotos(args []string) error {
	flags := flag.NewFlagSet("photos curate", flag.ContinueOnError)
	catalogPath := flags.String("catalog", "", "photo catalog JSON")
	workflowPath := flags.String("workflow", "examples/photo-set-curation/workflow.json", "workflow definition JSON")
	storePath := flags.String("store", ".loomer", "local Loomer repository directory")
	output := flags.String("output", "photo-curation-artifact.json", "artifact JSON output path")
	runOutput := flags.String("run-output", "photo-curation-run.json", "run record JSON output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *catalogPath == "" {
		return fmt.Errorf("--catalog is required")
	}
	var catalog loomer.PhotoCatalog
	if err := loomer.ReadJSON(*catalogPath, &catalog); err != nil {
		return err
	}
	workflow, err := loomer.LoadPhotoWorkflow(*workflowPath)
	if err != nil {
		return err
	}
	snapshot, err := catalog.ToInputSnapshot()
	if err != nil {
		return err
	}
	model, err := agent.OpenAICompatibleFromEnv()
	if err != nil {
		return err
	}
	registry := core.NewAgentRegistry()
	registry.Register(loomer.PhotoCuratorAgentDefinition(), agent.ReActAgent{Model: model})
	registry.Register(loomer.PhotoMetadataReviewerDefinition(), agent.ReActAgent{Model: model})
	skills := core.NewSkillRegistry()
	skills.Register(loomer.PhotoCurationSkill())
	repository, err := storage.NewFileRepository(*storePath)
	if err != nil {
		return err
	}
	task := "Curate an ordered photo set from this catalog and produce the photo-curation artifact."
	run, artifact, err := (core.Runner{Agents: registry, Skills: skills, Repository: repository}).Run(context.Background(), workflow.Definition, snapshot, task, workflow.Schema)
	if err != nil {
		return err
	}
	if err := loomer.WriteJSON(*output, artifact); err != nil {
		return err
	}
	if err := loomer.WriteJSON(*runOutput, run); err != nil {
		return err
	}
	fmt.Printf("Created artifact %s\nRun: %s\nStored records: %s\n", artifact.ArtifactID, run.RunID, *storePath)
	return nil
}

func evaluatePhotos(args []string) error {
	flags := flag.NewFlagSet("photos evaluate", flag.ContinueOnError)
	artifactID := flags.String("artifact", "", "photo curation artifact ID")
	storePath := flags.String("store", ".loomer", "local Loomer repository directory")
	output := flags.String("output", "photo-curation-evaluation.json", "evaluation JSON output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *artifactID == "" {
		return fmt.Errorf("--artifact is required")
	}
	repository, err := storage.NewFileRepository(*storePath)
	if err != nil {
		return err
	}
	artifact, err := repository.LoadArtifact(context.Background(), *artifactID)
	if err != nil {
		return err
	}
	run, err := repository.LoadRun(context.Background(), artifact.RunID)
	if err != nil {
		return err
	}
	snapshot, err := repository.LoadSnapshot(context.Background(), artifact.Provenance.InputSnapshotID)
	if err != nil {
		return err
	}
	workflow, err := repository.LoadWorkflow(context.Background(), artifact.Provenance.WorkflowID, artifact.Provenance.WorkflowVersion)
	if err != nil {
		return err
	}
	evaluation, err := (loomer.PhotoEvaluator{}).Evaluate(context.Background(), core.EvaluationInput{
		Snapshot: snapshot, Workflow: workflow, Run: run, Artifact: artifact,
	})
	if err != nil {
		return err
	}
	if err := repository.SaveEvaluation(context.Background(), evaluation); err != nil {
		return err
	}
	if err := loomer.WriteJSON(*output, evaluation); err != nil {
		return err
	}
	fmt.Printf("Evaluation %s: passed=%t\nStored records: %s\nOutput: %s\n", evaluation.EvaluationID, evaluation.Passed, *storePath, *output)
	if !evaluation.Passed {
		return fmt.Errorf("evaluation found one or more failed checks")
	}
	return nil
}

func printUsage() {
	fmt.Print(`Loomer local photo workflow

Usage:
  loomer photos scan --source DIR [--output catalog.json]
  loomer photos curate --catalog FILE [--workflow FILE] [--store DIR] [--output FILE]
  loomer photos evaluate --artifact ID [--store DIR] [--output FILE]
`)
}
