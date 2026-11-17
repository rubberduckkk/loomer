package loomer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rubberduckkk/loomer/internal/core"
)

type EvaluationCheck struct {
	Name    string   `json:"name"`
	Passed  bool     `json:"passed"`
	Details []string `json:"details,omitempty"`
}

type PhotoEvaluationMetrics struct {
	Checks []EvaluationCheck `json:"checks"`
}

type PhotoEvaluator struct{}

func (PhotoEvaluator) Evaluate(ctx context.Context, input core.EvaluationInput) (core.EvaluationResult, error) {
	if err := ctx.Err(); err != nil {
		return core.EvaluationResult{}, err
	}
	if input.Artifact.ArtifactType != "photo-curation" {
		return core.EvaluationResult{}, fmt.Errorf("unsupported artifact type %q", input.Artifact.ArtifactType)
	}
	catalog, err := PhotoCatalogFromSnapshot(input.Snapshot)
	if err != nil {
		return core.EvaluationResult{}, err
	}
	var payload PhotoCurationPayload
	if err := json.Unmarshal(input.Artifact.Payload, &payload); err != nil {
		return core.EvaluationResult{}, fmt.Errorf("decode photo curation payload: %w", err)
	}
	checks := evaluatePhotoPayload(catalog, input, payload)
	passed := true
	for _, check := range checks {
		passed = passed && check.Passed
	}
	metrics, err := json.Marshal(PhotoEvaluationMetrics{Checks: checks})
	if err != nil {
		return core.EvaluationResult{}, fmt.Errorf("encode evaluation metrics: %w", err)
	}
	evaluationID, err := newID("evaluation_")
	if err != nil {
		return core.EvaluationResult{}, err
	}
	return core.EvaluationResult{
		EvaluationID: evaluationID,
		EvaluatorID:  "photo-curation-review",
		Version:      EvaluatorVersion,
		ArtifactID:   input.Artifact.ArtifactID,
		EvaluatedAt:  time.Now().UTC(),
		Passed:       passed,
		Metrics:      metrics,
	}, nil
}

func evaluatePhotoPayload(catalog PhotoCatalog, input core.EvaluationInput, payload PhotoCurationPayload) []EvaluationCheck {
	artifact := input.Artifact
	provenanceDetails := []string{}
	if input.Run.Status != "completed" {
		provenanceDetails = append(provenanceDetails, fmt.Sprintf("run status is %q, expected completed", input.Run.Status))
	}
	if artifact.RunID != input.Run.RunID {
		provenanceDetails = append(provenanceDetails, "artifact run_id does not match loaded run")
	}
	if input.Run.ArtifactID != artifact.ArtifactID {
		provenanceDetails = append(provenanceDetails, "run artifact_id does not match loaded artifact")
	}
	if input.Run.InputSnapshotID != input.Snapshot.ID {
		provenanceDetails = append(provenanceDetails, "run input_snapshot_id does not match loaded snapshot")
	}
	if input.Workflow.WorkflowID != artifact.Provenance.WorkflowID || input.Workflow.Version != artifact.Provenance.WorkflowVersion {
		provenanceDetails = append(provenanceDetails, "loaded workflow does not match artifact provenance")
	}
	checks := []EvaluationCheck{{
		Name:    "run-artifact-provenance-consistent",
		Passed:  len(provenanceDetails) == 0,
		Details: provenanceDetails,
	}}
	checks = append(checks, EvaluationCheck{
		Name:   "catalog-snapshot-matches-artifact",
		Passed: artifact.Provenance.InputSnapshotID == catalog.SnapshotID,
	})
	if !checks[1].Passed {
		checks[1].Details = []string{fmt.Sprintf("artifact references snapshot %q but source snapshot is %q", artifact.Provenance.InputSnapshotID, catalog.SnapshotID)}
	}
	known := make(map[string]bool, len(catalog.Photos))
	for _, photo := range catalog.Photos {
		known[photo.PhotoID] = true
	}
	selected := make(map[string]bool, len(payload.Selection))
	var details []string
	for _, item := range payload.Selection {
		if !known[item.PhotoID] {
			details = append(details, fmt.Sprintf("selection references unknown photo_id %q", item.PhotoID))
		}
		if selected[item.PhotoID] {
			details = append(details, fmt.Sprintf("photo_id %q appears more than once", item.PhotoID))
		}
		selected[item.PhotoID] = true
		if item.Order < 1 || item.Order > len(payload.Selection) {
			details = append(details, fmt.Sprintf("photo_id %q has out-of-range order %d", item.PhotoID, item.Order))
		}
	}
	for _, alternate := range payload.Alternates {
		if !known[alternate.PhotoID] {
			details = append(details, fmt.Sprintf("alternate references unknown photo_id %q", alternate.PhotoID))
		}
		if !known[alternate.ReplacesPhotoID] {
			details = append(details, fmt.Sprintf("alternate replaces unknown photo_id %q", alternate.ReplacesPhotoID))
		}
	}
	for _, omission := range payload.Omissions {
		if !known[omission.PhotoID] {
			details = append(details, fmt.Sprintf("omission references unknown photo_id %q", omission.PhotoID))
		}
	}
	checks = append(checks, EvaluationCheck{Name: "photo-ids-resolve-and-are-unique", Passed: len(details) == 0, Details: details})

	details = nil
	orders := make(map[int]bool, len(payload.Selection))
	for _, item := range payload.Selection {
		if orders[item.Order] {
			details = append(details, fmt.Sprintf("order %d is used more than once", item.Order))
		}
		orders[item.Order] = true
	}
	for order := 1; order <= len(payload.Selection); order++ {
		if !orders[order] {
			details = append(details, fmt.Sprintf("order %d is missing", order))
		}
	}
	checks = append(checks, EvaluationCheck{Name: "order-is-contiguous", Passed: len(details) == 0, Details: details})

	details = nil
	if len(payload.Selection) != payload.Brief.TargetCount {
		details = append(details, fmt.Sprintf("target_count is %d but selection has %d items", payload.Brief.TargetCount, len(payload.Selection)))
	}
	checks = append(checks, EvaluationCheck{Name: "selection-count-matches-brief", Passed: len(details) == 0, Details: details})

	details = nil
	for _, photoID := range payload.Brief.MustInclude {
		if !selected[photoID] {
			details = append(details, fmt.Sprintf("required photo_id %q is not selected", photoID))
		}
	}
	for _, photoID := range payload.Brief.MustExclude {
		if selected[photoID] {
			details = append(details, fmt.Sprintf("excluded photo_id %q is selected", photoID))
		}
	}
	checks = append(checks, EvaluationCheck{Name: "brief-constraints-satisfied", Passed: len(details) == 0, Details: details})
	return checks
}
