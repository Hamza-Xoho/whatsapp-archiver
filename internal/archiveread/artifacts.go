package archiveread

import "context"

// Artifact is an additive DTO for a future explicitly supported archive adapter.
// No artifact storage schema or processing behavior is implied by this interface.
type Artifact struct {
	ArtifactRef      string         `json:"artifact_ref"`
	Kind             string         `json:"kind"`
	Language         string         `json:"language"`
	Text             string         `json:"text"`
	Structured       map[string]any `json:"structured"`
	SourceMessageRef string         `json:"source_message_ref"`
	Module           string         `json:"module"`
	ModuleVersion    string         `json:"module_version"`
	CreatedAt        string         `json:"created_at"`
}
type ArtifactReader interface {
	ListArtifacts(context.Context, string) ([]Artifact, error)
	GetArtifact(context.Context, string) (Artifact, error)
}
