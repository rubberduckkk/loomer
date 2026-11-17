package loomer

import "time"

const (
	CatalogSchemaVersion  = "0.1.0"
	ArtifactSchemaVersion = "0.1.0"
	EvaluatorVersion      = "0.1.0"
)

type Photo struct {
	PhotoID     string    `json:"photo_id"`
	Path        string    `json:"path"`
	Filename    string    `json:"filename"`
	ContentHash string    `json:"content_hash"`
	SizeBytes   int64     `json:"size_bytes"`
	ModifiedAt  time.Time `json:"modified_at"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	Format      string    `json:"format,omitempty"`
}

type PhotoCatalog struct {
	SchemaVersion string    `json:"schema_version"`
	SnapshotID    string    `json:"snapshot_id"`
	CreatedAt     time.Time `json:"created_at"`
	SourceName    string    `json:"source_name"`
	Photos        []Photo   `json:"photos"`
}

type CurationBrief struct {
	Platform    string   `json:"platform,omitempty"`
	TargetCount int      `json:"target_count"`
	Audience    string   `json:"audience,omitempty"`
	Mood        string   `json:"mood,omitempty"`
	Context     string   `json:"context,omitempty"`
	MustInclude []string `json:"must_include,omitempty"`
	MustExclude []string `json:"must_exclude,omitempty"`
}

type SelectionItem struct {
	PhotoID   string `json:"photo_id"`
	Order     int    `json:"order"`
	Role      string `json:"role"`
	Rationale string `json:"rationale"`
}

type Alternate struct {
	PhotoID         string `json:"photo_id"`
	ReplacesPhotoID string `json:"replaces_photo_id"`
	Rationale       string `json:"rationale"`
}

type Omission struct {
	PhotoID string `json:"photo_id"`
	Reason  string `json:"reason"`
}

type PhotoCurationPayload struct {
	Brief              CurationBrief   `json:"brief"`
	Concept            string          `json:"concept"`
	CaptionSuggestions []string        `json:"caption_suggestions,omitempty"`
	Selection          []SelectionItem `json:"selection"`
	Alternates         []Alternate     `json:"alternates,omitempty"`
	Omissions          []Omission      `json:"omissions,omitempty"`
	Warnings           []string        `json:"warnings,omitempty"`
}
