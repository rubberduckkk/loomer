package core

import "context"

// Repository is the persistence boundary between orchestration and storage.
// Implementations may use files, a database, or a remote object store.
type Repository interface {
	SaveSnapshot(context.Context, InputSnapshot) error
	LoadSnapshot(context.Context, string) (InputSnapshot, error)
	SaveWorkflow(context.Context, WorkflowDefinition) error
	LoadWorkflow(context.Context, string, string) (WorkflowDefinition, error)
	SaveRun(context.Context, RunRecord) error
	LoadRun(context.Context, string) (RunRecord, error)
	SaveArtifact(context.Context, ArtifactEnvelope) error
	LoadArtifact(context.Context, string) (ArtifactEnvelope, error)
	SaveEvaluation(context.Context, EvaluationResult) error
	LoadEvaluation(context.Context, string) (EvaluationResult, error)
}
