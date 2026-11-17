package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rubberduckkk/loomer/internal/core"
)

type FileRepository struct {
	root string
}

func NewFileRepository(root string) (*FileRepository, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	return &FileRepository{root: abs}, nil
}

func (r *FileRepository) SaveSnapshot(ctx context.Context, value core.InputSnapshot) error {
	return r.save(ctx, "snapshots", value.ID, value)
}

func (r *FileRepository) LoadSnapshot(ctx context.Context, id string) (core.InputSnapshot, error) {
	var value core.InputSnapshot
	err := r.load(ctx, "snapshots", id, &value)
	return value, err
}

func (r *FileRepository) SaveWorkflow(ctx context.Context, value core.WorkflowDefinition) error {
	return r.save(ctx, "workflows", value.WorkflowID+"@"+value.Version, value)
}

func (r *FileRepository) LoadWorkflow(ctx context.Context, id, version string) (core.WorkflowDefinition, error) {
	var value core.WorkflowDefinition
	err := r.load(ctx, "workflows", id+"@"+version, &value)
	return value, err
}

func (r *FileRepository) SaveRun(ctx context.Context, value core.RunRecord) error {
	return r.save(ctx, "runs", value.RunID, value)
}

func (r *FileRepository) LoadRun(ctx context.Context, id string) (core.RunRecord, error) {
	var value core.RunRecord
	err := r.load(ctx, "runs", id, &value)
	return value, err
}

func (r *FileRepository) SaveArtifact(ctx context.Context, value core.ArtifactEnvelope) error {
	return r.save(ctx, "artifacts", value.ArtifactID, value)
}

func (r *FileRepository) LoadArtifact(ctx context.Context, id string) (core.ArtifactEnvelope, error) {
	var value core.ArtifactEnvelope
	err := r.load(ctx, "artifacts", id, &value)
	return value, err
}

func (r *FileRepository) SaveEvaluation(ctx context.Context, value core.EvaluationResult) error {
	return r.save(ctx, "evaluations", value.EvaluationID, value)
}

func (r *FileRepository) LoadEvaluation(ctx context.Context, id string) (core.EvaluationResult, error) {
	var value core.EvaluationResult
	err := r.load(ctx, "evaluations", id, &value)
	return value, err
}

func (r *FileRepository) save(ctx context.Context, collection, id string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeID(id) {
		return fmt.Errorf("invalid %s identifier %q", collection, id)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", collection, err)
	}
	directory := filepath.Join(r.root, collection)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create %s directory: %w", collection, err)
	}
	temporary, err := os.CreateTemp(directory, ".pending-*")
	if err != nil {
		return fmt.Errorf("create temporary %s record: %w", collection, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s record: %w", collection, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync %s record: %w", collection, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s record: %w", collection, err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, id+".json")); err != nil {
		return fmt.Errorf("store %s record: %w", collection, err)
	}
	return nil
}

func (r *FileRepository) load(ctx context.Context, collection, id string, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeID(id) {
		return fmt.Errorf("invalid %s identifier %q", collection, id)
	}
	data, err := os.ReadFile(filepath.Join(r.root, collection, id+".json"))
	if err != nil {
		return fmt.Errorf("read %s record %q: %w", collection, id, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s record %q: %w", collection, id, err)
	}
	return nil
}

func safeID(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}
