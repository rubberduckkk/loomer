package loomer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var photoExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".tif": true, ".tiff": true, ".webp": true, ".heic": true,
}

func ScanPhotos(sourceDir string) (PhotoCatalog, error) {
	root, err := filepath.Abs(sourceDir)
	if err != nil {
		return PhotoCatalog{}, fmt.Errorf("resolve source directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return PhotoCatalog{}, fmt.Errorf("stat source directory: %w", err)
	}
	if !info.IsDir() {
		return PhotoCatalog{}, fmt.Errorf("source is not a directory: %s", sourceDir)
	}

	photos := make([]Photo, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !photoExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			return nil
		}
		photo, err := inspectPhoto(root, path)
		if err != nil {
			return err
		}
		photos = append(photos, photo)
		return nil
	})
	if err != nil {
		return PhotoCatalog{}, fmt.Errorf("scan photos: %w", err)
	}
	sort.Slice(photos, func(i, j int) bool { return photos[i].Path < photos[j].Path })
	if len(photos) == 0 {
		return PhotoCatalog{}, fmt.Errorf("no supported photo files found in %s", sourceDir)
	}

	hash := sha256.New()
	for _, photo := range photos {
		fmt.Fprintf(hash, "%s\x00%s\x00", photo.PhotoID, photo.Path)
	}
	catalog := PhotoCatalog{
		SchemaVersion: CatalogSchemaVersion,
		SnapshotID:    "snapshot_" + hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:     time.Now().UTC(),
		SourceName:    filepath.Base(root),
		Photos:        photos,
	}
	return catalog, nil
}

func inspectPhoto(root, path string) (Photo, error) {
	file, err := os.Open(path)
	if err != nil {
		return Photo{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return Photo{}, fmt.Errorf("stat %s: %w", path, err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return Photo{}, fmt.Errorf("hash %s: %w", path, err)
	}
	contentHash := hex.EncodeToString(hasher.Sum(nil))
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return Photo{}, fmt.Errorf("make relative path for %s: %w", path, err)
	}

	identityHash := sha256.Sum256([]byte(filepath.ToSlash(relativePath) + "\x00" + contentHash))
	photo := Photo{
		PhotoID:     "photo_" + hex.EncodeToString(identityHash[:]),
		Path:        filepath.ToSlash(relativePath),
		Filename:    filepath.Base(path),
		ContentHash: contentHash,
		SizeBytes:   stat.Size(),
		ModifiedAt:  stat.ModTime().UTC(),
	}
	if _, err := file.Seek(0, io.SeekStart); err == nil {
		if config, format, decodeErr := image.DecodeConfig(file); decodeErr == nil {
			photo.Width, photo.Height, photo.Format = config.Width, config.Height, format
		}
	}
	return photo, nil
}
