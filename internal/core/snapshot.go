package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// NewInputSnapshot validates and freezes source references for a run. Its ID
// is deterministic for the same set of IDs, URIs, and content hashes.
func NewInputSnapshot(items []SourceItem) (InputSnapshot, error) {
	if len(items) == 0 {
		return InputSnapshot{}, fmt.Errorf("snapshot must contain at least one source item")
	}
	copyItems := append([]SourceItem(nil), items...)
	sort.Slice(copyItems, func(i, j int) bool { return copyItems[i].ID < copyItems[j].ID })
	seen := make(map[string]bool, len(copyItems))
	hasher := sha256.New()
	for _, item := range copyItems {
		if item.ID == "" || item.URI == "" {
			return InputSnapshot{}, fmt.Errorf("source item id and uri are required")
		}
		if seen[item.ID] {
			return InputSnapshot{}, fmt.Errorf("duplicate source item id %q", item.ID)
		}
		seen[item.ID] = true
		metadata, err := json.Marshal(item.Metadata)
		if err != nil {
			return InputSnapshot{}, fmt.Errorf("source item %q has invalid metadata: %w", item.ID, err)
		}
		fmt.Fprintf(hasher, "%s\x00%s\x00%s\x00%s\x00%s\x00", item.ID, item.URI, item.MediaType, item.ContentHash, metadata)
	}
	return InputSnapshot{
		SchemaVersion: ContractVersion,
		ID:            "snapshot_" + hex.EncodeToString(hasher.Sum(nil)),
		CreatedAt:     time.Now().UTC(),
		Items:         copyItems,
	}, nil
}
