# Photo-set curation workflow

## User goal

Turn a large travel or event photo collection into a coherent, reviewable set for a post or album. The system should help select and sequence images while leaving the final editorial choice with the user.

## Inputs

- Photos, each with a stable `photo_id`, original filename, dimensions, capture time when available, and a source reference.
- Optional user brief: platform, target number of images, audience, mood, story or location context, and constraints such as must-include images.
- Optional shared instructions that apply to this run.

Do not require EXIF metadata to be present. Preserve it when available and distinguish observed metadata from inferred descriptions.

## Intended agent composition

1. **Photo inventory** — verifies the collection, reads available metadata, identifies near-duplicates or technical problems, and returns a concise inventory. It does not discard source files.
2. **Visual analysis** — describes each candidate image using visible evidence, identifies likely subject/location/mood, and notes uncertainty. It returns references by `photo_id`.
3. **Editorial curator** — selects and orders the requested number of photos to form a coherent narrative, honoring must-include and avoid constraints. It explains selection, order, and omissions.
4. **Artifact validator** — checks references, count, schema validity, and user constraints. Validation is deterministic where possible and does not rewrite editorial judgments.

The first implementation can combine inventory and visual analysis if the model/provider makes separate calls inefficient. Keep their responsibilities and output contracts distinct so they can be separated later.

The Python CLI runs a Strands-backed photo supervisor through the generic runner. Each specialist uses its own Strands agent loop. It can delegate catalog review to a metadata specialist and receives a versioned curation skill. The initial scanner catalog contains image metadata, not image pixels, so curation explanations must stay within that evidence. The generic harness routes any declared tool calls and specialist delegation through the core runtime.

## Output

The `photo-curation` payload contains:

- A short editorial concept and optional caption suggestions.
- Ordered selected photos with rationale and role in the story.
- Optional alternate photos and the selected photo each could replace.
- Omitted photo IDs with concise reasons, where available.
- Warnings for missing metadata, uncertain interpretation, or unmet constraints.

Every photo reference must resolve to the input snapshot. The workflow must not claim a location or event as fact when it is only inferred from visual content.

## Evaluation

Evaluation is a separate run and initially combines deterministic checks with human review.

**Deterministic checks:** selected count, valid and unique photo IDs, order indices, schema validity, and must-include/exclude constraints.

**Human-review dimensions:** relevance to brief, narrative coherence, visual variety, diversity of selected scenes, explanation usefulness, and whether the set feels representative of the source collection. Store each score with rubric version and reviewer notes. Do not collapse these into one opaque quality score.

## Acceptance criteria for the first usable version

- A user can supply a collection and a brief without manually renaming files.
- The result includes a valid ordered selection and every reference opens the correct source photo.
- The user can accept, reorder, replace, or remove selections and save the revised result.
- The saved artifact records its input snapshot, workflow, agent/prompt versions, schema version, and review history.
- A failed or partial run is visible and does not masquerade as a completed artifact.
