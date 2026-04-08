package charm

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gschiano/charm-registry/internal/core"
)

// ParseArchive extracts charm metadata from a charm archive payload.
//
// The following errors may be returned:
// - The archive cannot be opened or read.
// - `metadata.yaml` is missing.
// - `metadata.yaml` cannot be parsed.
func ParseArchive(payload []byte) (core.CharmArchive, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return core.CharmArchive{}, fmt.Errorf("open charm archive: %w", err)
	}

	var archive core.CharmArchive
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		content, err := readZipFile(file)
		if err != nil {
			return core.CharmArchive{}, err
		}
		switch {
		case strings.EqualFold(name, "metadata.yaml"):
			archive.MetadataYAML = string(content)
		case strings.EqualFold(name, "manifest.yaml"):
			archive.ManifestYAML = string(content)
		case strings.EqualFold(name, "config.yaml"):
			archive.ConfigYAML = string(content)
		case strings.EqualFold(name, "actions.yaml"):
			archive.ActionsYAML = string(content)
		case strings.EqualFold(name, "bundle.yaml"):
			archive.BundleYAML = string(content)
		case strings.EqualFold(name, "README.md"):
			archive.ReadmeMD = string(content)
		}
	}

	if archive.MetadataYAML == "" {
		return core.CharmArchive{}, fmt.Errorf("metadata.yaml not found in charm archive")
	}
	if err := yaml.Unmarshal([]byte(archive.MetadataYAML), &archive.Manifest); err != nil {
		return core.CharmArchive{}, fmt.Errorf("parse metadata.yaml: %w", err)
	}
	// manifest.yaml holds the bases/platforms for the charm (separate from
	// metadata.yaml).  Parse it after metadata so it always wins over any
	// stale "bases" key that may appear in older metadata.yaml files.
	if archive.ManifestYAML != "" {
		var m struct {
			Bases []core.CharmBase `yaml:"bases"`
		}
		if err := yaml.Unmarshal([]byte(archive.ManifestYAML), &m); err == nil {
			archive.Manifest.Bases = m.Bases
		}
	}
	populateContainerResources(&archive.Manifest)
	return archive, nil
}

// populateContainerResources ensures each container that references a resource
// has a corresponding entry in the resource map.
func populateContainerResources(manifest *core.CharmManifest) {
	if manifest.Resources == nil {
		manifest.Resources = map[string]core.CharmResourceDeclaration{}
	}
	if manifest.Containers == nil {
		return
	}
	for _, container := range manifest.Containers {
		if container.Resource == "" {
			continue
		}
		if _, exists := manifest.Resources[container.Resource]; !exists {
			manifest.Resources[container.Resource] = core.CharmResourceDeclaration{
				Type: "oci-image",
			}
		}
	}
}

// ExtractWebsites normalizes website metadata into a string slice.
func ExtractWebsites(raw any) []string {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return append([]string(nil), value...)
	default:
		return nil
	}
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file.Name, err)
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
