package loomer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rubberduckkk/loomer/internal/core"
)

type PhotoWorkflowConfig struct {
	core.WorkflowDefinition
	ArtifactSchema string `json:"artifact_schema"`
}

type LoadedPhotoWorkflow struct {
	Definition core.WorkflowDefinition
	Schema     json.RawMessage
	Digest     string
}

func LoadPhotoWorkflow(path string) (LoadedPhotoWorkflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LoadedPhotoWorkflow{}, fmt.Errorf("read workflow: %w", err)
	}
	var config PhotoWorkflowConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return LoadedPhotoWorkflow{}, fmt.Errorf("parse workflow: %w", err)
	}
	if config.ArtifactSchema == "" {
		return LoadedPhotoWorkflow{}, fmt.Errorf("workflow must define artifact_schema")
	}
	workflowPath, err := filepath.Abs(path)
	if err != nil {
		return LoadedPhotoWorkflow{}, fmt.Errorf("resolve workflow path: %w", err)
	}
	schemaPath := filepath.Clean(filepath.Join(filepath.Dir(workflowPath), config.ArtifactSchema))
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return LoadedPhotoWorkflow{}, fmt.Errorf("read artifact schema: %w", err)
	}
	if !json.Valid(schema) {
		return LoadedPhotoWorkflow{}, fmt.Errorf("artifact schema is not valid JSON")
	}
	if config.ArtifactType != "photo-curation" {
		return LoadedPhotoWorkflow{}, fmt.Errorf("photo workflow artifact_type must be photo-curation")
	}
	digest := sha256.Sum256(data)
	return LoadedPhotoWorkflow{
		Definition: config.WorkflowDefinition,
		Schema:     schema,
		Digest:     hex.EncodeToString(digest[:]),
	}, nil
}

func (catalog PhotoCatalog) ToInputSnapshot() (core.InputSnapshot, error) {
	items := make([]core.SourceItem, 0, len(catalog.Photos))
	for _, photo := range catalog.Photos {
		metadata, err := json.Marshal(map[string]any{
			"path": photo.Path, "filename": photo.Filename,
			"size_bytes": photo.SizeBytes, "modified_at": photo.ModifiedAt,
			"width": photo.Width, "height": photo.Height, "format": photo.Format,
		})
		if err != nil {
			return core.InputSnapshot{}, fmt.Errorf("encode metadata for %s: %w", photo.PhotoID, err)
		}
		items = append(items, core.SourceItem{
			ID:          photo.PhotoID,
			URI:         "photo://" + photo.PhotoID,
			MediaType:   photoMediaType(photo.Format, photo.Filename),
			ContentHash: photo.ContentHash,
			Metadata:    map[string]json.RawMessage{"photo": metadata},
		})
	}
	snapshot, err := core.NewInputSnapshot(items)
	if err != nil {
		return core.InputSnapshot{}, err
	}
	return snapshot, nil
}

func PhotoCuratorAgentDefinition() core.AgentDefinition {
	return core.AgentDefinition{
		ID:            "photo-supervisor",
		Version:       "0.1.0",
		PromptVersion: "photo-curation/0.1.0",
		SystemPrompt:  "You are the supervisor for photo-set curation. Decide whether the metadata reviewer specialist would help with this task and delegate when useful. Use only photo IDs present in the input. Make a coherent, varied ordered selection and explain each selection using only evidence available in the catalog. Do not claim to have visually inspected photos when the input contains metadata only.",
		Skills:        []core.SkillRef{{ID: "photo-curation-guidance", Version: "0.1.0"}},
		CanDelegate:   true,
	}
}

func PhotoMetadataReviewerDefinition() core.AgentDefinition {
	return core.AgentDefinition{
		ID:            "photo-metadata-reviewer",
		Version:       "0.1.0",
		Description:   "Groups catalog entries and reports metadata evidence that can help plan a varied photo sequence.",
		PromptVersion: "photo-metadata-review/0.1.0",
		SystemPrompt:  "Review the supplied photo catalog and assigned task. Return a concise JSON analysis listing useful groupings, dimensions, timestamps, and any limits of the available metadata. Do not invent visual content or select IDs absent from the catalog.",
	}
}

func PhotoCurationSkill() core.SkillDefinition {
	return core.SkillDefinition{
		ID: "photo-curation-guidance", Version: "0.1.0",
		Instructions: "Build a sequence with variety in orientation, dimensions, and capture time where the catalog supports it. Separate observable metadata from visual or narrative inference. Explain uncertainty and avoid implying that metadata reveals image content.",
	}
}

func photoMediaType(format, filename string) string {
	format = strings.ToLower(format)
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	}
	switch format {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "tif", "tiff":
		return "image/tiff"
	case "heic":
		return "image/heic"
	default:
		return "application/octet-stream"
	}
}

func PhotoCatalogFromSnapshot(snapshot core.InputSnapshot) (PhotoCatalog, error) {
	photos := make([]Photo, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		var metadata struct {
			Path       string    `json:"path"`
			Filename   string    `json:"filename"`
			SizeBytes  int64     `json:"size_bytes"`
			ModifiedAt time.Time `json:"modified_at"`
			Width      int       `json:"width"`
			Height     int       `json:"height"`
			Format     string    `json:"format"`
		}
		if err := json.Unmarshal(item.Metadata["photo"], &metadata); err != nil {
			return PhotoCatalog{}, fmt.Errorf("decode photo metadata for %s: %w", item.ID, err)
		}
		photos = append(photos, Photo{
			PhotoID: item.ID, Path: metadata.Path, Filename: metadata.Filename,
			ContentHash: item.ContentHash, SizeBytes: metadata.SizeBytes,
			ModifiedAt: metadata.ModifiedAt, Width: metadata.Width,
			Height: metadata.Height, Format: metadata.Format,
		})
	}
	return PhotoCatalog{
		SchemaVersion: CatalogSchemaVersion,
		SnapshotID:    snapshot.ID,
		CreatedAt:     snapshot.CreatedAt,
		Photos:        photos,
	}, nil
}
